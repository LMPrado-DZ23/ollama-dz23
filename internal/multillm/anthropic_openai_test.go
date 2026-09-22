package multillm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestOpenAICompatibleProviderServesAnthropicMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotPath, gotModel, gotAuthorization string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotModel, _ = body["model"].(string)
		messages, _ := body["messages"].([]any)
		if len(messages) != 2 {
			t.Fatalf("messages = %#v", messages)
		}
		if _, ok := body["tools"].([]any); !ok {
			t.Fatalf("tools were not converted: %#v", body["tools"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","model":"gpt-test","choices":[{"finish_reason":"tool_calls","message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Recife\"}"}}]}}],"usage":{"prompt_tokens":12,"completion_tokens":4}}`))
	}))
	defer upstream.Close()

	t.Setenv("OPENAI_TEST_KEY", "provider-secret")
	registry := &Registry{
		providers: map[string]Provider{
			"openai": {Name: "openai", Type: ProviderTypeOpenAICompatible, BaseURL: upstream.URL + "/v1", APIKeyEnv: "OPENAI_TEST_KEY", AllowPrivate: true, Paths: []string{"/v1/messages"}},
		},
		models: map[string]Model{
			"openai/gpt-test": {ID: "openai/gpt-test", UpstreamID: "gpt-test", Provider: "openai", Available: true},
		},
	}
	router := gin.New()
	router.Use(NewGateway(registry, upstream.Client()).Middleware())
	router.POST("/v1/messages", func(c *gin.Context) { t.Fatal("request was not proxied") })
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"openai/gpt-test","max_tokens":128,"system":"be concise","messages":[{"role":"user","content":"What is the weather?"}],"tools":[{"name":"weather","description":"Get weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}],"tool_choice":{"type":"any"}}`))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Authorization", "Bearer client-secret")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || gotPath != "/v1/chat/completions" || gotModel != "gpt-test" {
		t.Fatalf("status=%d path=%q model=%q body=%s", recorder.Code, gotPath, gotModel, recorder.Body.String())
	}
	if gotAuthorization != "Bearer provider-secret" {
		t.Fatalf("provider authorization = %q", gotAuthorization)
	}
	if !strings.Contains(recorder.Body.String(), `"type":"tool_use"`) || !strings.Contains(recorder.Body.String(), `"name":"weather"`) {
		t.Fatalf("Anthropic tool response was not converted: %s", recorder.Body.String())
	}
}

func TestOpenAICompatibleProviderStreamsAnthropicMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	registry := &Registry{
		providers: map[string]Provider{
			"openai": {Name: "openai", Type: ProviderTypeOpenAICompatible, BaseURL: upstream.URL + "/v1", AllowPrivate: true, Paths: []string{"/v1/messages"}},
		},
		models: map[string]Model{
			"openai/gpt-test": {ID: "openai/gpt-test", UpstreamID: "gpt-test", Provider: "openai", Available: true},
		},
	}
	router := gin.New()
	router.Use(NewGateway(registry, upstream.Client()).Middleware())
	router.POST("/v1/messages", func(c *gin.Context) { t.Fatal("request was not proxied") })
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"openai/gpt-test","stream":true,"messages":[{"role":"user","content":"hello"}]}`))
	request.RemoteAddr = "127.0.0.1:12345"
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, "event: message_start") || !strings.Contains(body, `"type":"text_delta"`) || !strings.Contains(body, `"text":"hello"`) || !strings.Contains(body, "event: message_stop") {
		t.Fatalf("status=%d content-type=%q body=%s", recorder.Code, recorder.Header().Get("Content-Type"), body)
	}
}
