// Package multillm provides the optional DZ23 multi-provider registry.
// It is disabled unless OLLAMA_DZ23_CONFIG points at a configuration file.
package multillm

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Config struct {
	Providers        []Provider `json:"providers"`
	GatewayAPIKeyEnv string     `json:"gateway_api_key_env,omitempty"`
}

type Provider struct {
	Name           string        `json:"name"`
	Type           string        `json:"type"`
	BaseURL        string        `json:"base_url"`
	APIKeyEnv      string        `json:"api_key_env,omitempty"`
	Models         []ModelConfig `json:"models"`
	Priority       int           `json:"priority,omitempty"`
	Enabled        *bool         `json:"enabled,omitempty"`
	Executable     string        `json:"executable,omitempty"`
	Args           []string      `json:"args,omitempty"`
	AllowExecution bool          `json:"allow_execution,omitempty"`
	TimeoutSeconds int           `json:"timeout_seconds,omitempty"`
	Paths          []string      `json:"paths,omitempty"`
	AuthStyle      string        `json:"auth_style,omitempty"`
	AllowPrivate   bool          `json:"allow_private,omitempty"`
}

const (
	ProviderTypeOpenAICompatible = "openai-compatible"
	ProviderTypeAnthropic        = "anthropic"
	ProviderTypeCLI              = "cli"
	AuthStyleBearer              = "bearer"
	AuthStyleAPIKey              = "x-api-key"
	AuthStyleAnthropic           = "anthropic"
	AuthStyleGoogleQuery         = "google-query"
)

type ModelConfig struct {
	ID           string   `json:"id"`
	Capabilities []string `json:"capabilities,omitempty"`
	Priority     int      `json:"priority,omitempty"`
}

type Model struct {
	ID           string   `json:"id"`
	UpstreamID   string   `json:"upstream_id"`
	Provider     string   `json:"provider"`
	Capabilities []string `json:"capabilities,omitempty"`
	Available    bool     `json:"available"`
	Priority     int      `json:"priority,omitempty"`
}

type Policy struct {
	LocalOnly bool
	Required  []string
	Path      string
}

type Registry struct {
	providers        map[string]Provider
	models           map[string]Model
	gatewayAPIKeyEnv string
}

func Load(path string) (*Registry, error) {
	if strings.TrimSpace(path) == "" {
		return &Registry{providers: map[string]Provider{}, models: map[string]Model{}}, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read DZ23 provider config: %w", err)
	}
	var cfg Config
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode DZ23 provider config: %w", err)
	}
	r := &Registry{providers: make(map[string]Provider), models: make(map[string]Model), gatewayAPIKeyEnv: cfg.GatewayAPIKeyEnv}
	for _, p := range cfg.Providers {
		if err := validateProvider(p); err != nil {
			return nil, err
		}
		if _, exists := r.providers[p.Name]; exists {
			return nil, fmt.Errorf("duplicate provider %q", p.Name)
		}
		r.providers[p.Name] = p
		enabled := p.Enabled == nil || *p.Enabled
		available := enabled && (p.APIKeyEnv == "" || credentialValue(p.APIKeyEnv) != "")
		for _, item := range p.Models {
			id := p.Name + "/" + item.ID
			if _, exists := r.models[id]; exists {
				return nil, fmt.Errorf("duplicate model %q", id)
			}
			r.models[id] = Model{ID: id, UpstreamID: item.ID, Provider: p.Name, Capabilities: append([]string(nil), item.Capabilities...), Available: available, Priority: p.Priority + item.Priority}
		}
	}
	return r, nil
}

