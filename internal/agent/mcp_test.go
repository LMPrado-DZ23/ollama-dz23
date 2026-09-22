package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestMCPManagerCallsAllowlistedMethod(t *testing.T) {
	t.Setenv("GO_WANT_MCP_HELPER_PROCESS", "1")
	manager := NewMCPManager()
	if err := manager.Register(MCPServerConfig{ID: "echo", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess"}, AllowedMethods: []string{"echo"}, EnvironmentVars: []string{"GO_WANT_MCP_HELPER_PROCESS"}, TimeoutSeconds: 5}); err != nil {
		t.Fatal(err)
	}
	defer manager.StopAll()
	result, err := manager.Call(context.Background(), "echo", "echo", map[string]any{"value": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil || payload["value"] != "hello" {
		t.Fatalf("result=%s err=%v", result, err)
	}
	if _, err := manager.Call(context.Background(), "echo", "tools/list", nil); err == nil || !strings.Contains(err.Error(), "not allowlisted") {
		t.Fatalf("unexpected allowlist result: %v", err)
	}
}

func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER_PROCESS") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			fmt.Printf("{\"jsonrpc\":\"2.0\",\"id\":0,\"error\":{\"message\":%q}}\n", err.Error())
			continue
		}
		value := request.Params["value"]
		fmt.Printf("{\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"value\":%q}}\n", request.ID, value)
	}
	os.Exit(0)
}
