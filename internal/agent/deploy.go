package agent

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type DeployConfig struct {
	ID             string `json:"id"`
	Provider       string `json:"provider"`
	BaseURL        string `json:"base_url"`
	TokenEnv       string `json:"token_env,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
	AccountID      string `json:"account_id,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type DeploymentRequest struct {
	Name   string
	Root   string
	Target string
}

type DeploymentResult struct {
	Provider     string `json:"provider"`
	DeploymentID string `json:"deployment_id,omitempty"`
	URL          string `json:"url,omitempty"`
	Status       string `json:"status"`
	Files        int    `json:"files"`
}

type DeploymentManager struct {
	configs map[string]DeployConfig
	client  *http.Client
}

func NewDeploymentManager() *DeploymentManager {
	return &DeploymentManager{configs: map[string]DeployConfig{}, client: &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("deployment redirects are disabled") }}}
}

func (m *DeploymentManager) Register(config DeployConfig) error {
	config.ID = strings.TrimSpace(config.ID)
	config.Provider = strings.ToLower(strings.TrimSpace(config.Provider))
	if config.ID == "" || config.Provider == "" {
		return errors.New("deployment id and provider are required")
	}
	if config.Provider != "vercel" && config.Provider != "netlify" && config.Provider != "generic" {
		return fmt.Errorf("unsupported deployment provider %q", config.Provider)
	}
	base, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || base == nil || base.Host == "" || base.User != nil {
		return errors.New("deployment base_url must be HTTPS or loopback HTTP without userinfo")
	}
	loopbackHTTP := base.Scheme == "http" && (base.Hostname() == "127.0.0.1" || base.Hostname() == "::1")
	if base.Scheme != "https" && !loopbackHTTP {
		return errors.New("deployment base_url must be HTTPS or loopback HTTP without userinfo")
	}
	if config.TokenEnv != "" && !validEnvName(config.TokenEnv) {
		return errors.New("invalid deployment token_env")
	}
	if config.TimeoutSeconds <= 0 || config.TimeoutSeconds > 600 {
		config.TimeoutSeconds = 120
	}
	m.configs[config.ID] = config
	return nil
}

