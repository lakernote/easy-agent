package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	core "github.com/lakernote/easy-agent/internal/agent"
)

type chatRequest struct {
	Model           string         `json:"model"`
	Messages        []chatMessage  `json:"messages"`
	Tools           []chatTool     `json:"tools,omitempty"`
	Temperature     float64        `json:"temperature,omitempty"`
	MaxTokens       int            `json:"max_tokens,omitempty"`
	ReasoningEffort string         `json:"reasoning_effort,omitempty"`
	Stream          bool           `json:"stream,omitempty"`
	StreamOptions   *streamOptions `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

type chatTool struct {
	Type     string           `json:"type"`
	Function chatToolFunction `json:"function"`
}

type chatToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type chatToolCall struct {
	ID       string               `json:"id,omitempty"`
	Type     string               `json:"type,omitempty"`
	Function chatToolCallFunction `json:"function"`
}

type chatToolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type chatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage chatUsage `json:"usage"`
}

type chatUsage struct {
	PromptTokens             int           `json:"prompt_tokens"`
	CompletionTokens         int           `json:"completion_tokens"`
	TotalTokens              int           `json:"total_tokens"`
	PromptTokensDetails      *tokenDetails `json:"prompt_tokens_details"`
	PromptCacheHitTokens     *int          `json:"prompt_cache_hit_tokens"`
	CacheReadInputTokens     *int          `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int          `json:"cache_creation_input_tokens"`
	CachedTokens             *int          `json:"cached_tokens"`
	CacheWriteTokens         *int          `json:"cache_write_tokens"`
}

type chatStreamDelta struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	ToolCalls []struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

type chatStreamChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta chatStreamDelta `json:"delta"`
	} `json:"choices"`
	Usage chatUsage `json:"usage"`
}

func (client *Client) generateChat(ctx context.Context, request core.Request) (core.Response, error) {
	payload := chatRequest{
		Model: request.Model, Messages: encodeChatMessages(request.Messages), Temperature: request.Temperature,
		MaxTokens: request.MaxOutputTokens, ReasoningEffort: request.ReasoningEffort,
	}
	for _, tool := range request.Tools {
		payload.Tools = append(payload.Tools, chatTool{Type: "function", Function: chatToolFunction{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters}})
	}
	if client.disableThinking {
		// OpenAI Chat Completions 和 Ollama 的兼容端点都使用 reasoning_effort；
		// think 属于 Ollama 原生 /api/chat，不应发送给 OpenAI 兼容服务。
		payload.ReasoningEffort = "none"
	}
	if request.OnTextDelta != nil {
		payload.Stream = true
		payload.StreamOptions = &streamOptions{IncludeUsage: true}
		return client.streamChat(ctx, payload, request.OnTextDelta)
	}
	return client.post(ctx, "/chat/completions", payload, string(ChatCompletions), decodeChatResponse)
}

// streamChat 读取 OpenAI Chat Completions 标准 SSE。每个可见文本增量立即交给
// 上层展示，最终仍组装成普通 Response，让 Runner、Tool Call 和 SQLite 无需分叉。
func (client *Client) streamChat(ctx context.Context, payload chatRequest, onTextDelta func(string)) (core.Response, error) {
	startedAt := time.Now()
	body, err := json.Marshal(payload)
	if err != nil {
		return core.Response{}, err
	}
	exchange := core.Exchange{Model: payload.Model, Protocol: string(ChatCompletions), Request: string(body)}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return core.Response{Exchange: exchange}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	if client.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+client.apiKey)
	}
	httpResponse, err := client.httpClient.Do(httpRequest)
	if err != nil {
		exchange.Duration = time.Since(startedAt)
		return core.Response{Exchange: exchange}, err
	}
	defer httpResponse.Body.Close()
	exchange.StatusCode = httpResponse.StatusCode
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(httpResponse.Body, 16*1024))
		exchange.Response = string(responseBody)
		exchange.Duration = time.Since(startedAt)
		return core.Response{Exchange: exchange}, fmt.Errorf("模型返回 %d: %s", httpResponse.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	// 一些 OpenAI 兼容网关会忽略 stream=true 并直接返回普通 JSON。
	// 这里自动降级，避免为了流式展示破坏原本可用的 Provider。
	if !strings.Contains(strings.ToLower(httpResponse.Header.Get("Content-Type")), "text/event-stream") {
		responseBody, readErr := io.ReadAll(io.LimitReader(httpResponse.Body, 8*1024*1024))
		exchange.Response = string(responseBody)
		exchange.Duration = time.Since(startedAt)
		if readErr != nil {
			return core.Response{Exchange: exchange}, readErr
		}
		result, decodeErr := decodeChatResponse(responseBody)
		result.Exchange = exchange
		result.Exchange.Usage = result.Usage
		if result.Message.Content != "" {
			onTextDelta(result.Message.Content)
		}
		return result, decodeErr
	}

	type partialToolCall struct {
		id        string
		name      string
		arguments strings.Builder
	}
	partialTools := map[int]*partialToolCall{}
	content := strings.Builder{}
	chunks := make([]json.RawMessage, 0, 32)
	chunkBytes := 0
	responseID, model := "", payload.Model
	usage := chatUsage{}
	scanner := bufio.NewScanner(httpResponse.Body)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := bytes.TrimSpace([]byte(strings.TrimPrefix(line, "data:")))
		if bytes.Equal(data, []byte("[DONE]")) {
			break
		}
		if !json.Valid(data) {
			return core.Response{Exchange: exchange}, errors.New("模型流返回了无效 JSON")
		}
		chunkBytes += len(data)
		if chunkBytes > 8*1024*1024 {
			return core.Response{Exchange: exchange}, errors.New("模型流响应超过 8 MiB")
		}
		chunks = append(chunks, append(json.RawMessage(nil), data...))
		var chunk chatStreamChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return core.Response{Exchange: exchange}, err
		}
		if chunk.ID != "" {
			responseID = chunk.ID
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 || chunk.Usage.TotalTokens > 0 {
			usage = chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				content.WriteString(choice.Delta.Content)
				onTextDelta(choice.Delta.Content)
			}
			for _, call := range choice.Delta.ToolCalls {
				partial := partialTools[call.Index]
				if partial == nil {
					partial = &partialToolCall{}
					partialTools[call.Index] = partial
				}
				if call.ID != "" {
					partial.id = call.ID
				}
				if call.Function.Name != "" {
					partial.name += call.Function.Name
				}
				partial.arguments.WriteString(argumentFragment(call.Function.Arguments))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		exchange.Duration = time.Since(startedAt)
		return core.Response{Exchange: exchange}, err
	}
	encodedChunks, _ := json.Marshal(map[string]any{"stream": true, "chunks": chunks})
	exchange.Response = string(encodedChunks)
	exchange.Duration = time.Since(startedAt)
	message := core.Message{Role: core.RoleAssistant, Content: content.String()}
	indexes := make([]int, 0, len(partialTools))
	for index := range partialTools {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		partial := partialTools[index]
		arguments := partial.arguments.String()
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		encodedArguments, _ := json.Marshal(arguments)
		normalized, err := normalizeArguments(encodedArguments)
		if err != nil {
			return core.Response{Exchange: exchange}, fmt.Errorf("工具 %s 参数无法解析: %w", partial.name, err)
		}
		message.ToolCalls = append(message.ToolCalls, core.ToolCall{ID: partial.id, Name: partial.name, Arguments: normalized})
	}
	normalizedUsage := usageFromChat(usage)
	exchange.Model = model
	exchange.Usage = normalizedUsage
	if strings.TrimSpace(message.Content) == "" && len(message.ToolCalls) == 0 {
		return core.Response{ID: responseID, Usage: normalizedUsage, Exchange: exchange}, core.ErrEmptyModelResponse
	}
	return core.Response{ID: responseID, Message: message, Usage: normalizedUsage, Exchange: exchange}, nil
}

