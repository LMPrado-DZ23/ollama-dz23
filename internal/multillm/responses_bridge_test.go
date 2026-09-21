package multillm

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/ollama/ollama/middleware"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResponsesBridgeChatProvider(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tool := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/tool=%t", stream, tool), func(t *testing.T) {
				var captured map[string]any
				upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/v1beta/openai/chat/completions" {
						t.Errorf("wrong API prefix: %s", r.URL.Path)
					}
					if r.Header.Get("Authorization") != "Bearer provider-test-only" {
						t.Error("wrong provider credential")
					}
					if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
						t.Error(err)
					}
					message := `{"role":"assistant","content":"BRIDGE OK"}`
					delta := `{"content":"BRIDGE OK"}`
					finish := "stop"
					if tool {
						message = `{"role":"assistant","content":"","tool_calls":[{"id":"call_test","type":"function","function":{"name":"lookup","arguments":"{\"value\":1}"}}]}`
						delta = `{"tool_calls":[{"index":0,"id":"call_test","type":"function","function":{"name":"lookup","arguments":"{\"value\":1}"}}]}`
						finish = "tool_calls"
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":%s,\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":%q}]}\n\ndata: [DONE]\n\n", delta, finish)
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(w, `{"choices":[{"message":%s,"finish_reason":%q}]}`, message, finish)
					}
				}))
				defer upstream.Close()
				t.Setenv("BRIDGE_PROVIDER_KEY", "provider-test-only")
				path := filepath.Join(t.TempDir(), "providers.json")
				data, _ := json.Marshal(Config{Providers: []Provider{{Name: "test", Type: ProviderTypeOpenAICompatible, BaseURL: upstream.URL + "/v1beta/openai", AllowPrivate: true, APIKeyEnv: "BRIDGE_PROVIDER_KEY", Models: []ModelConfig{{ID: "remote-model", Capabilities: []string{"chat", "coding", "tools"}}}}}})
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				registry, err := Load(path)
				if err != nil {
					t.Fatal(err)
				}
				g := NewGateway(registry, upstream.Client())
				router := gin.New()
				router.Use(g.Middleware())
				router.POST("/v1/responses", middleware.ResponsesMiddleware(), g.NativeChatMiddleware(), func(c *gin.Context) { c.JSON(404, gin.H{"error": "local model fallback must not run"}) })
				payload := fmt.Sprintf(`{"model":"test/remote-model","input":"hello","stream":%t,"max_output_tokens":17,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"value":{"type":"integer"}}}}]}`, stream)
				req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(payload))
				req.RemoteAddr = "127.0.0.1:23456"
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				body := w.Body.String()
				if w.Code != 200 {
					t.Fatalf("bridge status=%d body=%s", w.Code, body)
				}
				if captured["model"] != "remote-model" || captured["max_tokens"] != float64(17) {
					t.Fatalf("lost request contract: %v", captured)
				}
				if stream {
					if !strings.Contains(body, "response.completed") || !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
						t.Fatalf("invalid Responses stream: type=%s body=%s", w.Header().Get("Content-Type"), body)
					}
				} else {
					var response map[string]any
					if json.Unmarshal(w.Body.Bytes(), &response) != nil || response["object"] != "response" {
						t.Fatalf("invalid Responses body: %s", body)
					}
				}
				if tool && !strings.Contains(body, "function_call") {
					t.Fatalf("lost tool call: %s", body)
				}
				if !tool && !strings.Contains(body, "BRIDGE OK") {
					t.Fatalf("lost text: %s", body)
				}
			})
		}
	}
}

func TestResponsesBridgeAutoAliasDoesNotRequireNativeResponsesSupport(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"AUTO OK"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	path := filepath.Join(t.TempDir(), "providers.json")
	data, _ := json.Marshal(Config{Providers: []Provider{{Name: "test", Type: ProviderTypeOpenAICompatible, BaseURL: upstream.URL + "/v1", AllowPrivate: true, Models: []ModelConfig{{ID: "m", Capabilities: []string{"coding"}}}}}})
	os.WriteFile(path, data, 0600)
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	g := NewGateway(reg, upstream.Client())
	router := gin.New()
	router.Use(g.Middleware())
	router.POST("/v1/responses", middleware.ResponsesMiddleware(), g.NativeChatMiddleware(), func(c *gin.Context) { c.Status(404) })
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"auto/coding","input":"hello","stream":false}`))
	req.RemoteAddr = "127.0.0.1:1200"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "AUTO OK") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
}

func TestResponsesMissingProviderCredentialIsTerminalAndActionable(t *testing.T) {
	t.Setenv("MISSING_DZ23_TEST_KEY", "")
	path := filepath.Join(t.TempDir(), "p.json")
	data := `{"providers":[{"name":"test","type":"openai-compatible","base_url":"https://example.invalid/v1","api_key_env":"MISSING_DZ23_TEST_KEY","models":[{"id":"m"}]}]}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	g := NewGateway(reg, nil)
	router := gin.New()
	router.Use(g.Middleware())
	router.POST("/v1/responses", func(c *gin.Context) { t.Error("unconfigured provider reached inference") })
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test/m","input":"hi"}`))
	req.RemoteAddr = "127.0.0.1:1200"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "provider_not_configured") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
}
