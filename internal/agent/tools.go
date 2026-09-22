package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	registry := &Registry{tools: make(map[string]Tool)}
	registry.Register(workspaceListTool{})
	registry.Register(workspaceReadTool{})
	registry.Register(workspaceWriteTool{})
	registry.Register(terminalExecTool{allowed: map[string]bool{"pwd": true, "ls": true, "git": true}})
	registry.Register(sandboxExecTool{})
	registry.Register(browserOperatorTool{})
	registry.Register(desktopCompanionTool{})
	return registry
}

func (r *Registry) Register(tool Tool) {
	if r == nil || tool == nil {
		return
	}
	if r.tools == nil {
		r.tools = make(map[string]Tool)
	}
	r.tools[tool.Descriptor().Name] = tool
}

func (r *Registry) Get(name string) (Tool, bool) {
	if r == nil {
		return nil, false
	}
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *Registry) Descriptors() []ToolDescriptor {
	if r == nil {
		return nil
	}
	result := make([]ToolDescriptor, 0, len(r.tools))
	for _, tool := range r.tools {
		result = append(result, tool.Descriptor())
	}
	return result
}

type workspaceListTool struct{}

func (workspaceListTool) Descriptor() ToolDescriptor {
	return ToolDescriptor{Name: "workspace.list", Version: "1", Description: "Listar entradas do workspace autorizado", Risk: RiskRead}
}

func (workspaceListTool) Execute(_ context.Context, toolContext ToolContext, input map[string]any) (ToolResult, error) {
	path, err := safeWorkspacePath(toolContext.Workspace, stringInput(input, "path", "."))
	if err != nil {
		return ToolResult{}, err
	}
	maxEntries := intInput(input, "max_entries", 100)
	if maxEntries < 1 {
		maxEntries = 1
	}
	if maxEntries > 500 {
		maxEntries = 500
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return ToolResult{}, err
	}
	result := make([]map[string]any, 0, minInt(len(entries), maxEntries))
	for i, entry := range entries {
		if i >= maxEntries {
			break
		}
		result = append(result, map[string]any{"name": entry.Name(), "directory": entry.IsDir()})
	}
	return ToolResult{Value: map[string]any{"path": path, "entries": result, "truncated": len(entries) > maxEntries}}, nil
}

type workspaceReadTool struct{}

func (workspaceReadTool) Descriptor() ToolDescriptor {
	return ToolDescriptor{Name: "workspace.read", Version: "1", Description: "Ler um arquivo do workspace autorizado", Risk: RiskRead}
}

func (workspaceReadTool) Execute(_ context.Context, toolContext ToolContext, input map[string]any) (ToolResult, error) {
	path, err := safeWorkspacePath(toolContext.Workspace, stringInput(input, "path", ""))
	if err != nil {
		return ToolResult{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{}, err
	}
	if len(data) > 1<<20 {
		return ToolResult{}, errors.New("workspace.read limit exceeded")
	}
	return ToolResult{Value: map[string]any{"path": path, "content": string(data), "bytes": len(data)}}, nil
}

type workspaceWriteTool struct{}

func (workspaceWriteTool) Descriptor() ToolDescriptor {
	return ToolDescriptor{Name: "workspace.write", Version: "1", Description: "Escrever arquivo no workspace após aprovação", Risk: RiskWrite, RequiresApproval: true}
}

func (workspaceWriteTool) Execute(_ context.Context, toolContext ToolContext, input map[string]any) (ToolResult, error) {
	path, err := safeWorkspacePath(toolContext.Workspace, stringInput(input, "path", ""))
	if err != nil {
		return ToolResult{}, err
	}
	content := stringInput(input, "content", "")
	if len(content) > 1<<20 {
		return ToolResult{}, errors.New("workspace.write limit exceeded")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return ToolResult{}, err
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return ToolResult{}, err
	}
	manifest, err := BuildArtifactManifest(toolContext.Workspace, toolContext.MissionID, toolContext.StepID, filepath.Base(path), stringInput(input, "path", ""))
	if err != nil {
		return ToolResult{}, err
	}
	return ToolResult{Value: map[string]any{"path": manifest.Path, "bytes": manifest.Size}, Artifacts: []ArtifactManifest{manifest}}, nil
}

type terminalExecTool struct {
	allowed map[string]bool
}

func (t terminalExecTool) Descriptor() ToolDescriptor {
	return ToolDescriptor{Name: "terminal.exec", Version: "1", Description: "Executar um binário explicitamente permitido no workspace", Risk: RiskWrite, RequiresApproval: true, Scopes: []string{"terminal:allowlisted"}}
}

func (t terminalExecTool) Execute(ctx context.Context, toolContext ToolContext, input map[string]any) (ToolResult, error) {
	executable := filepath.Base(stringInput(input, "executable", ""))
	if executable == "" || !t.allowed[executable] {
		return ToolResult{}, fmt.Errorf("terminal executable %q is not allowlisted", executable)
	}
	args := stringSliceInput(input, "args")
	for _, arg := range args {
		if strings.ContainsAny(arg, "\x00\r\n") {
			return ToolResult{}, errors.New("terminal argument contains a control character")
		}
	}
	if executable == "git" && (len(args) == 0 || args[0] != "status") {
		return ToolResult{}, errors.New("only git status is allowlisted in the default terminal policy")
	}
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(deadline, executable, args...)
	command.Dir = toolContext.Workspace
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + toolContext.Workspace, "PWD=" + toolContext.Workspace}
	var stdout, stderr bytes.Buffer
	command.Stdout = &limitedBuffer{Buffer: &stdout, Limit: 64 << 10}
	command.Stderr = &limitedBuffer{Buffer: &stderr, Limit: 64 << 10}
	if err := command.Run(); err != nil {
		return ToolResult{Value: map[string]any{"stdout": stdout.String(), "stderr": stderr.String()}}, err
	}
	return ToolResult{Value: map[string]any{"stdout": stdout.String(), "stderr": stderr.String(), "exit_code": 0}}, nil
}

