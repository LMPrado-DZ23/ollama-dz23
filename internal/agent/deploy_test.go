package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDeploymentManagerGenericProvider(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>DZ23</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DZ23_DEPLOY_TOKEN", "deploy-test-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/deploy" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer deploy-test-token" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		var payload struct {
			Files []struct {
				File string `json:"file"`
				Data string `json:"data"`
			} `json:"files"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Files) != 1 || payload.Files[0].File != "index.html" {
			t.Fatalf("files=%+v", payload.Files)
		}
		decoded, err := base64.StdEncoding.DecodeString(payload.Files[0].Data)
		if err != nil || string(decoded) != "<h1>DZ23</h1>" {
			t.Fatalf("decoded payload=%q err=%v", decoded, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"dep_123","url":"https://example.test/d/123","status":"ready"}`))
	}))
	defer server.Close()
	manager := NewDeploymentManager()
	manager.client = server.Client()
	if err := manager.Register(DeployConfig{ID: "self", Provider: "generic", BaseURL: server.URL, TokenEnv: "DZ23_DEPLOY_TOKEN"}); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Deploy(context.Background(), "self", DeploymentRequest{Name: "site", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if result.DeploymentID != "dep_123" || result.URL == "" || result.Files != 1 {
		t.Fatalf("result=%+v", result)
	}
}

func TestDeploymentManagerRejectsExternalHTTP(t *testing.T) {
	manager := NewDeploymentManager()
	if err := manager.Register(DeployConfig{ID: "unsafe", Provider: "generic", BaseURL: "http://example.com"}); err == nil {
		t.Fatal("external HTTP deployment unexpectedly accepted")
	}
}
