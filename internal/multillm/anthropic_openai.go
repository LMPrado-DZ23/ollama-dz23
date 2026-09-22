package multillm

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// forwardAnthropicToOpenAI translates the Anthropic Messages contract used by
// Claude Code and Claude Desktop into OpenAI Chat Completions. It is opt-in per
// provider: a provider must explicitly advertise /v1/messages in its paths.
func (g *Gateway) forwardAnthropicToOpenAI(c *gin.Context, provider Provider, model Model, envelope map[string]json.RawMessage) error {
	body, stream, err := anthropicToOpenAIRequest(model.UpstreamID, envelope)
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
		return translateOpenAIAnthropicStream(c, resp.Body, model.ID)
	}
	return translateOpenAIAnthropicResponse(c, resp.Body, model.ID)
}

func anthropicToOpenAIRequest(model string, envelope map[string]json.RawMessage) ([]byte, bool, error) {
	request := map[string]any{
		"model":  model,
		"stream": false,
	}
	var stream bool
	if raw := envelope["stream"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &stream); err != nil {
			return nil, false, fmt.Errorf("invalid stream: %w", err)
		}
	}
	request["stream"] = stream

	if raw := envelope["max_tokens"]; len(raw) > 0 {
		var value int
		if err := json.Unmarshal(raw, &value); err == nil && value > 0 {
			request["max_tokens"] = value
		}
	}
	for _, key := range []string{"temperature", "top_p", "stop_sequences"} {
		if raw := envelope[key]; len(raw) > 0 {
			var value any
			if json.Unmarshal(raw, &value) == nil {
				if key == "stop_sequences" {
					key = "stop"
				}
				request[key] = value
			}
		}
	}

	messages, err := anthropicMessagesToOpenAI(envelope["messages"], envelope["system"])
	if err != nil {
		return nil, false, err
	}
	request["messages"] = messages

	if raw := envelope["tools"]; len(raw) > 0 {
		tools, err := anthropicToolsToOpenAI(raw)
		if err != nil {
			return nil, false, err
		}
		request["tools"] = tools
	}
	if raw := envelope["tool_choice"]; len(raw) > 0 {
		choice, err := anthropicToolChoiceToOpenAI(raw)
		if err != nil {
			return nil, false, err
		}
		if choice != nil {
			request["tool_choice"] = choice
		}
	}
	body, err := json.Marshal(request)
	return body, stream, err
}

func anthropicMessagesToOpenAI(rawMessages, rawSystem json.RawMessage) ([]map[string]any, error) {
	messages := make([]map[string]any, 0)
	if len(rawSystem) > 0 && string(rawSystem) != "null" {
		content, err := anthropicContentToOpenAI(rawSystem, false)
		if err != nil {
			return nil, fmt.Errorf("convert Anthropic system message: %w", err)
		}
		if content != nil {
			messages = append(messages, map[string]any{"role": "system", "content": content})
		}
	}
	var source []map[string]any
	if err := json.Unmarshal(rawMessages, &source); err != nil {
		return nil, errors.New("Anthropic adapter requires messages")
	}
	for _, message := range source {
		role, _ := message["role"].(string)
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "system" {
			content, err := anthropicContentToOpenAI(rawMessage(message["content"]), false)
			if err != nil {
				return nil, err
			}
			messages = append(messages, map[string]any{"role": "system", "content": content})
			continue
		}
		if role != "assistant" {
			role = "user"
		}
		content, toolCalls, toolResults, err := anthropicMessageParts(rawMessage(message["content"]), role)
		if err != nil {
			return nil, err
		}
		if len(toolResults) > 0 {
			for _, result := range toolResults {
				messages = append(messages, result)
			}
			if content == nil && len(toolCalls) == 0 {
				continue
			}
		}
		converted := map[string]any{"role": role}
		if content != nil {
			converted["content"] = content
		} else {
			converted["content"] = ""
		}
		if len(toolCalls) > 0 {
			converted["tool_calls"] = toolCalls
		}
		messages = append(messages, converted)
	}
	return messages, nil
}

