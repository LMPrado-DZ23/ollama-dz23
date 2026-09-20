package multillm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const maxRequestBytes = 32 << 20
const maxResponseBytes = 128 << 20

var errResponseLimit = errors.New("provider response exceeded limit")

type Gateway struct {
	registry *Registry
	client   *http.Client
}

func NewGateway(registry *Registry, client *http.Client) *Gateway {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	return &Gateway{registry: registry, client: client}
}

func (g *Gateway) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost || !supportedPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxRequestBytes+1))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "read request body"})
			return
		}
		if len(body) > maxRequestBytes {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(body, &envelope); err != nil {
			c.Next()
			return
		}
		var requested string
		if err := json.Unmarshal(envelope["model"], &requested); err != nil || requested == "" {
			c.Next()
			return
		}
		if requested == "local/private" {
			localModel := strings.TrimSpace(os.Getenv("OLLAMA_DZ23_LOCAL_MODEL"))
			if localModel == "" {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "OLLAMA_DZ23_LOCAL_MODEL is not configured"})
				return
			}
			encoded, _ := json.Marshal(localModel)
			envelope["model"] = encoded
			rewritten, err := json.Marshal(envelope)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "encode local request"})
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(rewritten))
			c.Request.ContentLength = int64(len(rewritten))
			c.Next()
			return
		}
		if registered, exists := g.registry.Model(requested); exists && !registered.Available {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "model provider is configured but unavailable"})
			return
		}
		model, ok := g.registry.Resolve(requested, Policy{Path: c.Request.URL.Path})
		if !ok {
			if strings.HasPrefix(requested, "auto/") || requested == "auto" {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "no available provider satisfies the routing policy"})
				return
			}
			c.Next()
			return
		}
		if !g.registry.Authorize(c.Request) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "remote provider access requires a gateway API key"})
			return
		}
		provider, ok := g.registry.Provider(model.Provider)
		if !ok {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "configured provider is unavailable"})
			return
		}
		if !provider.SupportsPath(c.Request.URL.Path) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "provider does not support this API path"})
			return
		}
		if provider.Type == ProviderTypeCLI {
			if err := g.executeCLI(c, provider, model, envelope); err != nil {
				slog.Warn("DZ23 CLI request failed", "provider", provider.Name, "error", err)
				c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "CLI execution failed"})
			}
			return
		}
		if provider.Type == ProviderTypeAnthropic {
			if err := g.forwardAnthropic(c, provider, model, envelope); err != nil {
				slog.Warn("DZ23 Anthropic request failed", "provider", provider.Name, "error", err)
				if !c.Writer.Written() {
					c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "provider request failed"})
				}
			}
			c.Abort()
			return
		}
		if c.Request.URL.Path == "/api/chat" || c.Request.URL.Path == "/api/generate" {
			if err := g.forwardNative(c, provider, model, envelope); err != nil {
				slog.Warn("DZ23 native provider request failed", "provider", provider.Name, "error", err)
				c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "provider request failed"})
			}
			c.Abort()
			return
		}
		upstreamModel, _ := json.Marshal(model.UpstreamID)
		envelope["model"] = upstreamModel
		forwardBody, err := json.Marshal(envelope)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "encode provider request"})
			return
		}
		if err := g.forward(c, provider, forwardBody); err != nil {
			slog.Warn("DZ23 provider request failed", "provider", provider.Name, "error", err)
			if errors.Is(err, errResponseLimit) && c.Writer.Written() {
				c.Abort()
				return
			}
			c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "provider request failed"})
			return
		}
		c.Abort()
	}
}