func argumentFragment(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return string(raw)
}

func encodeChatMessages(messages []core.Message) []chatMessage {
	result := make([]chatMessage, 0, len(messages))
	for _, message := range messages {
		item := chatMessage{Role: string(message.Role), Content: message.Content, ToolCallID: message.ToolCallID, Name: message.Name}
		for _, call := range message.ToolCalls {
			arguments := call.Arguments
			if len(arguments) == 0 {
				arguments = json.RawMessage(`{}`)
			}
			encodedArguments, _ := json.Marshal(string(arguments))
			item.ToolCalls = append(item.ToolCalls, chatToolCall{ID: call.ID, Type: "function", Function: chatToolCallFunction{Name: call.Name, Arguments: encodedArguments}})
		}
		result = append(result, item)
	}
	return result
}

func decodeChatResponse(body []byte) (core.Response, error) {
	var payload chatResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return core.Response{}, err
	}
	if len(payload.Choices) == 0 {
		return core.Response{}, errors.New("模型没有返回 choices")
	}
	wire := payload.Choices[0].Message
	message := core.Message{Role: core.RoleAssistant, Content: wire.Content}
	for _, call := range wire.ToolCalls {
		arguments, err := normalizeArguments(call.Function.Arguments)
		if err != nil {
			return core.Response{}, fmt.Errorf("工具 %s 参数无法解析: %w", call.Function.Name, err)
		}
		message.ToolCalls = append(message.ToolCalls, core.ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: arguments})
	}
	usage := usageFromChat(payload.Usage)
	return core.Response{ID: payload.ID, Message: message, Usage: usage}, nil
}

func usageFromChat(payload chatUsage) core.Usage {
	var detailCached, detailWrite, detailCreated *int
	if payload.PromptTokensDetails != nil {
		detailCached = payload.PromptTokensDetails.CachedTokens
		detailWrite = payload.PromptTokensDetails.CacheWriteTokens
		detailCreated = payload.PromptTokensDetails.CacheCreationInputTokens
	}
	usage := core.Usage{
		InputTokens: payload.PromptTokens, OutputTokens: payload.CompletionTokens, TotalTokens: payload.TotalTokens,
		CachedInputTokens: optionalMax(payload.CachedTokens, detailCached, payload.PromptCacheHitTokens, payload.CacheReadInputTokens),
		CacheWriteTokens:  optionalMax(payload.CacheWriteTokens, detailWrite, detailCreated, payload.CacheCreationInputTokens),
		CacheReported: anyReported(
			payload.CachedTokens, detailCached, payload.PromptCacheHitTokens, payload.CacheReadInputTokens,
			payload.CacheWriteTokens, detailWrite, detailCreated, payload.CacheCreationInputTokens,
		),
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return usage
}

// normalizeArguments 同时兼容 OpenAI 返回的 JSON 字符串和 Ollama 返回的对象。
func normalizeArguments(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			text = "{}"
		}
		if !json.Valid([]byte(text)) {
			return nil, errors.New("arguments 字符串不是有效 JSON")
		}
		return json.RawMessage(text), nil
	}
	if !json.Valid(raw) {
		return nil, errors.New("arguments 不是有效 JSON")
	}
	return append(json.RawMessage(nil), raw...), nil
}
