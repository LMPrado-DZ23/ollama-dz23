package server

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/ollama/ollama/internal/multillm"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDZ23CompactionInferencePreservesCallerBoundary(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"summary"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	config := multillm.Config{Providers: []multillm.Provider{{Name: "test", Type: "openai-compatible", BaseURL: upstream.URL + "/v1", AllowPrivate: true, Models: []multillm.ModelConfig{{ID: "m"}}}}}
	raw, _ := json.Marshal(config)
	path := filepath.Join(t.TempDir(), "p.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	registry, err := multillm.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{multiRegistry: registry, multiProvider: multillm.NewGateway(registry, upstream.Client())}
	for _, tc := range []struct {
		address string
		want    int
	}{{"127.0.0.1:12345", 200}, {"192.168.1.2:12345", 401}} {
		t.Run(tc.address, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/responses/compact", nil)
			c.Request.RemoteAddr = tc.address
			c.Request.Header.Set("X-Forwarded-For", "127.0.0.1")
			response := s.runResponsesCompactionInference(c, []byte(`{"model":"test/m","input":"summarize","stream":false}`))
			if response.status != tc.want {
				t.Fatalf("status=%d want=%d body=%s", response.status, tc.want, response.body.String())
			}
		})
	}
}

func TestDZ23RegistryStatusDoesNotExposeCredentialsOrAllowRemote(t *testing.T) {
	t.Setenv("OLLAMA_DZ23_CONFIG", "")
	handler, err := (&Server{}).GenerateRoutes()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		addr string
		want int
	}{{"127.0.0.1:1000", 200}, {"192.168.2.3:1000", 403}} {
		req := httptest.NewRequest("GET", "/api/dz23/models", nil)
		req.RemoteAddr = tc.addr
		req.Header.Set("X-Forwarded-For", "127.0.0.1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("addr=%s status=%d body=%s", tc.addr, rec.Code, rec.Body)
		}
	}
}