func (m *DeploymentManager) List() []DeployConfig {
	result := make([]DeployConfig, 0, len(m.configs))
	for _, config := range m.configs {
		config.TokenEnv = ""
		result = append(result, config)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (m *DeploymentManager) Deploy(ctx context.Context, providerID string, request DeploymentRequest) (DeploymentResult, error) {
	if m == nil {
		return DeploymentResult{}, errors.New("deployment manager is unavailable")
	}
	config, ok := m.configs[strings.TrimSpace(providerID)]
	if !ok {
		return DeploymentResult{}, fmt.Errorf("deployment provider %q is not registered", providerID)
	}
	files, err := collectDeployFiles(request.Root)
	if err != nil {
		return DeploymentResult{}, err
	}
	if len(files) == 0 {
		return DeploymentResult{}, errors.New("deployment workspace has no files")
	}
	var result DeploymentResult
	switch config.Provider {
	case "vercel":
		result, err = m.deployVercel(ctx, config, request, files)
	case "netlify":
		result, err = m.deployNetlify(ctx, config, request, files)
	default:
		result, err = m.deployGeneric(ctx, config, request, files)
	}
	if err != nil {
		return DeploymentResult{}, err
	}
	result.Provider = config.Provider
	result.Files = len(files)
	return result, nil
}

type deployFile struct {
	Path string
	Data []byte
}

func collectDeployFiles(root string) ([]deployFile, error) {
	root, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil || root == "." || root == string(filepath.Separator) {
		return nil, errors.New("invalid deployment root")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, errors.New("deployment root is not a directory")
	}
	var files []deployFile
	var total int64
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if len(files) >= 2000 || total+info.Size() > 50<<20 {
			return errors.New("deployment workspace exceeds file or size limit")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("deployment path escaped root")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, deployFile{Path: filepath.ToSlash(relative), Data: data})
		total += int64(len(data))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func (m *DeploymentManager) request(ctx context.Context, config DeployConfig, method, endpoint string, body []byte, contentType string) (map[string]any, error) {
	base, err := url.Parse(config.BaseURL)
	if err != nil {
		return nil, err
	}
	relative, err := url.Parse(endpoint)
	if err != nil || relative.IsAbs() || !validConnectorPath(relative.Path) {
		return nil, errors.New("invalid deployment endpoint")
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + relative.Path
	base.RawQuery = relative.RawQuery
	req, err := http.NewRequestWithContext(ctx, method, base.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if config.TokenEnv != "" {
		if token := os.Getenv(config.TokenEnv); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	client := *m.client
	client.Timeout = time.Duration(config.TimeoutSeconds) * time.Second
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("deployment provider returned status %d: %s", resp.StatusCode, limitError(string(data), 800))
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return map[string]any{"raw": string(data)}, nil
	}
	return payload, nil
}

func (m *DeploymentManager) deployGeneric(ctx context.Context, config DeployConfig, request DeploymentRequest, files []deployFile) (DeploymentResult, error) {
	payload := map[string]any{"name": request.Name, "target": request.Target, "files": encodeFiles(files)}
	body, _ := json.Marshal(payload)
	response, err := m.request(ctx, config, http.MethodPost, "/deploy", body, "application/json")
	if err != nil {
		return DeploymentResult{}, err
	}
	return resultFromPayload(response), nil
}

func (m *DeploymentManager) deployVercel(ctx context.Context, config DeployConfig, request DeploymentRequest, files []deployFile) (DeploymentResult, error) {
	payload := map[string]any{"name": request.Name, "target": request.Target, "files": encodeFiles(files)}
	if config.ProjectID != "" {
		payload["project"] = config.ProjectID
	}
	body, _ := json.Marshal(payload)
	endpoint := "/v13/deployments"
	if config.AccountID != "" {
		endpoint += "?teamId=" + url.QueryEscape(config.AccountID)
	}
	response, err := m.request(ctx, config, http.MethodPost, endpoint, body, "application/json")
	if err != nil {
		return DeploymentResult{}, err
	}
	return resultFromPayload(response), nil
}

func (m *DeploymentManager) deployNetlify(ctx context.Context, config DeployConfig, request DeploymentRequest, files []deployFile) (DeploymentResult, error) {
	siteID := config.ProjectID
	var site map[string]any
	var err error
	if siteID == "" {
		createPayload, _ := json.Marshal(map[string]any{"name": request.Name})
		site, err = m.request(ctx, config, http.MethodPost, "/api/v1/sites", createPayload, "application/json")
		if err != nil {
			return DeploymentResult{}, err
		}
		siteID = firstString(site, "id", "site_id")
	}
	if siteID == "" {
		return DeploymentResult{}, errors.New("netlify response has no site id")
	}
	digests := map[string]string{}
	for _, file := range files {
		digest := sha1.Sum(file.Data)
		digests["/"+file.Path] = hex.EncodeToString(digest[:])
	}
	deployPayload, _ := json.Marshal(map[string]any{"files": digests})
	deploy, err := m.request(ctx, config, http.MethodPost, "/api/v1/sites/"+url.PathEscape(siteID)+"/deploys", deployPayload, "application/json")
	if err != nil {
		return DeploymentResult{}, err
	}
	deployID := firstString(deploy, "id", "deploy_id")
	if deployID == "" {
		return DeploymentResult{}, errors.New("netlify response has no deployment id")
	}
	for _, file := range files {
		endpoint := "/api/v1/deploys/" + url.PathEscape(deployID) + "/files/" + url.PathEscape(file.Path)
		if _, err := m.request(ctx, config, http.MethodPut, endpoint, file.Data, "application/octet-stream"); err != nil {
			return DeploymentResult{}, err
		}
	}
	return resultFromPayload(deploy), nil
}

func encodeFiles(files []deployFile) []map[string]string {
	encoded := make([]map[string]string, 0, len(files))
	for _, file := range files {
		encoded = append(encoded, map[string]string{"file": file.Path, "data": base64.StdEncoding.EncodeToString(file.Data)})
	}
	return encoded
}

func resultFromPayload(payload map[string]any) DeploymentResult {
	return DeploymentResult{DeploymentID: firstString(payload, "id", "deployment_id", "deploy_id"), URL: firstString(payload, "url", "deploy_url", "ssl_url"), Status: firstString(payload, "status", "state")}
}

func firstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
