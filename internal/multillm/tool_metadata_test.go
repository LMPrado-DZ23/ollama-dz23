package multillm

import (
	"encoding/json"
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

func TestResponsesToolMetadataSurvivesClientReplayAndGatewayRestart(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("XDG_CACHE_HOME", root)
	calls := 0
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid upstream request")
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_signed","type":"function","function":{"name":"lookup","arguments":"{\"value\":1}"},"extra_content":{"google":{"thought_signature":"OPAQUE_PROVIDER_CONTEXT_TEST"}}}]},"finish_reason":"tool_calls"}]}`)
			return
		}
		encoded, _ := json.Marshal(body.Messages)
		if !strings.Contains(string(encoded), "OPAQUE_PROVIDER_CONTEXT_TEST") {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"missing provider context"}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"TOOL RESULT ACCEPTED"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	path := filepath.Join(root, "providers.json")
	data, _ := json.Marshal(Config{Providers: []Provider{{Name: "signed", Type: ProviderTypeOpenAICompatible, BaseURL: upstream.URL + "/v1", AllowPrivate: true, Models: []ModelConfig{{ID: "model"}}}}})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	registry, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	send := func(input string) *httptest.ResponseRecorder {
		// A new Gateway instance proves provider context is not lost on restart.
		g := NewGateway(registry, upstream.Client())
		router := gin.New()
		router.Use(g.Middleware())
		router.POST("/v1/responses", middleware.ResponsesMiddleware(), g.NativeChatMiddleware(), func(c *gin.Context) { c.Status(404) })
		req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(input))
		req.RemoteAddr = "127.0.0.1:50000"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	first := send(`{"model":"signed/model","input":"lookup 1","stream":false,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"value":{"type":"integer"}}}}]}`)
	if first.Code != 200 || !strings.Contains(first.Body.String(), "function_call") {
		t.Fatalf("first: %d %s", first.Code, first.Body)
	}
	second := send(`{"model":"signed/model","input":[{"role":"user","content":"lookup 1"},{"type":"function_call","call_id":"call_signed","name":"lookup","arguments":"{\"value\":1}"},{"type":"function_call_output","call_id":"call_signed","output":"result 1"}],"stream":false}`)
	if second.Code != 200 || !strings.Contains(second.Body.String(), "TOOL RESULT ACCEPTED") {
		t.Fatalf("continuation: %d %s", second.Code, second.Body)
	}
}

func TestToolMetadataScopeAndArgumentBinding(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("XDG_CACHE_HOME", root)
	t.Setenv("META_KEY", "credential-one")
	p := Provider{Name: "test", BaseURL: "https://api.example.com/v1", APIKeyEnv: "META_KEY"}
	m := Model{UpstreamID: "test-model"}
	g := NewGateway(nil, nil)
	raw := json.RawMessage(`[{"id":"call_one","type":"function","function":{"name":"lookup","arguments":"{\"value\":1}"},"extra_content":{"google":{"thought_signature":"OPAQUE_TEST"}}}]`)
	if err := g.rememberToolMetadata(p, m, raw); err != nil {
		t.Fatal(err)
	}
	fresh := func() []map[string]any {
		var calls []any
		json.Unmarshal(raw, &calls)
		delete(calls[0].(map[string]any), "extra_content")
		return []map[string]any{{"role": "assistant", "tool_calls": calls}}
	}
	check := func(p Provider, m Model, want bool) {
		t.Helper()
		messages := fresh()
		if err := g.restoreToolMetadata(p, m, messages); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(messages)
		if strings.Contains(string(data), "OPAQUE_TEST") != want {
			t.Fatalf("unexpected metadata scope; wanted %t", want)
		}
	}
	check(p, m, true)
	other := p
	other.BaseURL = "https://other.example/v1"
	check(other, m, false)
	check(p, Model{UpstreamID: "another-model"}, false)
	t.Setenv("META_KEY", "credential-two")
	check(p, m, false)

}

func TestStreamingToolMetadataIsRetainedAndPrivate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("XDG_CACHE_HOME", root)
	p := Provider{Name: "signed", BaseURL: "https://example.com/v1"}
	m := Model{ID: "signed/model", UpstreamID: "model"}
	g := NewGateway(nil, nil)
	stream := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_stream","type":"function","function":{"name":"lookup","arguments":"{\"value\":1}"},"extra_content":{"google":{"thought_signature":"OPAQUE_STREAM_TEST"}}}]},"finish_reason":null}]}` + "\n\n" + `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if err := translateChatStream(c, strings.NewReader(stream), m.ID, "/api/chat", func(raw json.RawMessage) error { return g.rememberToolMetadata(p, m, raw) }); err != nil {
		t.Fatal(err)
	}
	calls := []any{map[string]any{"id": "call_stream", "type": "function", "function": map[string]any{"name": "lookup", "arguments": "{\"value\":1}"}}}
	messages := []map[string]any{{"role": "assistant", "tool_calls": calls}}
	if err := NewGateway(nil, nil).restoreToolMetadata(p, m, messages); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(messages)
	if !strings.Contains(string(raw), "OPAQUE_STREAM_TEST") {
		t.Fatal("stream metadata was lost")
	}
	calls[0].(map[string]any)["function"].(map[string]any)["arguments"] = "{\"value\":2}"
	delete(calls[0].(map[string]any), "extra_content")
	if err := g.restoreToolMetadata(p, m, messages); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(messages)
	if strings.Contains(string(raw), "OPAQUE_STREAM_TEST") {
		t.Fatal("signature reused for different function arguments")
	}
	files, err := filepath.Glob(filepath.Join(root, "Ollama DZ23", "tool-context", "*"))
	if err != nil || len(files) != 1 {
		t.Fatalf("unexpected stored metadata files: %v %v", files, err)
	}
	stored, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(files[0], ".dpapi") && strings.Contains(string(stored), "OPAQUE_STREAM_TEST") {
		t.Fatal("Windows provider context was stored without DPAPI protection")
	}
}
