package multillm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func desktopFixture(t *testing.T) http.Handler {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("APPDATA", root)
	t.Setenv("DZ23_TEST_API_KEY", "")
	t.Setenv("DZ23_TEST_API_KEY_FILE", "")
	path := filepath.Join(root, "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"test","type":"openai-compatible","base_url":"https://example.com/v1","api_key_env":"DZ23_TEST_API_KEY","models":[{"id":"test-model"}]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OLLAMA_DZ23_CONFIG", path)
	return DesktopCredentialsHandler("desktop-test-token")
}

func desktopRequest(h http.Handler, method, body, token, origin, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1:5555/api/v1/dz23/providers", strings.NewReader(body))
	req.RemoteAddr = remote
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "token", Value: token})
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestDesktopCredentialsLifecycle(t *testing.T) {
	h := desktopFixture(t)
	call := func(method, body string) *httptest.ResponseRecorder {
		return desktopRequest(h, method, body, "desktop-test-token", "http://127.0.0.1:5555", "127.0.0.1:12345")
	}
	reg, err := Load(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"first-secret", "replacement-secret"} {
		rr := call("PUT", `{"provider":"test","key":"`+key+`"}`)
		if rr.Code != 200 {
			t.Fatalf("save: %d %s", rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), key) {
			t.Fatal("secret leaked in response")
		}
		if credentialValue("DZ23_TEST_API_KEY") != key {
			t.Fatal("credential not readable")
		}
		model, _ := reg.Model("test/test-model")
		if !model.Available {
			t.Fatal("running registry did not pick up key")
		}
	}
	rr := call("GET", "")
	if rr.Code != 200 || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unsafe list response")
	}
	var result struct {
		Providers []DesktopProvider `json:"providers"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Providers) != 1 || !result.Providers[0].Configured {
		t.Fatal("saved status missing")
	}
	rr = call("DELETE", `{"provider":"test"}`)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	if credentialValue("DZ23_TEST_API_KEY") != "" {
		t.Fatal("credential not removed")
	}
	model, _ := reg.Model("test/test-model")
	if model.Available {
		t.Fatal("deleted key still available")
	}
}

func TestDesktopCredentialsBoundary(t *testing.T) {
	h := desktopFixture(t)
	for _, test := range []struct{ name, token, origin, remote string }{
		{"missing token", "", "", "127.0.0.1:1234"},
		{"wrong token", "wrong", "", "127.0.0.1:1234"},
		{"cross origin", "desktop-test-token", "https://evil.example", "127.0.0.1:1234"},
		{"null origin", "desktop-test-token", "null", "127.0.0.1:1234"},
		{"remote client", "desktop-test-token", "", "192.0.2.1:1234"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rr := desktopRequest(h, "PUT", `{"provider":"test","key":"must-not-save"}`, test.token, test.origin, test.remote)
			if rr.Code != 403 {
				t.Fatalf("status %d", rr.Code)
			}
			if credentialValue("DZ23_TEST_API_KEY") != "" {
				t.Fatal("unauthorized write")
			}
		})
	}
	for _, body := range []string{`{"provider":"test","key":""}`, `{"provider":"test","key":"a\\nb"}`, `{"provider":"test","key":"abc","unknown":true}`, `{"provider":"test","key":"abc"} {}`, `{"provider":"test","key":"` + strings.Repeat("a", 20000) + `"}`} {
		// The escaped newline is a literal backslash in the JSON source; decode a real newline escape.
		body = strings.ReplaceAll(body, `a\\nb`, `a\nb`)
		rr := desktopRequest(h, "PUT", body, "desktop-test-token", "", "127.0.0.1:1234")
		if rr.Code != 400 {
			t.Fatalf("invalid input accepted: %d", rr.Code)
		}
	}
	rr := desktopRequest(h, "PUT", `{"provider":"../../escape","key":"abc"}`, "desktop-test-token", "", "127.0.0.1:1234")
	if rr.Code != 404 {
		t.Fatal("unknown provider accepted")
	}
	t.Setenv("DZ23_TEST_API_KEY", "external-secret")
	rr = desktopRequest(h, "DELETE", `{"provider":"test"}`, "desktop-test-token", "", "127.0.0.1:1234")
	if rr.Code != 409 {
		t.Fatal("external credential modified")
	}
	if _, err := managedCredentialPath("../../escape"); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestConfigPathFindsInstallerConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("APPDATA", root)
	t.Setenv("OLLAMA_DZ23_CONFIG", "")
	if ConfigPath() != "" {
		t.Fatal("enabled without a config")
	}
	p := filepath.Join(root, "Ollama DZ23", "dz23-providers.json")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"providers":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if ConfigPath() != p {
		t.Fatal("installer config not found")
	}
	t.Setenv("OLLAMA_DZ23_CONFIG", "custom.json")
	if ConfigPath() != "custom.json" {
		t.Fatal("explicit config ignored")
	}
}
