package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

type ConnectorConfig struct {
	ID             string               `json:"id"`
	Provider       string               `json:"provider"`
	BaseURL        string               `json:"base_url"`
	TokenEnv       string               `json:"token_env,omitempty"`
	OAuthProvider  string               `json:"oauth_provider,omitempty"`
	AllowedOrigins []string             `json:"allowed_origins,omitempty"`
	Operations     []ConnectorOperation `json:"operations"`
	TimeoutSeconds int                  `json:"timeout_seconds,omitempty"`
}

type ConnectorOperation struct {
	Name         string   `json:"name"`
	Methods      []string `json:"methods"`
	PathPrefixes []string `json:"path_prefixes"`
}

type ConnectorManager struct {
	mu         sync.RWMutex
	connectors map[string]ConnectorConfig
	client     *http.Client
	auth       *AuthStore
}

func NewConnectorManager() *ConnectorManager {
	return &ConnectorManager{connectors: make(map[string]ConnectorConfig), client: &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("connector redirects are disabled") }}}
}

func (m *ConnectorManager) SetOAuthStore(store *AuthStore) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.auth = store
	m.mu.Unlock()
}

func (m *ConnectorManager) Register(config ConnectorConfig) error {
	config.ID = strings.TrimSpace(config.ID)
	config.Provider = strings.TrimSpace(config.Provider)
	if config.ID == "" || config.Provider == "" {
		return errors.New("connector id and provider are required")
	}
	base, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil {
		return errors.New("connector base_url must be an https URL without userinfo")
	}
	if config.TimeoutSeconds <= 0 || config.TimeoutSeconds > 120 {
		config.TimeoutSeconds = 30
	}
	if config.TokenEnv != "" && !validEnvName(config.TokenEnv) {
		return errors.New("invalid connector token_env")
	}
	for i := range config.Operations {
		operation := &config.Operations[i]
		operation.Name = strings.TrimSpace(operation.Name)
		if operation.Name == "" || len(operation.Methods) == 0 || len(operation.PathPrefixes) == 0 {
			return errors.New("connector operations require name, methods and path_prefixes")
		}
		for j := range operation.Methods {
			operation.Methods[j] = strings.ToUpper(strings.TrimSpace(operation.Methods[j]))
		}
		for j := range operation.PathPrefixes {
			if !validConnectorPath(operation.PathPrefixes[j]) {
				return fmt.Errorf("invalid connector path prefix %q", operation.PathPrefixes[j])
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connectors[config.ID] = config
	return nil
}

func (m *ConnectorManager) List() []ConnectorConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]ConnectorConfig, 0, len(m.connectors))
	for _, connector := range m.connectors {
		copy := connector
		copy.TokenEnv = ""
		result = append(result, copy)
	}
	return result
}

func (m *ConnectorManager) Call(ctx context.Context, connectorID, operationName, method, requestPath string, body []byte) (int, string, error) {
	return m.call(ctx, connectorID, operationName, method, requestPath, body, "")
}

func (m *ConnectorManager) CallForOrganization(ctx context.Context, organizationID, connectorID, operationName, method, requestPath string, body []byte) (int, string, error) {
	m.mu.RLock()
	config, ok := m.connectors[strings.TrimSpace(connectorID)]
	auth := m.auth
	m.mu.RUnlock()
	if !ok {
		return 0, "", fmt.Errorf("connector %q is not registered", connectorID)
	}
	token := ""
	if auth != nil && strings.TrimSpace(config.OAuthProvider) != "" {
		var err error
		token, _, err = auth.OAuthAccessTokenForOrganization(organizationID, config.OAuthProvider)
		if err != nil {
			return 0, "", fmt.Errorf("resolve OAuth credential for connector %q: %w", connectorID, err)
		}
	}
	return m.call(ctx, connectorID, operationName, method, requestPath, body, token)
}

func (m *ConnectorManager) call(ctx context.Context, connectorID, operationName, method, requestPath string, body []byte, tokenOverride string) (int, string, error) {
	m.mu.RLock()
	config, ok := m.connectors[strings.TrimSpace(connectorID)]
	m.mu.RUnlock()
	if !ok {
		return 0, "", fmt.Errorf("connector %q is not registered", connectorID)
	}
	operation, allowed := findConnectorOperation(config.Operations, operationName, method, requestPath)
	if !allowed {
		return 0, "", fmt.Errorf("connector operation %q is not allowlisted", operationName)
	}
	_ = operation
	base, _ := url.Parse(config.BaseURL)
	relative, err := url.Parse(requestPath)
	if err != nil || relative.IsAbs() || !validConnectorPath(relative.Path) {
		return 0, "", errors.New("invalid connector request path")
	}
	base.Path = path.Join(strings.TrimSuffix(base.Path, "/"), relative.Path)
	base.RawQuery = relative.RawQuery
	request, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), base.String(), bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	request.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	token := tokenOverride
	if token == "" && config.TokenEnv != "" {
		token = os.Getenv(config.TokenEnv)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := *m.client
	client.Timeout = time.Duration(config.TimeoutSeconds) * time.Second
	response, err := client.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return response.StatusCode, "", err
	}
	return response.StatusCode, string(data), nil
}

func findConnectorOperation(operations []ConnectorOperation, name, method, requestPath string) (ConnectorOperation, bool) {
	method = strings.ToUpper(strings.TrimSpace(method))
	for _, operation := range operations {
		if operation.Name != name {
			continue
		}
		methodOK := false
		for _, allowedMethod := range operation.Methods {
			if strings.ToUpper(allowedMethod) == method {
				methodOK = true
				break
			}
		}
		if !methodOK {
			return operation, false
		}
		for _, prefix := range operation.PathPrefixes {
			if strings.HasPrefix(requestPath, prefix) {
				return operation, true
			}
		}
	}
	return ConnectorOperation{}, false
}

func validConnectorPath(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.Contains(value, "..") && !strings.ContainsAny(value, "\x00\r\n")
}

func validEnvName(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		if !(char == '_' || char >= 'A' && char <= 'Z' || index > 0 && char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}

type connectorTool struct{ manager *ConnectorManager }

func (t connectorTool) Descriptor() ToolDescriptor {
	return ToolDescriptor{Name: "connector.http", Version: "1", Description: "Chamar operação allowlisted de um conector externo", Risk: RiskExternalSideEffect, RequiresApproval: true, Scopes: []string{"connector:external"}}
}

func (t connectorTool) Execute(ctx context.Context, toolContext ToolContext, input map[string]any) (ToolResult, error) {
	if t.manager == nil {
		return ToolResult{}, errors.New("connector manager is unavailable")
	}
	body := []byte(stringInput(input, "body", ""))
	if len(body) > 1<<20 {
		return ToolResult{}, errors.New("connector body limit exceeded")
	}
	status, response, err := t.manager.CallForOrganization(ctx, toolContext.OrganizationID, stringInput(input, "connector_id", ""), stringInput(input, "operation", ""), stringInput(input, "method", "GET"), stringInput(input, "path", "/"), body)
	if err != nil {
		return ToolResult{}, err
	}
	var value any
	if json.Unmarshal([]byte(response), &value) != nil {
		value = response
	}
	return ToolResult{Value: map[string]any{"status": status, "response": value}}, nil
}
