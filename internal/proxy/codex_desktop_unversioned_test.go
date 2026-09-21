package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Desktop uses /responses while some CLI builds append /v1/responses.
func TestCodexDesktopBothResponsesPrefixes(t *testing.T) {
	for _, suffix := range []string{"/responses", "/v1/responses", "/responses/compact", "/v1/responses/compact"} {
		for _, native := range []bool{false, true} {
			t.Run(suffix+map[bool]string{false: "-ollama", true: "-native"}[native], func(t *testing.T) {
				gotPath, gotAuth := "", ""
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotPath = r.URL.Path
					gotAuth = r.Header.Get("Authorization")
					w.WriteHeader(http.StatusNoContent)
				}))
				defer upstream.Close()
				handler := newTestCodexDesktop(t, upstream.URL, upstream.URL+"/backend-api/codex", writeCatalog(t, "local-test"))
				model := "local-test"
				if native {
					model = "gpt-5.6-sol"
				}
				req := httptest.NewRequest("POST", CodexDesktopPathPrefix+suffix, strings.NewReader(`{"model":"`+model+`","input":"hello"}`))
				req.RemoteAddr = "127.0.0.1:50000"
				req.Header.Set("Authorization", "Bearer test-native-session")
				req.Header.Set("ChatGPT-Account-ID", "test-account")
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				want := "/v1" + strings.TrimPrefix(suffix, "/v1")
				if native {
					want = "/backend-api/codex" + strings.TrimPrefix(suffix, "/v1")
				}
				if rec.Code != 204 || gotPath != want {
					t.Fatalf("status=%d path=%q want=%q body=%s", rec.Code, gotPath, want, rec.Body)
				}
				if !native && gotAuth != "" {
					t.Fatal("native credentials leaked to Ollama")
				}
				if native && gotAuth != "Bearer test-native-session" {
					t.Fatal("native authentication was lost")
				}
			})
		}
	}
}

func TestCodexDesktopUnversionedWebSocketFallsBackWithoutUpstream(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, "unexpected") }))
	defer upstream.Close()
	handler := newTestCodexDesktop(t, upstream.URL, upstream.URL+"/backend-api/codex", writeCatalog(t, "local-test"))
	req := httptest.NewRequest("GET", CodexDesktopPathPrefix+"/responses", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 426 || calls != 0 {
		t.Fatalf("status=%d calls=%d", rec.Code, calls)
	}
}

func TestCodexDesktopExternalCredentialErrorIsNotOllamaLogin(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Ollama-DZ23-Provider", "external")
		w.WriteHeader(401)
		io.WriteString(w, `{"error":{"message":"external: provider rejected the API key or account permissions (HTTP 401)","type":"api_error"}}`)
	}))
	defer upstream.Close()
	handler := newTestCodexDesktop(t, upstream.URL, upstream.URL+"/backend-api/codex", writeCatalog(t, "external/model"))
	req := httptest.NewRequest("POST", CodexDesktopPathPrefix+"/responses", strings.NewReader(`{"model":"external/model","input":"hi"}`))
	req.RemoteAddr = "127.0.0.1:50000"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), "external: provider rejected") {
		t.Fatalf("wrong error: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "sign in to Ollama") {
		t.Fatal("incorrectly requested an Ollama login for a third-party API")
	}
}
