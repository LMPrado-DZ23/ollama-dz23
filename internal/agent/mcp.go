package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type MCPServerConfig struct {
	ID              string   `json:"id"`
	Command         string   `json:"command"`
	Args            []string `json:"args,omitempty"`
	AllowedMethods  []string `json:"allowed_methods,omitempty"`
	EnvironmentVars []string `json:"environment_vars,omitempty"`
	TimeoutSeconds  int      `json:"timeout_seconds,omitempty"`
}

type MCPManager struct {
	mu      sync.RWMutex
	servers map[string]*MCPServer
}

func NewMCPManager() *MCPManager {
	return &MCPManager{servers: make(map[string]*MCPServer)}
}

func (m *MCPManager) Register(config MCPServerConfig) error {
	if strings.TrimSpace(config.ID) == "" || strings.TrimSpace(config.Command) == "" {
		return errors.New("MCP server id and command are required")
	}
	if config.TimeoutSeconds <= 0 || config.TimeoutSeconds > 300 {
		config.TimeoutSeconds = 30
	}
	command, err := exec.LookPath(config.Command)
	if err != nil {
		return fmt.Errorf("MCP command unavailable: %w", err)
	}
	config.Command = command
	allowed := make(map[string]bool, len(config.AllowedMethods))
	for _, method := range config.AllowedMethods {
		allowed[strings.TrimSpace(method)] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old := m.servers[config.ID]; old != nil {
		_ = old.Stop()
	}
	m.servers[config.ID] = &MCPServer{config: config, allowedMethods: allowed}
	return nil
}

func (m *MCPManager) StopAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, server := range m.servers {
		_ = server.Stop()
	}
}

func (m *MCPManager) List() []MCPServerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]MCPServerConfig, 0, len(m.servers))
	for _, server := range m.servers {
		config := server.config
		config.EnvironmentVars = append([]string(nil), config.EnvironmentVars...)
		result = append(result, config)
	}
	return result
}

func (m *MCPManager) Call(ctx context.Context, serverID, method string, params any) (json.RawMessage, error) {
	m.mu.RLock()
	server := m.servers[serverID]
	m.mu.RUnlock()
	if server == nil {
		return nil, fmt.Errorf("MCP server %q is not registered", serverID)
	}
	return server.Call(ctx, method, params)
}

type MCPServer struct {
	mu             sync.Mutex
	config         MCPServerConfig
	allowedMethods map[string]bool
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	stdout         *bufio.Reader
	cancel         context.CancelFunc
	nextID         int64
}

func (s *MCPServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startLocked()
}

func (s *MCPServer) startLocked() error {
	if s.cmd != nil {
		return nil
	}
	processContext, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(processContext, s.config.Command, s.config.Args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/tmp"}
	for _, name := range s.config.EnvironmentVars {
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, "=\x00\r\n") {
			cancel()
			return fmt.Errorf("invalid MCP environment variable %q", name)
		}
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		cancel()
		return err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		cancel()
		return err
	}
	s.cmd = cmd
	s.stdin = stdin
	s.stdout = bufio.NewReaderSize(stdout, 1<<20)
	s.cancel = cancel
	return nil
}

func (s *MCPServer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopLocked()
}

func (s *MCPServer) stopLocked() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	var err error
	if s.cmd != nil {
		err = s.cmd.Wait()
	}
	s.cmd, s.stdin, s.stdout, s.cancel = nil, nil, nil, nil
	return err
}

func (s *MCPServer) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	method = strings.TrimSpace(method)
	if method == "" {
		return nil, errors.New("MCP method is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.allowedMethods) > 0 && !s.allowedMethods[method] {
		return nil, fmt.Errorf("MCP method %q is not allowlisted", method)
	}
	if err := s.startLocked(); err != nil {
		return nil, err
	}
	s.nextID++
	requestID := s.nextID
	request := map[string]any{"jsonrpc": "2.0", "id": requestID, "method": method, "params": params}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if _, err := s.stdin.Write(append(data, '\n')); err != nil {
		_ = s.stopLocked()
		return nil, err
	}
	resultChannel := make(chan mcpResponse, 1)
	go func() {
		line, err := s.stdout.ReadBytes('\n')
		if err != nil {
			resultChannel <- mcpResponse{err: err}
			return
		}
		var response mcpResponse
		if err := json.Unmarshal(line, &response); err != nil {
			resultChannel <- mcpResponse{err: err}
			return
		}
		resultChannel <- response
	}()
	timeout := time.Duration(s.config.TimeoutSeconds) * time.Second
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < timeout {
		timeout = time.Until(deadline)
	}
	select {
	case <-ctx.Done():
		_ = s.stopLocked()
		return nil, ctx.Err()
	case <-time.After(timeout):
		_ = s.stopLocked()
		return nil, errors.New("MCP call timed out")
	case response := <-resultChannel:
		if response.err != nil {
			_ = s.stopLocked()
			return nil, response.err
		}
		if response.Error != nil {
			return nil, fmt.Errorf("MCP error: %s", response.Error.Message)
		}
		return response.Result, nil
	}
}

type mcpResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *mcpError       `json:"error,omitempty"`
	err    error
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpCallTool struct{ manager *MCPManager }

func (t mcpCallTool) Descriptor() ToolDescriptor {
	return ToolDescriptor{Name: "mcp.call", Version: "1", Description: "Chamar método allowlisted de servidor MCP stdio", Risk: RiskExternalSideEffect, RequiresApproval: true, Scopes: []string{"mcp:call"}}
}

func (t mcpCallTool) Execute(ctx context.Context, _ ToolContext, input map[string]any) (ToolResult, error) {
	if t.manager == nil {
		return ToolResult{}, errors.New("MCP manager is unavailable")
	}
	result, err := t.manager.Call(ctx, stringInput(input, "server_id", ""), stringInput(input, "method", ""), input["params"])
	if err != nil {
		return ToolResult{}, err
	}
	var value any
	if err := json.Unmarshal(result, &value); err != nil {
		return ToolResult{Value: string(result)}, nil
	}
	return ToolResult{Value: value}, nil
}
