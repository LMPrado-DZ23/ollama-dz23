package multillm

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type anthropicResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
}

func (g *Gateway) forwardAnthropic(c *gin.Context, provider Provider, model Model, envelope map[string]json.RawMessage) error {
	body, stream, err := anthropicRequest(c.Request.URL.Path, model.UpstreamID, envelope)
	if err != nil {
		return err
	}
	resp, err := g.doProviderRequest(c, provider, "/v1/messages", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return writeBufferedProviderResponse(c, resp)
	}
	if c.Request.URL.Path == "/v1/messages" {
		if stream {
			copyResponseHeaders(c.Writer.Header(), resp.Header)
			c.Status(resp.StatusCode)
			return copySSEBounded(c.Writer, resp.Body, maxResponseBytes)
		}
		return writeBufferedProviderResponse(c, resp)
	}
	if stream {
		return translateAnthropicStream(c, resp.Body, model.ID, c.Request.URL.Path)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxResponseBytes {
		return errResponseLimit
	}
	var result anthropicResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	text, toolCalls := anthropicContent(result)
	if c.Request.URL.Path == "/api/chat" || c.Request.URL.Path == "/api/generate" {
		writeNativeChunk(c, model.ID, c.Request.URL.Path, text, toolCalls, true, anthropicFinishReason(result.StopReason), false)
		return nil
	}
	c.JSON(http.StatusOK, gin.H{
		"id": result.ID, "object": "chat.completion", "model": model.ID,
		"choices": []gin.H{{"index": 0, "finish_reason": anthropicFinishReason(result.StopReason), "message": gin.H{"role": "assistant", "content": text, "tool_calls": rawJSONValue(toolCalls)}}},
	})
	return nil
}

func anthropicRequest(path, upstreamModel string, envelope map[string]json.RawMessage) ([]byte, bool, error) {
	if path == "/v1/messages" {
		envelope["model"], _ = json.Marshal(upstreamModel)
		var stream bool
		_ = json.Unmarshal(envelope["stream"], &stream)
		body, err := json.Marshal(envelope)
		return body, stream, err
	}
	request := map[string]any{"model": upstreamModel, "max_tokens": 4096}
	stream := strings.HasPrefix(path, "/api/")
	if raw := envelope["stream"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &stream)
	}
	request["stream"] = stream
	if raw := envelope["max_tokens"]; len(raw) > 0 {
		var maxTokens int
		if json.Unmarshal(raw, &maxTokens) == nil && maxTokens > 0 {
			request["max_tokens"] = maxTokens
		}
	}

	var source []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	}
	if path == "/api/generate" {
		var prompt, system string
		_ = json.Unmarshal(envelope["prompt"], &prompt)
		_ = json.Unmarshal(envelope["system"], &system)
		if system != "" {
			request["system"] = system
		}
		source = append(source, struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		}{Role: "user", Content: prompt})
	} else if err := json.Unmarshal(envelope["messages"], &source); err != nil {
		return nil, false, errors.New("Anthropic adapter requires messages")
	}
	messages := make([]map[string]any, 0, len(source))
	for _, message := range source {
		if message.Role == "system" {
			if text, ok := message.Content.(string); ok {
				request["system"] = text
			}
			continue
		}
		role := message.Role
		if role != "assistant" {
			role = "user"
		}
		messages = append(messages, map[string]any{"role": role, "content": message.Content})
	}
	request["messages"] = messages
	if raw := envelope["tools"]; len(raw) > 0 {
		var tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				Parameters  map[string]any `json:"parameters"`
			} `json:"function"`
		}
		if json.Unmarshal(raw, &tools) == nil {
			converted := make([]map[string]any, 0, len(tools))
			for _, tool := range tools {
				converted = append(converted, map[string]any{"name": tool.Function.Name, "description": tool.Function.Description, "input_schema": tool.Function.Parameters})
			}
			request["tools"] = converted
		}
	}
	body, err := json.Marshal(request)
	return body, stream, err
}

func anthropicContent(result anthropicResponse) (string, json.RawMessage) {
	var text strings.Builder
	var calls []map[string]any
	for _, block := range result.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			calls = append(calls, map[string]any{"id": block.ID, "type": "function", "function": map[string]any{"name": block.Name, "arguments": string(block.Input)}})
		}
	}
	raw, _ := json.Marshal(calls)
	return text.String(), raw
}

func anthropicFinishReason(reason string) string {
	switch reason {
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	default:
		return "stop"
	}
}

func rawJSONValue(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return []any{}
	}
	var value any
	_ = json.Unmarshal(raw, &value)
	return value
}

func translateAnthropicStream(c *gin.Context, reader io.Reader, modelID, path string) error {
	c.Header("Content-Type", map[bool]string{true: "application/x-ndjson", false: "text/event-stream"}[strings.HasPrefix(path, "/api/")])
	scanner := bufio.NewScanner(&boundedReader{reader: reader, remaining: maxResponseBytes})
	scanner.Buffer(make([]byte, 64<<10), maxResponseBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event) != nil {
			continue
		}
		if event.Type == "content_block_delta" && event.Delta.Text != "" {
			if strings.HasPrefix(path, "/api/") {
				writeNativeChunk(c, modelID, path, event.Delta.Text, nil, false, "", true)
			} else {
				writeOpenAIAnthropicChunk(c, modelID, event.Delta.Text, "")
			}
		} else if event.Type == "message_delta" && event.Delta.StopReason != "" {
			reason := anthropicFinishReason(event.Delta.StopReason)
			if strings.HasPrefix(path, "/api/") {
				writeNativeChunk(c, modelID, path, "", nil, true, reason, true)
			} else {
				writeOpenAIAnthropicChunk(c, modelID, "", reason)
				_, _ = io.WriteString(c.Writer, "data: [DONE]\n\n")
			}
		}
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	return scanner.Err()
}

func writeOpenAIAnthropicChunk(c *gin.Context, model, text, reason string) {
	chunk := gin.H{"id": "chatcmpl-dz23-anthropic", "object": "chat.completion.chunk", "model": model, "choices": []gin.H{{"index": 0, "delta": gin.H{"content": text}, "finish_reason": nil}}}
	if reason != "" {
		chunk["choices"] = []gin.H{{"index": 0, "delta": gin.H{}, "finish_reason": reason}}
	}
	b, _ := json.Marshal(chunk)
	_, _ = io.WriteString(c.Writer, "data: "+string(b)+"\n\n")
}
