package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDZ23CatalogEndpoint(t *testing.T) {
	t.Setenv("OLLAMA_DZ23_CONFIG", "")
	handler, err := (&Server{}).GenerateRoutes()
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/dz23/cli-catalog", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Tools) != 46 {
		t.Fatalf("catalog has %d tools", len(response.Tools))
	}
}

func TestDZ23ConfiguredProviderAppearsInModelCatalog(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OLLAMA_MODELS", filepath.Join(dir, "models"))
	t.Setenv("TEST_DZ23_KEY", "secret")
	configPath := filepath.Join(dir, "providers.json")
	config := `{"providers":[{"name":"test","type":"openai-compatible","base_url":"https://example.com/v1","api_key_env":"TEST_DZ23_KEY","models":[{"id":"model","capabilities":["chat"]}]}]}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OLLAMA_DZ23_CONFIG", configPath)
	handler, err := (&Server{}).GenerateRoutes()
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/tags", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !containsBytes(recorder.Body.Bytes(), []byte(`"model":"test/model"`)) {
		t.Fatalf("remote model missing: %s", recorder.Body.String())
	}

	show := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/show", strings.NewReader(`{"model":"test/model"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(show, request)
	if show.Code != http.StatusOK || !containsBytes(show.Body.Bytes(), []byte(`"format":"remote"`)) || !containsBytes(show.Body.Bytes(), []byte(`"chat"`)) {
		t.Fatalf("remote show status=%d body=%s", show.Code, show.Body.String())
	}
}

func containsBytes(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
