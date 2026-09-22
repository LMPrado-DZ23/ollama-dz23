package agent

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed browser_helper.py
var browserHelper []byte

type browserOperatorTool struct{}

func (browserOperatorTool) Descriptor() ToolDescriptor {
	return ToolDescriptor{Name: "browser.operator", Version: "1", Description: "Operar um browser Playwright em sessão isolada", Risk: RiskExternalSideEffect, RequiresApproval: true, Scopes: []string{"browser:navigate", "browser:files", "browser:takeover"}}
}

func (browserOperatorTool) Execute(ctx context.Context, toolContext ToolContext, input map[string]any) (ToolResult, error) {
	action := strings.TrimSpace(stringInput(input, "action", ""))
	if action == "" {
		return ToolResult{}, errors.New("browser action is required")
	}
	if action == "navigate" {
		if strings.TrimSpace(stringInput(input, "url", "")) == "" {
			return ToolResult{}, errors.New("browser navigate requires url")
		}
	}
	for _, key := range []string{"path", "save_path"} {
		if value := stringInput(input, key, ""); value != "" {
			if _, err := safeWorkspacePath(toolContext.Workspace, value); err != nil {
				return ToolResult{}, fmt.Errorf("browser %s: %w", key, err)
			}
		}
	}
	request := cloneMap(input)
	request["session_id"] = toolContext.MissionID
	requestData, err := json.Marshal(request)
	if err != nil {
		return ToolResult{}, err
	}
	temp, err := os.CreateTemp("", "ollama-agent-browser-*.py")
	if err != nil {
		return ToolResult{}, err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o700); err != nil {
		_ = temp.Close()
		return ToolResult{}, err
	}
	if _, err := temp.Write(browserHelper); err != nil {
		_ = temp.Close()
		return ToolResult{}, err
	}
	if err := temp.Close(); err != nil {
		return ToolResult{}, err
	}
	deadline, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	command := exec.CommandContext(deadline, "/usr/bin/python3", tempName)
	command.Stdin = bytes.NewReader(requestData)
	command.Env = append(os.Environ(), "OLLAMA_AGENT_BROWSER_ROOT="+filepath.Join(toolContext.Workspace, ".browser"))
	var stdout, stderr bytes.Buffer
	command.Stdout = &limitedBuffer{Buffer: &stdout, Limit: 256 << 10}
	command.Stderr = &limitedBuffer{Buffer: &stderr, Limit: 64 << 10}
	if err := command.Run(); err != nil {
		if stdout.Len() > 0 {
			var failure map[string]any
			if json.Unmarshal(stdout.Bytes(), &failure) == nil {
				return ToolResult{Value: failure}, fmt.Errorf("browser operator: %v", failure["error"])
			}
		}
		return ToolResult{Value: map[string]any{"stderr": stderr.String()}}, err
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return ToolResult{}, fmt.Errorf("decode browser result: %w", err)
	}
	if result["error"] != nil {
		return ToolResult{Value: result}, fmt.Errorf("browser operator: %v", result["error"])
	}
	return ToolResult{Value: result}, nil
}