func (g *Gateway) executeCLI(c *gin.Context, provider Provider, model Model, envelope map[string]json.RawMessage) error {
	var streaming bool
	_ = json.Unmarshal(envelope["stream"], &streaming)
	if streaming {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "streaming is not supported by CLI adapters"})
		return nil
	}
	prompt, err := promptFromEnvelope(envelope)
	if err != nil {
		return err
	}
	timeout := time.Duration(provider.TimeoutSeconds) * time.Second
	if timeout <= 0 || timeout > 30*time.Minute {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, provider.Executable, provider.Args...)
	command.Stdin = strings.NewReader(prompt)
	var stdout, stderr limitedBuffer
	stdout.limit = 16 << 20
	stderr.limit = 64 << 10
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s failed: %w: %s", provider.Name, err, strings.TrimSpace(stderr.String()))
	}
	output := strings.TrimSpace(stdout.String())
	if c.Request.URL.Path == "/api/chat" {
		c.Header("Content-Type", "application/json")
		c.JSON(http.StatusOK, gin.H{"model": model.ID, "created_at": time.Now().UTC().Format(time.RFC3339Nano), "message": gin.H{"role": "assistant", "content": output}, "done": true, "done_reason": "stop"})
	} else if c.Request.URL.Path == "/api/generate" {
		c.Header("Content-Type", "application/json")
		c.JSON(http.StatusOK, gin.H{"model": model.ID, "created_at": time.Now().UTC().Format(time.RFC3339Nano), "response": output, "done": true, "done_reason": "stop"})
	} else if c.Request.URL.Path == "/v1/responses" {
		c.JSON(http.StatusOK, gin.H{"id": "resp_dz23_cli", "object": "response", "status": "completed", "model": model.ID, "output": []gin.H{{"id": "msg_dz23_cli", "type": "message", "role": "assistant", "status": "completed", "content": []gin.H{{"type": "output_text", "text": output, "annotations": []any{}}}}}})
	} else {
		c.JSON(http.StatusOK, gin.H{"id": "chatcmpl-dz23-cli", "object": "chat.completion", "model": model.ID, "choices": []gin.H{{"index": 0, "finish_reason": "stop", "message": gin.H{"role": "assistant", "content": output}}}})
	}
	c.Abort()
	return nil
}

func promptFromEnvelope(envelope map[string]json.RawMessage) (string, error) {
	if raw := envelope["prompt"]; len(raw) > 0 {
		var text string
		if json.Unmarshal(raw, &text) == nil && text != "" {
			return text, nil
		}
	}
	if raw := envelope["input"]; len(raw) > 0 {
		var text string
		if json.Unmarshal(raw, &text) == nil && text != "" {
			return text, nil
		}
	}
	var messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(envelope["messages"], &messages); err != nil {
		return "", errors.New("CLI adapter requires text input or messages")
	}
	var b strings.Builder
	for _, message := range messages {
		var content string
		if json.Unmarshal(message.Content, &content) == nil {
			fmt.Fprintf(&b, "%s: %s\n", message.Role, content)
		}
	}
	if b.Len() == 0 {
		return "", errors.New("CLI adapter did not receive textual content")
	}
	return b.String(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("CLI output limit exceeded")
	}
	return b.Buffer.Write(p)
}

func supportedPath(path string) bool {
	switch path {
	case "/api/chat", "/api/generate", "/v1/chat/completions", "/v1/completions", "/v1/responses", "/v1/embeddings", "/v1/messages":
		return true
	default:
		return false
	}
}