func (r *Registry) Authorize(request *http.Request) bool {
	if r.gatewayAPIKeyEnv != "" {
		expected := credentialValue(r.gatewayAPIKeyEnv)
		provided := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
		return expected != "" && subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

func validateProvider(p Provider) error {
	if p.Name == "" || p.Name != strings.TrimSpace(p.Name) || strings.ContainsAny(p.Name, "/: \\") {
		return errors.New("provider name must be non-empty and may not contain separators")
	}
	switch p.Type {
	case ProviderTypeOpenAICompatible, ProviderTypeAnthropic, ProviderTypeCLI:
	default:
		return fmt.Errorf("provider %q has unsupported type %q", p.Name, p.Type)
	}
	if p.Type == ProviderTypeCLI {
		if strings.TrimSpace(p.Executable) == "" {
			return fmt.Errorf("CLI provider %q requires executable", p.Name)
		}
		if !filepath.IsAbs(p.Executable) {
			return fmt.Errorf("CLI provider %q requires an absolute executable path", p.Name)
		}
		if !p.AllowExecution {
			return fmt.Errorf("CLI provider %q requires allow_execution=true", p.Name)
		}
		for _, arg := range p.Args {
			if strings.ContainsAny(arg, "\r\n\x00") {
				return fmt.Errorf("CLI provider %q has an invalid argument", p.Name)
			}
		}
		for _, path := range p.Paths {
			if path == "/v1/embeddings" || path == "/v1/messages" {
				return fmt.Errorf("CLI provider %q cannot implement protocol path %q", p.Name, path)
			}
		}
	} else {
		u, err := url.Parse(p.BaseURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return fmt.Errorf("provider %q requires an HTTPS base_url without userinfo", p.Name)
		}
		if ip := net.ParseIP(u.Hostname()); ip != nil && !p.AllowPrivate && unsafeProviderIP(ip) {
			return fmt.Errorf("provider %q targets a private address; set allow_private explicitly for trusted local services", p.Name)
		}
	}
	for _, path := range p.Paths {
		if !supportedPath(path) {
			return fmt.Errorf("provider %q has unsupported path %q", p.Name, path)
		}
	}
	for _, m := range p.Models {
		if strings.TrimSpace(m.ID) == "" || strings.ContainsAny(m.ID, "\r\n") {
			return fmt.Errorf("provider %q has invalid model id", p.Name)
		}
	}
	return nil
}

func (p Provider) SupportsPath(path string) bool {
	if len(p.Paths) == 0 {
		if p.Type == ProviderTypeAnthropic {
			return path == "/v1/messages" || path == "/v1/chat/completions" || path == "/api/chat" || path == "/api/generate"
		}
		return path == "/v1/chat/completions" || path == "/api/chat" || path == "/api/generate"
	}
	for _, allowed := range p.Paths {
		if allowed == path {
			return true
		}
	}
	return false
}

func (r *Registry) Models() []Model {
	models := make([]Model, 0, len(r.models))
	for _, m := range r.models {
		m.Available = r.modelAvailable(m)
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

func (r *Registry) Provider(name string) (Provider, bool) {
	p, ok := r.providers[name]
	return p, ok
}

func (r *Registry) Model(name string) (Model, bool) {
	m, ok := r.models[name]
	if ok {
		m.Available = r.modelAvailable(m)
	}
	return m, ok
}

func (r *Registry) Resolve(name string, policy Policy) (Model, bool) {
	if policy.LocalOnly || name == "local/private" {
		return Model{}, false
	}
	if m, ok := r.Model(name); ok && m.Available && supports(m, policy.Required) && r.supportsPath(m, policy.Path) {
		return m, true
	}
	if !strings.HasPrefix(name, "auto/") && name != "auto" {
		return Model{}, false
	}
	if len(policy.Required) == 0 {
		switch name {
		case "auto/coding":
			policy.Required = []string{"coding"}
		case "auto/reasoning":
			policy.Required = []string{"reasoning"}
		case "auto/vision":
			policy.Required = []string{"vision"}
		}
	}
	var candidates []Model
	for _, m := range r.models {
		m.Available = r.modelAvailable(m)
		if m.Available && supports(m, policy.Required) && r.supportsPath(m, policy.Path) {
			candidates = append(candidates, m)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Priority == candidates[j].Priority {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].Priority > candidates[j].Priority
	})
	if len(candidates) == 0 {
		return Model{}, false
	}
	return candidates[0], true
}

func (r *Registry) modelAvailable(m Model) bool {
	p, ok := r.providers[m.Provider]
	if !ok || (p.Enabled != nil && !*p.Enabled) {
		return false
	}
	return p.APIKeyEnv == "" || credentialValue(p.APIKeyEnv) != ""
}

func unsafeProviderIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

func (r *Registry) supportsPath(m Model, path string) bool {
	if path == "" {
		return true
	}
	provider, ok := r.providers[m.Provider]
	return ok && provider.SupportsPath(path)
}

func supports(m Model, required []string) bool {
	for _, want := range required {
		found := false
		for _, have := range m.Capabilities {
			if have == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (r *Registry) SafeSnapshot() string {
	type safeProvider struct {
		Name      string `json:"name"`
		Type      string `json:"type"`
		Available bool   `json:"available"`
	}
	providers := make([]safeProvider, 0, len(r.providers))
	for _, p := range r.providers {
		providers = append(providers, safeProvider{Name: p.Name, Type: p.Type, Available: p.APIKeyEnv == "" || credentialValue(p.APIKeyEnv) != ""})
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Name < providers[j].Name })
	b, _ := json.Marshal(struct {
		Providers []safeProvider `json:"providers"`
		Models    []Model        `json:"models"`
	}{providers, r.Models()})
	return string(b)
}