type limitedBuffer struct {
	Buffer *bytes.Buffer
	Limit  int
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	if b.Buffer.Len()+len(data) > b.Limit {
		return 0, errors.New("tool output limit exceeded")
	}
	return b.Buffer.Write(data)
}

func safeWorkspacePath(workspace, relative string) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", errors.New("workspace is required")
	}
	if filepath.IsAbs(relative) {
		return "", errors.New("tool path must be relative to workspace")
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	candidate, err := filepath.Abs(filepath.Join(root, relative))
	if err != nil {
		return "", err
	}
	if !isWithin(root, candidate) {
		return "", errors.New("tool path escapes workspace")
	}
	return candidate, nil
}

func stringInput(input map[string]any, key, fallback string) string {
	value, ok := input[key].(string)
	if !ok {
		return fallback
	}
	return value
}

func intInput(input map[string]any, key string, fallback int) int {
	value, ok := input[key]
	if !ok {
		return fallback
	}
	switch number := value.(type) {
	case float64:
		return int(number)
	case int:
		return number
	case string:
		parsed, err := strconv.Atoi(number)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func stringSliceInput(input map[string]any, key string) []string {
	value, ok := input[key]
	if !ok {
		return nil
	}
	var result []string
	switch items := value.(type) {
	case []string:
		return append([]string(nil), items...)
	case []any:
		for _, item := range items {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
	}
	return result
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type sandboxExecTool struct{}

func (sandboxExecTool) Descriptor() ToolDescriptor {
	return ToolDescriptor{Name: "sandbox.exec", Version: "1", Description: "Executar Python ou Node em user namespace isolado e sem rede", Risk: RiskWrite, RequiresApproval: true, Scopes: []string{"sandbox:execute"}}
}

func (sandboxExecTool) Execute(ctx context.Context, toolContext ToolContext, input map[string]any) (ToolResult, error) {
	language := strings.ToLower(strings.TrimSpace(stringInput(input, "language", "")))
	interpreter := map[string]string{"python": "/usr/bin/python3", "python3": "/usr/bin/python3", "node": "/usr/bin/node"}[language]
	if interpreter == "" {
		return ToolResult{}, errors.New("sandbox language must be python or node")
	}
	code := stringInput(input, "code", "")
	if strings.TrimSpace(code) == "" {
		return ToolResult{}, errors.New("sandbox code is required")
	}
	if len(code) > 512<<10 {
		return ToolResult{}, errors.New("sandbox code limit exceeded")
	}
	if _, err := os.Stat(interpreter); err != nil {
		return ToolResult{}, fmt.Errorf("sandbox interpreter unavailable: %w", err)
	}
	sandboxDir := filepath.Join(toolContext.Workspace, ".agent-sandbox")
	if err := os.MkdirAll(sandboxDir, 0o700); err != nil {
		return ToolResult{}, err
	}
	extension := ".py"
	if language == "node" {
		extension = ".js"
	}
	codePath := filepath.Join(sandboxDir, toolContext.StepID+extension)
	if err := os.WriteFile(codePath, []byte(code), 0o600); err != nil {
		return ToolResult{}, err
	}
	defer os.Remove(codePath)
	deadline, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	mountScript := `set -eu
mount --make-rprivate /
mount -t tmpfs tmpfs /home
mkdir -p /home/workspace /tmp
mount --bind "$1" /home/workspace
mount -t tmpfs tmpfs /tmp
cd /home/workspace
exec "$2" "$3"`
	command := exec.CommandContext(deadline, "unshare", "--user", "--map-root-user", "--mount", "--pid", "--fork", "--mount-proc", "--net", "/bin/sh", "-c", mountScript, "sandbox", toolContext.Workspace, interpreter, "/home/workspace/.agent-sandbox/"+filepath.Base(codePath))
	command.Dir = toolContext.Workspace
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=/home/workspace", "PWD=/home/workspace"}
	var stdout, stderr bytes.Buffer
	command.Stdout = &limitedBuffer{Buffer: &stdout, Limit: 128 << 10}
	command.Stderr = &limitedBuffer{Buffer: &stderr, Limit: 128 << 10}
	if err := command.Run(); err != nil {
		return ToolResult{Value: map[string]any{"stdout": stdout.String(), "stderr": stderr.String()}}, err
	}
	return ToolResult{Value: map[string]any{"stdout": stdout.String(), "stderr": stderr.String(), "exit_code": 0}}, nil
}