func (g *Gateway) forwardNative(c *gin.Context, provider Provider, model Model, envelope map[string]json.RawMessage) error {
	var stream bool
	if raw := envelope["stream"]; len(raw) == 0 {
		stream = true
	} else {
		_ = json.Unmarshal(raw, &stream)
	}
	request := make(map[string]any)
	request["model"] = model.UpstreamID
	request["stream"] = stream
	if c.Request.URL.Path == "/api/generate" {
		var prompt, system string
		_ = json.Unmarshal(envelope["prompt"], &prompt)
		_ = json.Unmarshal(envelope["system"], &system)
		messages := make([]map[string]any, 0, 2)
		if system != "" {
			messages = append(messages, map[string]any{"role": "system", "content": system})
		}
		messages = append(messages, map[string]any{"role": "user", "content": prompt})
		request["messages"] = messages
	} else {
		messages, err := openAICompatibleMessages(envelope["messages"])
		if err != nil {
			return errors.New("native chat request requires messages")
		}
		request["messages"] = messages
		for _, field := range []string{"tools", "format"} {
			if raw := envelope[field]; len(raw) > 0 {
				var value any
				if json.Unmarshal(raw, &value) == nil {
					request[field] = value
				}
			}
		}
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	resp, err := g.doProviderRequest(c, provider, "/v1/chat/completions", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return writeBufferedProviderResponse(c, resp)
	}
	if stream {
		c.Header("Content-Type", "application/x-ndjson")
		return translateChatStream(c, resp.Body, model.ID, c.Request.URL.Path)
	}
	var completion struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string          `json:"content"`
				ToolCalls json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(responseBody) > maxResponseBytes {
		return errors.New("provider response exceeded limit")
	}
	if err := json.Unmarshal(responseBody, &completion); err != nil {
		return err
	}
	if len(completion.Choices) == 0 {
		return errors.New("provider response contained no choices")
	}
	choice := completion.Choices[0]
	c.Header("Content-Type", "application/json")
	writeNativeChunk(c, model.ID, c.Request.URL.Path, choice.Message.Content, choice.Message.ToolCalls, true, choice.FinishReason, false)
	return nil
}

func (g *Gateway) doProviderRequest(c *gin.Context, provider Provider, path string, body []byte) (*http.Response, error) {
	base, err := url.Parse(provider.BaseURL)
	if err != nil {
		return nil, err
	}
	approvedIPs, err := resolveProviderDestination(c.Request.Context(), provider)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(base.Path, "/v1") && strings.HasPrefix(path, "/v1/") {
		path = strings.TrimPrefix(path, "/v1")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, base.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	copyRequestHeaders(req.Header, c.Request.Header)
	applyProviderAuth(req, provider)
	client := *g.client
	transport, err := pinnedProviderTransport(client.Transport, approvedIPs)
	if err != nil {
		return nil, err
	}
	client.Transport = transport
	client.CheckRedirect = sameHostRedirects(base.Host, client.CheckRedirect)
	return client.Do(req)
}

func resolveProviderDestination(ctx context.Context, provider Provider) ([]net.IP, error) {
	if provider.AllowPrivate {
		return nil, nil
	}
	base, err := url.Parse(provider.BaseURL)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, base.Hostname())
	if err != nil {
		return nil, fmt.Errorf("resolve provider host: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("provider host resolved to no addresses")
	}
	approved := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		if unsafeProviderIP(address.IP) {
			return nil, errors.New("provider host resolved to a private or link-local address")
		}
		approved = append(approved, append(net.IP(nil), address.IP...))
	}
	return approved, nil
}

func pinnedProviderTransport(source http.RoundTripper, approved []net.IP) (http.RoundTripper, error) {
	if len(approved) == 0 {
		return source, nil
	}
	if source == nil {
		source = http.DefaultTransport
	}
	base, ok := source.(*http.Transport)
	if !ok {
		return nil, errors.New("provider client transport cannot enforce destination pinning")
	}
	transport := base.Clone()
	transport.Proxy = nil
	transport.DialTLS = nil
	transport.DialTLSContext = nil
	dial := transport.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, ip := range approved {
			connection, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return connection, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
	return transport, nil
}

func translateChatStream(c *gin.Context, body io.Reader, modelID, nativePath string) error {
	scanner := bufio.NewScanner(&boundedReader{reader: body, remaining: maxResponseBytes})
	buffer := make([]byte, 64<<10)
	scanner.Buffer(buffer, maxResponseBytes)
	doneWritten := false
	toolCalls := make(map[int]*openAIToolCall)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			if !doneWritten {
				writeNativeChunk(c, modelID, nativePath, "", nil, true, "stop", true)
			}
			return nil
		}
		var chunk struct {
			Choices []struct {
				FinishReason string `json:"finish_reason"`
				Delta        struct {
					Content   string          `json:"content"`
					ToolCalls json.RawMessage `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		accumulateOpenAIToolCalls(toolCalls, choice.Delta.ToolCalls)
		done := choice.FinishReason != ""
		doneWritten = doneWritten || done
		var completedCalls json.RawMessage
		if done {
			completedCalls = normalizedAccumulatedToolCalls(toolCalls)
		}
		writeNativeChunk(c, modelID, nativePath, choice.Delta.Content, completedCalls, done, choice.FinishReason, true)
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	if errors.Is(scanner.Err(), errResponseLimit) {
		payload, _ := json.Marshal(gin.H{"error": "provider response exceeded limit", "done": true})
		_, _ = c.Writer.Write(append(payload, '\n'))
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return nil
	}
	return scanner.Err()
}

func writeNativeChunk(c *gin.Context, modelID, nativePath, content string, toolCalls json.RawMessage, done bool, reason string, streaming bool) {
	if streaming {
		c.Header("Content-Type", "application/x-ndjson")
	} else {
		c.Header("Content-Type", "application/json")
	}
	result := map[string]any{"model": modelID, "created_at": time.Now().UTC().Format(time.RFC3339Nano), "done": done}
	if done && reason != "" {
		result["done_reason"] = reason
	}
	if nativePath == "/api/generate" {
		result["response"] = content
	} else {
		message := map[string]any{"role": "assistant", "content": content}
		if len(toolCalls) > 0 && string(toolCalls) != "null" {
			calls, err := normalizeOpenAIToolCalls(toolCalls)
			if err == nil && len(calls) > 0 {
				message["tool_calls"] = calls
			}
		}
		result["message"] = message
	}
	b, _ := json.Marshal(result)
	_, _ = c.Writer.Write(append(b, '\n'))
}

type openAIToolCall struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

func normalizeOpenAIToolCalls(raw json.RawMessage) ([]map[string]any, error) {
	var calls []openAIToolCall
	if err := json.Unmarshal(raw, &calls); err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		arguments := map[string]any{}
		if call.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(call.Function.Arguments), &arguments); err != nil {
				return nil, err
			}
		}
		nativeCall := map[string]any{"function": map[string]any{"name": call.Function.Name, "arguments": arguments}}
		if call.ID != "" {
			nativeCall["id"] = call.ID
		}
		result = append(result, nativeCall)
	}
	return result, nil
}

func openAICompatibleMessages(raw json.RawMessage) ([]map[string]any, error) {
	var messages []map[string]any
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, err
	}
	callIDs := make(map[string]string)
	for messageIndex, message := range messages {
		if calls, ok := message["tool_calls"].([]any); ok {
			for callIndex, value := range calls {
				call, ok := value.(map[string]any)
				if !ok {
					continue
				}
				id, _ := call["id"].(string)
				if id == "" {
					id = fmt.Sprintf("call_dz23_%d_%d", messageIndex, callIndex)
					call["id"] = id
				}
				call["type"] = "function"
				if function, ok := call["function"].(map[string]any); ok {
					name, _ := function["name"].(string)
					if name != "" {
						callIDs[name] = id
					}
					if _, isString := function["arguments"].(string); !isString {
						encoded, err := json.Marshal(function["arguments"])
						if err != nil {
							return nil, err
						}
						function["arguments"] = string(encoded)
					}
				}
			}
		}
		if message["role"] == "tool" {
			if _, ok := message["tool_call_id"].(string); !ok {
				if name, ok := message["tool_name"].(string); ok {
					message["tool_call_id"] = callIDs[name]
				}
			}
			delete(message, "tool_name")
		}
	}
	return messages, nil
}

func accumulateOpenAIToolCalls(accumulator map[int]*openAIToolCall, raw json.RawMessage) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	var chunks []openAIToolCall
	if json.Unmarshal(raw, &chunks) != nil {
		return
	}
	for _, chunk := range chunks {
		call := accumulator[chunk.Index]
		if call == nil {
			call = &openAIToolCall{Index: chunk.Index}
			accumulator[chunk.Index] = call
		}
		if chunk.ID != "" {
			call.ID = chunk.ID
		}
		if chunk.Type != "" {
			call.Type = chunk.Type
		}
		call.Function.Name += chunk.Function.Name
		call.Function.Arguments += chunk.Function.Arguments
	}
}

func normalizedAccumulatedToolCalls(accumulator map[int]*openAIToolCall) json.RawMessage {
	if len(accumulator) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(accumulator))
	for index := range accumulator {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	calls := make([]openAIToolCall, 0, len(indexes))
	for _, index := range indexes {
		calls = append(calls, *accumulator[index])
	}
	raw, _ := json.Marshal(calls)
	return raw
}

func (g *Gateway) forward(c *gin.Context, provider Provider, body []byte) error {
	resp, err := g.doProviderRequest(c, provider, c.Request.URL.Path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		if resp.ContentLength > maxResponseBytes {
			return errors.New("provider response exceeded limit")
		}
		copyResponseHeaders(c.Writer.Header(), resp.Header)
		c.Status(resp.StatusCode)
		return copySSEBounded(c.Writer, resp.Body, maxResponseBytes)
	}
	return writeBufferedProviderResponse(c, resp)
}

func writeBufferedProviderResponse(c *gin.Context, resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxResponseBytes {
		return errors.New("provider response exceeded limit")
	}
	copyResponseHeaders(c.Writer.Header(), resp.Header)
	c.Status(resp.StatusCode)
	_, err = c.Writer.Write(body)
	return err
}

type boundedReader struct {
	reader    io.Reader
	remaining int64
}

func (r *boundedReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		var probe [1]byte
		n, err := r.reader.Read(probe[:])
		if n > 0 {
			return 0, errResponseLimit
		}
		return 0, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}

func copySSEBounded(writer io.Writer, reader io.Reader, limit int64) error {
	_, err := io.Copy(writer, &boundedReader{reader: reader, remaining: limit})
	if errors.Is(err, errResponseLimit) {
		_, writeErr := io.WriteString(writer, "\nevent: error\ndata: {\"error\":{\"message\":\"provider response exceeded limit\",\"type\":\"response_too_large\"}}\n\n")
		if writeErr != nil {
			return writeErr
		}
		return errResponseLimit
	}
	return err
}

func sameHostRedirects(host string, configured func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(next *http.Request, via []*http.Request) error {
		if next.URL.Scheme != "https" {
			return errors.New("provider redirect must use HTTPS")
		}
		if next.URL.Host != host {
			return errors.New("provider redirect changed host")
		}
		if configured != nil {
			return configured(next, via)
		}
		if len(via) >= 10 {
			return errors.New("too many provider redirects")
		}
		return nil
	}
}

func applyProviderAuth(req *http.Request, provider Provider) {
	key := ""
	if provider.APIKeyEnv != "" {
		key = credentialValue(provider.APIKeyEnv)
	}
	style := provider.AuthStyle
	if style == "" && provider.Type == ProviderTypeAnthropic {
		style = AuthStyleAnthropic
	}
	switch style {
	case AuthStyleAPIKey:
		req.Header.Set("X-API-Key", key)
	case AuthStyleAnthropic:
		req.Header.Set("X-API-Key", key)
		req.Header.Set("Anthropic-Version", "2023-06-01")
	case AuthStyleGoogleQuery:
		q := req.URL.Query()
		q.Set("key", key)
		req.URL.RawQuery = q.Encode()
	default:
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
}

func copyRequestHeaders(dst, src http.Header) {
	for _, name := range []string{"Accept", "Content-Type", "OpenAI-Beta", "User-Agent"} {
		if value := src.Get(name); value != "" {
			dst.Set(name, value)
		}
	}
	dst.Set("Content-Type", "application/json")
}

func copyResponseHeaders(dst, src http.Header) {
	for _, name := range []string{"Content-Type", "Cache-Control", "Retry-After", "X-Request-ID"} {
		if value := src.Get(name); value != "" {
			dst.Set(name, value)
		}
	}
}
