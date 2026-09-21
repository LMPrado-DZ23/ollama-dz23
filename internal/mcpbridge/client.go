// Package mcpbridge connects the desktop to explicitly configured MCP servers.
package mcpbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Config contains no API keys. OAuth remains managed by the configured client.
type Config struct {
	Enabled      bool     `json:"enabled"`
	Command      string   `json:"command"`
	Args         []string `json:"args"`
	AllowedTools []string `json:"allowed_tools"`
}

type Session struct {
	client *mcp.ClientSession
	tools  []*mcp.Tool
}

func Load(path string) (Config, error) {
	var cfg Config
	f, err := os.Open(path)
	if err != nil {
		return cfg, errors.New("Desktop Commander não configurado: configure mcp.json em Ollama DZ23")
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 65537))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&cfg); err != nil {
		return cfg, errors.New("configuração MCP inválida")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return cfg, errors.New("configuração MCP contém dados extras")
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if !c.Enabled {
		return errors.New("Desktop Commander desativado na configuração MCP")
	}
	if !filepath.IsAbs(c.Command) {
		return errors.New("o executável MCP deve usar caminho absoluto")
	}
	if len(c.Args) > 32 || len(c.AllowedTools) == 0 || len(c.AllowedTools) > 64 {
		return errors.New("configure de 1 a 64 ferramentas MCP permitidas")
	}
	for _, arg := range c.Args {
		if strings.ContainsAny(arg, "\x00\r\n") {
			return errors.New("argumento MCP inválido")
		}
	}
	return nil
}

// Do not pass model-provider keys or other application secrets to an MCP process.
func ProcessEnvironment(environ []string) []string {
	allowed := map[string]bool{"PATH": true, "HOME": true, "USERPROFILE": true, "APPDATA": true, "LOCALAPPDATA": true, "TEMP": true, "TMP": true, "SYSTEMROOT": true, "WINDIR": true, "COMSPEC": true, "PATHEXT": true, "HOMEDRIVE": true, "HOMEPATH": true, "LANG": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true}
	var result []string
	for _, e := range environ {
		key, _, ok := strings.Cut(e, "=")
		if ok && allowed[strings.ToUpper(key)] {
			result = append(result, e)
		}
	}
	return result
}

func Connect(ctx context.Context, cfg Config) (*Session, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	cmd.Env = ProcessEnvironment(os.Environ())
	cmd.Stderr = io.Discard // OAuth diagnostics can contain credentials/authorization URLs.
	prepareCommand(cmd)
	client := mcp.NewClient(&mcp.Implementation{Name: "ollama-dz23", Version: "0.1.3"}, nil)
	initCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cs, err := client.Connect(initCtx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, errors.New("não foi possível conectar ao MCP; conclua o login pelo atalho Conectar Desktop Commander")
	}
	s := &Session{client: cs}
	allowed := map[string]bool{}
	for _, name := range cfg.AllowedTools {
		allowed[name] = true
	}
	count := 0
	for tool, err := range cs.Tools(initCtx, nil) {
		if err != nil {
			cs.Close()
			return nil, errors.New("falha ao consultar ferramentas MCP")
		}
		count++
		if count > 512 {
			cs.Close()
			return nil, errors.New("catálogo MCP excede o limite")
		}
		if allowed[tool.Name] {
			raw, err := json.Marshal(tool.InputSchema)
			if err != nil || len(raw) > 65536 {
				cs.Close()
				return nil, errors.New("schema MCP inválido ou grande demais")
			}
			s.tools = append(s.tools, tool)
		}
	}
	if len(s.tools) == 0 {
		cs.Close()
		return nil, errors.New("nenhuma ferramenta MCP permitida foi encontrada")
	}
	return s, nil
}
func (s *Session) Close() error       { return s.client.Close() }
func (s *Session) Tools() []*mcp.Tool { return s.tools }
func (s *Session) Call(ctx context.Context, name string, args map[string]any) (string, error) {
	allowed := false
	for _, t := range s.tools {
		if t.Name == name {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", errors.New("ferramenta MCP não permitida")
	}
	raw, err := json.Marshal(args)
	if err != nil || len(raw) > 65536 {
		return "", errors.New("argumentos MCP excedem o limite")
	}
	callCtx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	result, err := s.client.CallTool(callCtx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", errors.New("a ferramenta MCP falhou ou excedeu o tempo limite")
	}
	return ResultText(result)
}
func ResultText(result *mcp.CallToolResult) (string, error) {
	if result == nil {
		return "", errors.New("resultado MCP vazio")
	}
	var text strings.Builder
	for _, content := range result.Content {
		if c, ok := content.(*mcp.TextContent); ok {
			text.WriteString(c.Text)
			text.WriteByte('\n')
		}
	}
	value := text.String()
	if len(value) > 24000 {
		value = value[:24000] + "\n[Resultado truncado: solicite um trecho menor.]"
	}
	if value == "" {
		value = "A ferramenta retornou conteúdo não textual. Esta integração aceita resultados de texto."
	}
	if result.IsError {
		return "", fmt.Errorf("MCP: %s", value)
	}
	return value, nil
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func ToolName(name string) string {
	clean := unsafeName.ReplaceAllString(name, "_")
	if len(clean) > 40 {
		clean = clean[:40]
	}
	sum := sha256.Sum256([]byte(name))
	return "pc_" + clean + "_" + hex.EncodeToString(sum[:4])
}
