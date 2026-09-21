package multillm

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
)

var credentialName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
var desktopCredentialMu sync.Mutex

// ConfigPath also finds the installer-owned configuration when Explorer or a
// terminal has an old environment from before installation. No config is created.
func ConfigPath() string {
	if p := strings.TrimSpace(os.Getenv("OLLAMA_DZ23_CONFIG")); p != "" {
		return p
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(root, "Ollama DZ23", "dz23-providers.json")
	if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
		return p
	}
	return ""
}

func managedCredentialPath(name string) (string, error) {
	if !credentialName.MatchString(name) {
		return "", errors.New("invalid credential name")
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "Ollama DZ23", "credentials", name+managedCredentialExtension), nil
}

// Accept only legacy files owned by the DZ23 configurator; arbitrary external
// credential files remain read-only in the desktop.
func editableCredentialPath(name string) (string, error) {
	path, err := managedCredentialPath(name)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(os.Getenv(name)) != "" {
		return "", errors.New("externally managed credential")
	}
	external := strings.TrimSpace(os.Getenv(name + "_FILE"))
	if external == "" {
		return path, nil
	}
	if runtime.GOOS == "windows" {
		root := os.Getenv("LOCALAPPDATA")
		legacy := filepath.Join(root, "Ollama DZ23", "secrets", name+".dpapi")
		if root != "" && strings.EqualFold(filepath.Clean(external), filepath.Clean(legacy)) {
			return legacy, nil
		}
	}
	if filepath.Clean(external) == filepath.Clean(path) {
		return path, nil
	}
	return "", errors.New("externally managed credential")
}

func credentialManagedExternally(name string) bool {
	_, err := editableCredentialPath(name)
	return err != nil
}

func saveManagedCredential(name, value string) error {
	path, err := editableCredentialPath(name)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	raw, err := encodeCredentialFile(value)
	if err != nil {
		return err
	}
	defer clear(raw)
	f, err := os.CreateTemp(filepath.Dir(path), ".credential-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

type DesktopProvider struct {
	Name              string   `json:"name"`
	BaseURL           string   `json:"base_url"`
	Models            []string `json:"models"`
	Configured        bool     `json:"configured"`
	Enabled           bool     `json:"enabled"`
	ManagedExternally bool     `json:"managed_externally"`
}

func desktopProviders(reg *Registry) []DesktopProvider {
	result := make([]DesktopProvider, 0)
	for _, p := range reg.providers {
		if p.APIKeyEnv == "" || p.Type == ProviderTypeCLI {
			continue
		}
		models := make([]string, 0, len(p.Models))
		for _, m := range p.Models {
			models = append(models, m.ID)
		}
		result = append(result, DesktopProvider{Name: p.Name, BaseURL: p.BaseURL, Models: models,
			Configured: credentialValue(p.APIKeyEnv) != "", Enabled: p.Enabled == nil || *p.Enabled,
			ManagedExternally: credentialManagedExternally(p.APIKeyEnv)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// DesktopCredentialsHandler is deliberately NOT registered on the inference
// listener. Only the authenticated desktop session can manage stored credentials.
func DesktopCredentialsHandler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		fail := func(status int, message string) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
		}
		host, _, err := net.SplitHostPort(req.RemoteAddr)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			fail(403, "Local desktop access required")
			return
		}
		cookie, err := req.Cookie("token")
		if token == "" || err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(token)) != 1 {
			fail(403, "Desktop authentication required")
			return
		}
		if origin := req.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != req.Host || (u.Scheme != "http" && u.Scheme != "https") {
				fail(403, "Cross-origin access denied")
				return
			}
		}
		if req.Header.Get("Sec-Fetch-Site") == "cross-site" {
			fail(403, "Cross-site access denied")
			return
		}
		desktopCredentialMu.Lock()
		defer desktopCredentialMu.Unlock()
		reg, err := Load(ConfigPath())
		if err != nil {
			fail(503, "Provider configuration could not be loaded")
			return
		}
		if req.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"providers": desktopProviders(reg)})
			return
		}
		if req.Method != http.MethodPut && req.Method != http.MethodDelete {
			fail(405, "Method not allowed")
			return
		}
		contentType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
		if err != nil || contentType != "application/json" {
			fail(415, "JSON content type required")
			return
		}
		var input struct {
			Provider string `json:"provider"`
			Key      string `json:"key"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			fail(400, "Invalid credential request")
			return
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			fail(400, "Invalid credential request")
			return
		}
		p, ok := reg.providers[input.Provider]
		if !ok || p.APIKeyEnv == "" || p.Type == ProviderTypeCLI {
			fail(404, "Provider not found")
			return
		}
		if credentialManagedExternally(p.APIKeyEnv) {
			fail(409, "This credential is managed by the environment; remove that override before editing here")
			return
		}
		path, err := editableCredentialPath(p.APIKeyEnv)
		if err != nil {
			fail(400, "Invalid credential configuration")
			return
		}
		if req.Method == http.MethodDelete {
			err = os.Remove(path)
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		} else {
			key := strings.TrimSpace(input.Key)
			if key == "" || len(key) > 8192 || strings.ContainsAny(key, "\r\n\x00") {
				fail(400, "Enter a valid non-empty API key")
				return
			}
			err = saveManagedCredential(p.APIKeyEnv, key)
		}
		if err != nil {
			fail(500, "Credential could not be saved")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"providers": desktopProviders(reg)})
	})
}
