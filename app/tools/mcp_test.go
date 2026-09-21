//go:build windows || darwin

package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A nil session makes accidental execution fail immediately in these tests.
func TestMCPDeniedNeverExecutes(t *testing.T) {
	called := false
	tool := &mcpTool{spec: &mcp.Tool{Name: "read_file"}, budget: &mcpBudget{}, approve: func(name, args string) bool {
		called = true
		if name != "read_file" || !strings.Contains(args, "example.txt") {
			t.Fatal("approval did not show the operation")
		}
		return false
	}}
	_, _, err := tool.Execute(context.Background(), map[string]any{"path": "example.txt"})
	if err == nil || !called {
		t.Fatal("denied operation was not blocked")
	}
}

func TestMCPCanceledNeverPrompts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tool := &mcpTool{spec: &mcp.Tool{Name: "read_file"}, budget: &mcpBudget{}, approve: func(string, string) bool { t.Fatal("canceled operation prompted"); return true }}
	if _, _, err := tool.Execute(ctx, nil); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestMCPBudgetNeverPrompts(t *testing.T) {
	tool := &mcpTool{spec: &mcp.Tool{Name: "read_file"}, budget: &mcpBudget{calls: 24}, approve: func(string, string) bool { t.Fatal("over-budget operation prompted"); return true }}
	if _, _, err := tool.Execute(context.Background(), nil); err == nil {
		t.Fatal("budget ignored")
	}
}

func TestMCPMissingApprovalFailsClosed(t *testing.T) {
	tool := &mcpTool{spec: &mcp.Tool{Name: "read_file"}, budget: &mcpBudget{}}
	if _, _, err := tool.Execute(context.Background(), nil); err == nil {
		t.Fatal("missing approval allowed execution")
	}
}