func anthropicMessageParts(raw json.RawMessage, role string) (any, []map[string]any, []map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, nil, nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil, nil, nil
	}
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid Anthropic content: %w", err)
	}
	contentParts := make([]any, 0)
	toolCalls := make([]map[string]any, 0)
	toolResults := make([]map[string]any, 0)
	for _, block := range blocks {
		typeName, _ := block["type"].(string)
		switch typeName {
		case "text":
			if value, ok := block["text"].(string); ok {
				if role == "user" {
					contentParts = append(contentParts, map[string]any{"type": "text", "text": value})
				} else {
					contentParts = append(contentParts, value)
				}
			}
		case "image":
			if image := anthropicImageToOpenAI(block); image != nil {
				contentParts = append(contentParts, image)
			}
		case "tool_use":
			if role != "assistant" {
				continue
			}
			name, _ := block["name"].(string)
			id, _ := block["id"].(string)
			input := block["input"]
			arguments, err := json.Marshal(input)
			if err != nil {
				return nil, nil, nil, err
			}
			toolCalls = append(toolCalls, map[string]any{
				"id":   id,
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": string(arguments),
				},
			})
		case "tool_result":
			if role != "user" {
				continue
			}
			id, _ := block["tool_use_id"].(string)
			result := block["content"]
			if result == nil {
				result = ""
			}
			toolResults = append(toolResults, map[string]any{"role": "tool", "tool_call_id": id, "content": anthropicToolResultText(result)})
		}
	}
	var content any
	if len(contentParts) == 1 {
		content = contentParts[0]
	} else if len(contentParts) > 1 {
		content = contentParts
	} else if len(toolCalls) == 0 {
		content = ""
	}
	return content, toolCalls, toolResults, nil
}

