// Package ollama adapts Ollama's native /api/chat protocol to the provider-
// neutral agent runtime. It intentionally does not route through Ollama's
// OpenAI compatibility endpoint, so native usage, tool and stop semantics stay
// available to EasyAgent.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	core "github.com/lakernote/easy-agent/internal/agent"
)

const Protocol = "ollama_chat"

type Config struct {
	BaseURL         string
	APIKey          string
	HTTPClient      *http.Client
	Timeout         time.Duration
	DisableThinking bool
	ContextWindow   int
}

type Client struct {
	baseURL         string
	apiKey          string
	httpClient      *http.Client
	disableThinking bool
	contextWindow   int
	requestSequence atomic.Uint64
}

func New(config Config) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	// The previous EasyAgent default pointed at Ollama's compatibility /v1.
	// Accept that saved value while switching the wire protocol to native chat.
	baseURL = strings.TrimSuffix(baseURL, "/v1")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("Ollama 地址必须是有效的 http(s) URL")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		timeout := config.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Minute
		}
		httpClient = &http.Client{Timeout: timeout}
	}
	return &Client{baseURL: baseURL, apiKey: strings.TrimSpace(config.APIKey), httpClient: httpClient, disableThinking: config.DisableThinking, contextWindow: config.ContextWindow}, nil
}

func (client *Client) Capabilities() core.ModelCapabilities {
	return core.ModelCapabilities{
		Provider: "ollama", Protocol: Protocol, Streaming: true, NativeTools: true,
		StructuredToolResults: false, ServerContinuation: false, MultimodalInput: true,
	}
}

type chatRequest struct {
	Model    string         `json:"model"`
	Messages []chatMessage  `json:"messages"`
	Tools    []tool         `json:"tools,omitempty"`
	Stream   bool           `json:"stream"`
	Think    any            `json:"think,omitempty"`
	Options  map[string]any `json:"options,omitempty"`
}

type chatMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content,omitempty"`
	Thinking  string     `json:"thinking,omitempty"`
	Images    []string   `json:"images,omitempty"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

type tool struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  map[string]any  `json:"parameters,omitempty"`
	Arguments   json.RawMessage `json:"arguments,omitempty"`
}

type toolCall struct {
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function toolFunction `json:"function"`
}

type chatResponse struct {
	Model                 string      `json:"model"`
	Message               chatMessage `json:"message"`
	Done                  bool        `json:"done"`
	DoneReason            string      `json:"done_reason"`
	PromptEvalCount       int         `json:"prompt_eval_count"`
	PromptEvalCachedCount *int        `json:"prompt_eval_cached_count"`
	EvalCount             int         `json:"eval_count"`
}

type streamTrace struct {
	Stream        bool              `json:"stream"`
	Transport     string            `json:"transport"`
	FinalResponse chatResponse      `json:"final_response"`
	RawEvents     []json.RawMessage `json:"raw_events"`
}

func (client *Client) Generate(ctx context.Context, request core.Request) (core.Response, error) {
	if strings.TrimSpace(request.Model) == "" {
		return core.Response{}, errors.New("请先配置模型名称")
	}
	payload := chatRequest{Model: request.Model, Messages: encodeMessages(request.Messages), Stream: request.OnTextDelta != nil}
	requestSequence := client.requestSequence.Add(1)
	selectedTools := request.Tools
	if request.ToolChoice.Mode == core.ToolChoiceNone {
		selectedTools = nil
	} else if request.ToolChoice.Name != "" {
		selectedTools = filterTools(selectedTools, request.ToolChoice.Name)
	}
	for _, spec := range selectedTools {
		payload.Tools = append(payload.Tools, tool{Type: "function", Function: toolFunction{Name: spec.Name, Description: spec.Description, Parameters: spec.Parameters}})
	}
	if client.disableThinking {
		payload.Think = false
	}
	if request.MaxOutputTokens > 0 || client.contextWindow > 0 {
		payload.Options = map[string]any{}
		if request.MaxOutputTokens > 0 {
			payload.Options["num_predict"] = request.MaxOutputTokens
		}
		if client.contextWindow > 0 {
			payload.Options["num_ctx"] = client.contextWindow
		}
	}
	if payload.Stream {
		return client.streamChat(ctx, payload, requestSequence, request.OnTextDelta)
	}
	startedAt := time.Now()
	body, err := json.Marshal(payload)
	if err != nil {
		return core.Response{}, err
	}
	exchange := core.Exchange{
		Model: request.Model, Protocol: Protocol, Request: string(body), HistoryMode: "full_history",
		RequestMessages: len(payload.Messages), ToolDefinitions: len(payload.Tools),
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return core.Response{Exchange: exchange}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	client.authorize(httpRequest)
	httpResponse, err := client.httpClient.Do(httpRequest)
	if err != nil {
		exchange.Duration = time.Since(startedAt)
		return core.Response{Exchange: exchange}, err
	}
	defer httpResponse.Body.Close()
	limit := int64(8 * 1024 * 1024)
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		limit = 32 * 1024
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(httpResponse.Body, limit))
	exchange.Response = string(responseBody)
	exchange.StatusCode = httpResponse.StatusCode
	exchange.Duration = time.Since(startedAt)
	if readErr != nil {
		return core.Response{Exchange: exchange}, readErr
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return core.Response{Exchange: exchange}, &core.ModelError{StatusCode: httpResponse.StatusCode, Message: fmt.Sprintf("Ollama 返回 %d: %s", httpResponse.StatusCode, strings.TrimSpace(string(responseBody)))}
	}
	var wire chatResponse
	if err := json.Unmarshal(responseBody, &wire); err != nil {
		return core.Response{Exchange: exchange}, err
	}
	response, err := normalizeResponse(wire, requestSequence)
	if err != nil {
		return core.Response{Exchange: exchange}, err
	}
	response.Exchange = exchange
	response.Exchange.Usage = response.Usage
	response.Exchange.StopReason = response.StopReason
	response.Exchange.IncompleteReason = response.IncompleteReason
	return response, nil
}

func (client *Client) streamChat(ctx context.Context, payload chatRequest, requestSequence uint64, onTextDelta func(string)) (core.Response, error) {
	startedAt := time.Now()
	body, err := json.Marshal(payload)
	if err != nil {
		return core.Response{}, err
	}
	exchange := core.Exchange{
		Model: payload.Model, Protocol: Protocol, Request: string(body), HistoryMode: "full_history",
		RequestMessages: len(payload.Messages), ToolDefinitions: len(payload.Tools),
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return core.Response{Exchange: exchange}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/x-ndjson")
	client.authorize(httpRequest)
	httpResponse, err := client.httpClient.Do(httpRequest)
	if err != nil {
		exchange.Duration = time.Since(startedAt)
		return core.Response{Exchange: exchange}, err
	}
	defer httpResponse.Body.Close()
	exchange.StatusCode = httpResponse.StatusCode
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(httpResponse.Body, 32*1024))
		exchange.Response = string(responseBody)
		exchange.Duration = time.Since(startedAt)
		return core.Response{Exchange: exchange}, &core.ModelError{StatusCode: httpResponse.StatusCode, Message: fmt.Sprintf("Ollama 返回 %d: %s", httpResponse.StatusCode, strings.TrimSpace(string(responseBody)))}
	}
	var final chatResponse
	var content, thinking strings.Builder
	chunks := make([]json.RawMessage, 0, 32)
	totalBytes := 0
	recordPartialTrace := func() {
		final.Message.Role = "assistant"
		final.Message.Content = content.String()
		final.Message.Thinking = thinking.String()
		trace, _ := json.Marshal(streamTrace{Stream: true, Transport: "ndjson", FinalResponse: final, RawEvents: chunks})
		exchange.Response = string(trace)
		exchange.Duration = time.Since(startedAt)
	}
	scanner := bufio.NewScanner(httpResponse.Body)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		data := bytes.TrimSpace(scanner.Bytes())
		if len(data) == 0 {
			continue
		}
		totalBytes += len(data)
		if totalBytes > 8*1024*1024 {
			recordPartialTrace()
			return core.Response{Exchange: exchange}, errors.New("Ollama 流响应超过 8 MiB")
		}
		if !json.Valid(data) {
			recordPartialTrace()
			return core.Response{Exchange: exchange}, errors.New("Ollama 流返回了无效 JSON")
		}
		chunks = append(chunks, append(json.RawMessage(nil), data...))
		var chunk chatResponse
		if err := json.Unmarshal(data, &chunk); err != nil {
			recordPartialTrace()
			return core.Response{Exchange: exchange}, err
		}
		if chunk.Model != "" {
			final.Model = chunk.Model
		}
		if chunk.Message.Content != "" {
			content.WriteString(chunk.Message.Content)
			onTextDelta(chunk.Message.Content)
		}
		thinking.WriteString(chunk.Message.Thinking)
		final.Message.ToolCalls = append(final.Message.ToolCalls, chunk.Message.ToolCalls...)
		if chunk.Done {
			final.Done = true
			final.DoneReason = chunk.DoneReason
			final.PromptEvalCount = chunk.PromptEvalCount
			final.PromptEvalCachedCount = chunk.PromptEvalCachedCount
			final.EvalCount = chunk.EvalCount
		}
	}
	if err := scanner.Err(); err != nil {
		recordPartialTrace()
		return core.Response{Exchange: exchange}, err
	}
	final.Message.Role = "assistant"
	final.Message.Content = content.String()
	final.Message.Thinking = thinking.String()
	trace, _ := json.Marshal(streamTrace{Stream: true, Transport: "ndjson", FinalResponse: final, RawEvents: chunks})
	exchange.Response = string(trace)
	exchange.Duration = time.Since(startedAt)
	response, err := normalizeResponse(final, requestSequence)
	if err != nil {
		return core.Response{Exchange: exchange}, err
	}
	response.Exchange = exchange
	response.Exchange.Usage = response.Usage
	response.Exchange.StopReason = response.StopReason
	response.Exchange.IncompleteReason = response.IncompleteReason
	return response, nil
}

func normalizeResponse(wire chatResponse, requestSequence uint64) (core.Response, error) {
	message := core.Message{Role: core.RoleAssistant, Content: wire.Message.Content, Reasoning: wire.Message.Thinking}
	for index, call := range wire.Message.ToolCalls {
		arguments := call.Function.Arguments
		if len(arguments) == 0 {
			arguments = json.RawMessage(`{}`)
		}
		if !json.Valid(arguments) {
			return core.Response{}, fmt.Errorf("Ollama 工具 %s 参数不是有效 JSON", call.Function.Name)
		}
		id := strings.TrimSpace(call.ID)
		if id == "" {
			id = fmt.Sprintf("ollama_call_%d_%d", requestSequence, index+1)
		}
		message.ToolCalls = append(message.ToolCalls, core.ToolCall{ID: id, Name: call.Function.Name, Arguments: append(json.RawMessage(nil), arguments...)})
	}
	usage := core.Usage{InputTokens: wire.PromptEvalCount, OutputTokens: wire.EvalCount, TotalTokens: wire.PromptEvalCount + wire.EvalCount}
	if wire.PromptEvalCachedCount != nil {
		usage.CachedInputTokens = *wire.PromptEvalCachedCount
		usage.CacheReported = true
	}
	stopReason, incompleteReason := normalizeStopReason(wire.Done, wire.DoneReason, len(message.ToolCalls) > 0)
	return core.Response{Message: message, Usage: usage, StopReason: stopReason, IncompleteReason: incompleteReason}, nil
}

func (client *Client) authorize(request *http.Request) {
	if client.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+client.apiKey)
	}
}

func encodeMessages(messages []core.Message) []chatMessage {
	result := make([]chatMessage, 0, len(messages))
	for _, message := range messages {
		content := message.Content
		if message.Role == core.RoleTool && message.ToolResult != nil {
			content = message.ToolResult.ModelText()
		}
		wire := chatMessage{Role: string(message.Role), Content: content, Thinking: message.Reasoning, ToolName: message.Name}
		for _, block := range message.Parts {
			switch block.Type {
			case "text":
				wire.Content += "\n" + block.Text
			case "json":
				wire.Content += "\n" + string(block.JSON)
			case "image":
				wire.Images = append(wire.Images, base64.StdEncoding.EncodeToString(block.Data))
			default:
				encoded, _ := json.Marshal(block)
				wire.Content += "\n" + string(encoded)
			}
		}
		for _, attachment := range message.Attachments {
			if attachment.Kind == "image" {
				wire.Images = append(wire.Images, base64.StdEncoding.EncodeToString(attachment.Data))
				continue
			}
			wire.Content += fmt.Sprintf("\n\n<attachment name=%q type=%q>\n%s\n</attachment>", attachment.Name, attachment.MIMEType, string(attachment.Data))
		}
		for _, call := range message.ToolCalls {
			arguments := call.Arguments
			if len(arguments) == 0 {
				arguments = json.RawMessage(`{}`)
			}
			wire.ToolCalls = append(wire.ToolCalls, toolCall{ID: call.ID, Type: "function", Function: toolFunction{Name: call.Name, Arguments: arguments}})
		}
		result = append(result, wire)
	}
	return result
}

func filterTools(tools []core.ToolSpec, name string) []core.ToolSpec {
	for _, spec := range tools {
		if spec.Name == name {
			return []core.ToolSpec{spec}
		}
	}
	return nil
}

func normalizeStopReason(done bool, reason string, hasTools bool) (core.StopReason, string) {
	if hasTools && done {
		return core.StopReasonToolUse, ""
	}
	if !done {
		return core.StopReasonIncomplete, "Ollama 响应未完成"
	}
	normalized := strings.ToLower(strings.TrimSpace(reason))
	switch normalized {
	case "", "stop", "end_turn":
		return core.StopReasonStop, ""
	case "length", "max_tokens":
		return core.StopReasonLength, normalized
	case "cancelled", "canceled":
		return core.StopReasonInterrupted, normalized
	default:
		return core.StopReasonIncomplete, normalized
	}
}
