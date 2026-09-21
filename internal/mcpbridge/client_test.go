package mcpbridge

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigRejectsUnsafeOrUnapprovedCommands(t *testing.T) {
	for _, cfg := range []Config{{Enabled: false}, {Enabled: true, Command: "node", AllowedTools: []string{"read_file"}}, {Enabled: true, Command: filepath.Join(t.TempDir(), "node"), Args: []string{"bad\narg"}, AllowedTools: []string{"read_file"}}, {Enabled: true, Command: filepath.Join(t.TempDir(), "node")}} {
		if cfg.Validate() == nil {
			t.Fatal("invalid config accepted")
		}
	}
}
func TestLoadRejectsUnknownFieldsAndExtraJSON(t *testing.T) {
	for _, text := range []string{`{"enabled":true,"secret":"no"}`, `{} {}`} {
		p := filepath.Join(t.TempDir(), "mcp.json")
		os.WriteFile(p, []byte(text), 0600)
		if _, err := Load(p); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}
func TestChildDoesNotInheritProviderCredentials(t *testing.T) {
	env := ProcessEnvironment([]string{"PATH=/bin", "APPDATA=test", "OPENAI_API_KEY=secret", "GITHUB_TOKEN=secret", "OLLAMA_DZ23_GEMINI_API_KEY_FILE=secret", "SystemRoot=C:\\Windows"})
	if len(env) != 3 || strings.Contains(strings.Join(env, "\n"), "secret") {
		t.Fatalf("unexpected environment %v", env)
	}
}
func TestNamesAreBoundedDistinctAndValid(t *testing.T) {
	a, b := ToolName("a/b"), ToolName("a.b")
	if a == b {
		t.Fatal("name collision")
	}
	if len(ToolName(strings.Repeat("x", 100))) > 64 {
		t.Fatal("name too long")
	}
	if unsafeName.MatchString(a) {
		t.Fatal("invalid model tool name")
	}
}
func TestToolErrorsAndTruncation(t *testing.T) {
	if _, err := ResultText(nil); err == nil {
		t.Fatal("nil result accepted")
	}
	if _, err := ResultText(&mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "operation failed"}}}); err == nil {
		t.Fatal("error swallowed")
	}
	text, err := ResultText(&mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", 30000)}}})
	if err != nil || len(text) > 24200 || !strings.Contains(text, "truncado") {
		t.Fatal("result not bounded")
	}
}
func TestProtocolCallAndAllowlist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "read_test", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "actual test result"}}}, nil
	})
	ct, st := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	s := &Session{client: cs, tools: []*mcp.Tool{{Name: "read_test"}}}
	text, err := s.Call(ctx, "read_test", map[string]any{})
	if err != nil || !strings.Contains(text, "actual test result") {
		t.Fatalf("call failed: %v", err)
	}
	if _, err = s.Call(ctx, "not_allowed", nil); err == nil {
		t.Fatal("unlisted tool accepted")
	}
	if _, err = s.Call(ctx, "read_test", map[string]any{"huge": strings.Repeat("a", 70000)}); err == nil {
		t.Fatal("oversized call accepted")
	}
}