func anthropicContentToOpenAI(raw json.RawMessage, _ bool) (any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	parts := make([]any, 0, len(blocks))
	for _, block := range blocks {
		switch block["type"] {
		case "text":
			if value, ok := block["text"].(string); ok {
				parts = append(parts, value)
			}
		case "image":
			if image := anthropicImageToOpenAI(block); image != nil {
				parts = append(parts, image)
			}
		}
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	return parts, nil
}

func anthropicImageToOpenAI(block map[string]any) map[string]any {
	source, ok := block["source"].(map[string]any)
	if !ok {
		return nil
	}
	mediaType, _ := source["media_type"].(string)
	data, _ := source["data"].(string)
	if mediaType == "" || data == "" {
		return nil
	}
	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:" + mediaType + ";base64," + data}}
}

func anthropicToolResultText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func anthropicToolsToOpenAI(raw json.RawMessage) ([]map[string]any, error) {
	var source []map[string]any
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, fmt.Errorf("invalid Anthropic tools: %w", err)
	}
	tools := make([]map[string]any, 0, len(source))
	for _, tool := range source {
		name, _ := tool["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		function := map[string]any{"name": name}
		if description, ok := tool["description"].(string); ok && description != "" {
			function["description"] = description
		}
		if schema := tool["input_schema"]; schema != nil {
			function["parameters"] = schema
		}
		tools = append(tools, map[string]any{"type": "function", "function": function})
	}
	return tools, nil
}

func anthropicToolChoiceToOpenAI(raw json.RawMessage) (any, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if choice, ok := value.(string); ok {
		switch choice {
		case "auto", "none":
			return choice, nil
		case "any":
			return "required", nil
		}
	}
	if object, ok := value.(map[string]any); ok {
		if name, ok := object["name"].(string); ok && name != "" {
			return map[string]any{"type": "function", "function": map[string]any{"name": name}}, nil
		}
	}
	return nil, nil
}

func rawMessage(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

type openAIAnthropicCompletion struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func translateOpenAIAnthropicResponse(c *gin.Context, reader io.Reader, modelID string) error {
	body, err := io.ReadAll(io.LimitReader(reader, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxResponseBytes {
		return errResponseLimit
	}
	var completion openAIAnthropicCompletion
	if err := json.Unmarshal(body, &completion); err != nil {
		return err
	}
	if len(completion.Choices) == 0 {
		return errors.New("provider response contained no choices")
	}
	choice := completion.Choices[0]
	content := make([]map[string]any, 0, 1+len(choice.Message.ToolCalls))
	if choice.Message.Content != "" {
		content = append(content, map[string]any{"type": "text", "text": choice.Message.Content})
	}
	for _, call := range choice.Message.ToolCalls {
		input := map[string]any{}
		if strings.TrimSpace(call.Function.Arguments) != "" {
			if err := json.Unmarshal([]byte(call.Function.Arguments), &input); err != nil {
				return fmt.Errorf("provider returned invalid tool arguments: %w", err)
			}
		}
		content = append(content, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Function.Name, "input": input})
	}
	stopReason := openAIFinishReason(choice.FinishReason)
	response := map[string]any{
		"id":            completion.ID,
		"type":          "message",
		"role":          "assistant",
		"model":         modelID,
		"content":       content,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  completion.Usage.PromptTokens,
			"output_tokens": completion.Usage.CompletionTokens,
		},
	}
	c.Header("Content-Type", "application/json")
	c.JSON(http.StatusOK, response)
	return nil
}

func openAIFinishReason(reason string) string {
	switch reason {
	case "tool_calls", "function_call":
		return "tool_use"
	case "length", "max_tokens":
		return "max_tokens"
	default:
		return "end_turn"
	}
}

func translateOpenAIAnthropicStream(c *gin.Context, reader io.Reader, modelID string) error {
	c.Header("Content-Type", "text/event-stream")
	messageID := "msg_dz23_" + fmt.Sprint(time.Now().UnixNano())
	writeAnthropicSSE(c, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": messageID, "type": "message", "role": "assistant", "model": modelID,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})
	index := -1
	toolIndexes := make(map[int]int)
	scanner := bufio.NewScanner(&boundedReader{reader: reader, remaining: maxResponseBytes})
	scanner.Buffer(make([]byte, 64<<10), maxResponseBytes)
	finishReason := "end_turn"
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				FinishReason string `json:"finish_reason"`
				Delta        struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			finishReason = openAIFinishReason(choice.FinishReason)
		}
		if choice.Delta.Content != "" {
			if index == -1 {
				index = 0
				writeAnthropicSSE(c, "content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "text", "text": ""}})
			}
			writeAnthropicSSE(c, "content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "text_delta", "text": choice.Delta.Content}})
		}
		for _, call := range choice.Delta.ToolCalls {
			blockIndex, exists := toolIndexes[call.Index]
			if !exists {
				blockIndex = len(toolIndexes)
				if index == -1 {
					index = blockIndex
				}
				toolIndexes[call.Index] = blockIndex
				writeAnthropicSSE(c, "content_block_start", map[string]any{"type": "content_block_start", "index": blockIndex, "content_block": map[string]any{"type": "tool_use", "id": call.ID, "name": call.Function.Name, "input": map[string]any{}}})
			}
			if call.Function.Arguments != "" {
				writeAnthropicSSE(c, "content_block_delta", map[string]any{"type": "content_block_delta", "index": blockIndex, "delta": map[string]any{"type": "input_json_delta", "partial_json": call.Function.Arguments}})
			}
		}
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if index == -1 {
		index = 0
		writeAnthropicSSE(c, "content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "text", "text": ""}})
	}
	for blockIndex := 0; blockIndex <= index; blockIndex++ {
		writeAnthropicSSE(c, "content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex})
	}
	writeAnthropicSSE(c, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": finishReason, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 0}})
	writeAnthropicSSE(c, "message_stop", map[string]any{"type": "message_stop"})
	return nil
}

func writeAnthropicSSE(c *gin.Context, event string, payload map[string]any) {
	body, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, body)
}
