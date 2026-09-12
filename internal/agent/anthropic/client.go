// Package anthropic adapts the native Anthropic Messages API. Tool results are
// emitted as tool_result content blocks with explicit is_error semantics rather
// than flattened into an OpenAI-shaped message.
package anthropic

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
	"sort"
	"strings"
	"time"

	core "github.com/lakernote/easy-agent/internal/agent"
)

const Protocol = "anthropic_messages"

type Config struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	Timeout    time.Duration
}

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func New(config Config) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("Anthropic 地址必须是有效的 http(s) URL")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		timeout := config.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Minute
		}
		httpClient = &http.Client{Timeout: timeout}
	}
	return &Client{baseURL: baseURL, apiKey: strings.TrimSpace(config.APIKey), httpClient: httpClient}, nil
}

func (client *Client) Capabilities() core.ModelCapabilities {
	return core.ModelCapabilities{
		Provider: "anthropic", Protocol: Protocol, Streaming: true, NativeTools: true,
		StructuredToolResults: true, ServerContinuation: false, MultimodalInput: true,
	}
}

type requestBody struct {
	Model       string    `json:"model"`
	System      string    `json:"system,omitempty"`
	Messages    []message `json:"messages"`
	Tools       []tool    `json:"tools,omitempty"`
	ToolChoice  any       `json:"tool_choice,omitempty"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature *float64  `json:"temperature,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
}

type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   any             `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Source    any             `json:"source,omitempty"`
}

type responseBody struct {
	ID         string         `json:"id"`
	Model      string         `json:"model"`
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      anthropicUsage `json:"usage"`
}

type anthropicUsage struct {
	InputTokens              int  `json:"input_tokens"`
	OutputTokens             int  `json:"output_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
}

type streamEvent struct {
	Type         string         `json:"type"`
	Message      responseBody   `json:"message"`
	Index        int            `json:"index"`
	ContentBlock contentBlock   `json:"content_block"`
	Usage        anthropicUsage `json:"usage"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type streamBlock struct {
	block contentBlock
	text  strings.Builder
	input strings.Builder
}

type streamTrace struct {
	Stream        bool              `json:"stream"`
	Transport     string            `json:"transport"`
	FinalResponse responseBody      `json:"final_response"`
	RawEvents     []json.RawMessage `json:"raw_events"`
}

func (client *Client) Generate(ctx context.Context, request core.Request) (core.Response, error) {
	if strings.TrimSpace(request.Model) == "" {
		return core.Response{}, errors.New("请先配置模型名称")
	}
	system, messages := encodeMessages(request.Messages)
	maxTokens := request.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	payload := requestBody{
		Model: request.Model, System: system, Messages: messages, MaxTokens: maxTokens,
		ToolChoice: encodeToolChoice(request.ToolChoice), Stream: request.OnTextDelta != nil,
	}
	if request.Temperature != 0 {
		value := request.Temperature
		payload.Temperature = &value
	}
	selectedTools := request.Tools
	if request.ToolChoice.Mode == core.ToolChoiceNone {
		selectedTools = nil
	} else if request.ToolChoice.Name != "" {
		selectedTools = filterTools(selectedTools, request.ToolChoice.Name)
	}
	for _, spec := range selectedTools {
		payload.Tools = append(payload.Tools, tool{Name: spec.Name, Description: spec.Description, InputSchema: spec.Parameters})
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
	if payload.Stream {
		return client.streamMessages(ctx, body, exchange, startedAt, request.OnTextDelta)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return core.Response{Exchange: exchange}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("anthropic-version", "2023-06-01")
	if client.apiKey != "" {
		httpRequest.Header.Set("x-api-key", client.apiKey)
	}
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
	responseBytes, readErr := io.ReadAll(io.LimitReader(httpResponse.Body, limit))
	exchange.Response = string(responseBytes)
	exchange.StatusCode = httpResponse.StatusCode
	exchange.Duration = time.Since(startedAt)
	if readErr != nil {
		return core.Response{Exchange: exchange}, readErr
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return core.Response{Exchange: exchange}, &core.ModelError{StatusCode: httpResponse.StatusCode, Message: fmt.Sprintf("Anthropic 返回 %d: %s", httpResponse.StatusCode, strings.TrimSpace(string(responseBytes)))}
	}
	var wire responseBody
	if err := json.Unmarshal(responseBytes, &wire); err != nil {
		return core.Response{Exchange: exchange}, err
	}
	response, decodeErr := decodeResponse(wire)
	response.Exchange = exchange
	response.Exchange.Model = wire.Model
	response.Exchange.Usage = response.Usage
	response.Exchange.StopReason = response.StopReason
	response.Exchange.IncompleteReason = response.IncompleteReason
	return response, decodeErr
}

func (client *Client) streamMessages(ctx context.Context, body []byte, exchange core.Exchange, startedAt time.Time, onTextDelta func(string)) (core.Response, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return core.Response{Exchange: exchange}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	httpRequest.Header.Set("anthropic-version", "2023-06-01")
	if client.apiKey != "" {
		httpRequest.Header.Set("x-api-key", client.apiKey)
	}
	httpResponse, err := client.httpClient.Do(httpRequest)
	if err != nil {
		exchange.Duration = time.Since(startedAt)
		return core.Response{Exchange: exchange}, err
	}
	defer httpResponse.Body.Close()
	exchange.StatusCode = httpResponse.StatusCode
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		responseBytes, _ := io.ReadAll(io.LimitReader(httpResponse.Body, 32*1024))
		exchange.Response = string(responseBytes)
		exchange.Duration = time.Since(startedAt)
		return core.Response{Exchange: exchange}, &core.ModelError{StatusCode: httpResponse.StatusCode, Message: fmt.Sprintf("Anthropic 返回 %d: %s", httpResponse.StatusCode, strings.TrimSpace(string(responseBytes)))}
	}

	var final responseBody
	blocks := map[int]*streamBlock{}
	chunks := make([]json.RawMessage, 0, 32)
	totalBytes := 0
	terminal := false
	recordPartialTrace := func() {
		trace, _ := json.Marshal(streamTrace{Stream: true, Transport: "sse", FinalResponse: final, RawEvents: chunks})
		exchange.Response = string(trace)
		exchange.Duration = time.Since(startedAt)
	}
	scanner := bufio.NewScanner(httpResponse.Body)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(data) == 0 {
			continue
		}
		totalBytes += len(data)
		if totalBytes > 8*1024*1024 {
			recordPartialTrace()
			return core.Response{Exchange: exchange}, errors.New("Anthropic 流响应超过 8 MiB")
		}
		if !json.Valid(data) {
			recordPartialTrace()
			return core.Response{Exchange: exchange}, errors.New("Anthropic 流返回了无效 JSON")
		}
		chunks = append(chunks, append(json.RawMessage(nil), data...))
		var event streamEvent
		if err := json.Unmarshal(data, &event); err != nil {
			recordPartialTrace()
			return core.Response{Exchange: exchange}, err
		}
		switch event.Type {
		case "message_start":
			final.ID = event.Message.ID
			final.Model = event.Message.Model
			mergeUsage(&final.Usage, event.Message.Usage)
		case "content_block_start":
			state := &streamBlock{block: event.ContentBlock}
			if event.ContentBlock.Text != "" {
				state.text.WriteString(event.ContentBlock.Text)
				onTextDelta(event.ContentBlock.Text)
			}
			blocks[event.Index] = state
		case "content_block_delta":
			state := blocks[event.Index]
			if state == nil {
				state = &streamBlock{}
				blocks[event.Index] = state
			}
			switch event.Delta.Type {
			case "text_delta":
				state.text.WriteString(event.Delta.Text)
				onTextDelta(event.Delta.Text)
			case "input_json_delta":
				state.input.WriteString(event.Delta.PartialJSON)
			}
		case "message_delta":
			final.StopReason = event.Delta.StopReason
			mergeUsage(&final.Usage, event.Usage)
		case "message_stop":
			terminal = true
		case "error":
			message := "Anthropic 流返回错误"
			if event.Error != nil && strings.TrimSpace(event.Error.Message) != "" {
				message = event.Error.Message
			}
			recordPartialTrace()
			return core.Response{Exchange: exchange}, &core.ModelError{StatusCode: http.StatusInternalServerError, Message: message}
		}
	}
	if err := scanner.Err(); err != nil {
		recordPartialTrace()
		return core.Response{Exchange: exchange}, err
	}
	indexes := make([]int, 0, len(blocks))
	for index := range blocks {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		state := blocks[index]
		block := state.block
		if block.Type == "text" {
			block.Text = state.text.String()
		}
		if block.Type == "tool_use" && state.input.Len() > 0 {
			block.Input = json.RawMessage(state.input.String())
		}
		final.Content = append(final.Content, block)
	}
	if !terminal || strings.TrimSpace(final.StopReason) == "" {
		final.StopReason = "stream_incomplete"
	}
	trace, _ := json.Marshal(streamTrace{Stream: true, Transport: "sse", FinalResponse: final, RawEvents: chunks})
	exchange.Response = string(trace)
	exchange.Duration = time.Since(startedAt)
	response, decodeErr := decodeResponse(final)
	response.Exchange = exchange
	response.Exchange.Model = final.Model
	response.Exchange.Usage = response.Usage
	response.Exchange.StopReason = response.StopReason
	response.Exchange.IncompleteReason = response.IncompleteReason
	return response, decodeErr
}

func mergeUsage(target *anthropicUsage, value anthropicUsage) {
	if value.InputTokens != 0 {
		target.InputTokens = value.InputTokens
	}
	if value.OutputTokens != 0 {
		target.OutputTokens = value.OutputTokens
	}
	if value.CacheReadInputTokens != nil {
		target.CacheReadInputTokens = value.CacheReadInputTokens
	}
	if value.CacheCreationInputTokens != nil {
		target.CacheCreationInputTokens = value.CacheCreationInputTokens
	}
}

func decodeResponse(wire responseBody) (core.Response, error) {
	result := core.Message{Role: core.RoleAssistant}
	for _, block := range wire.Content {
		switch block.Type {
		case "text":
			result.Content += block.Text
		case "tool_use":
			arguments := block.Input
			if len(arguments) == 0 {
				arguments = json.RawMessage(`{}`)
			}
			if !json.Valid(arguments) {
				return core.Response{}, fmt.Errorf("Anthropic 工具 %s 参数不是有效 JSON", block.Name)
			}
			result.ToolCalls = append(result.ToolCalls, core.ToolCall{ID: block.ID, Name: block.Name, Arguments: append(json.RawMessage(nil), arguments...)})
		}
	}
	usage := core.Usage{
		InputTokens: wire.Usage.InputTokens, OutputTokens: wire.Usage.OutputTokens,
		CachedInputTokens: optionalInt(wire.Usage.CacheReadInputTokens), CacheWriteTokens: optionalInt(wire.Usage.CacheCreationInputTokens),
		CacheReported: wire.Usage.CacheReadInputTokens != nil || wire.Usage.CacheCreationInputTokens != nil,
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	stopReason, incompleteReason := normalizeStopReason(wire.StopReason, len(result.ToolCalls) > 0)
	return core.Response{ID: wire.ID, Message: result, Usage: usage, StopReason: stopReason, IncompleteReason: incompleteReason}, nil
}

func optionalInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func encodeMessages(messages []core.Message) (string, []message) {
	systems := make([]string, 0)
	result := make([]message, 0, len(messages))
	for index := 0; index < len(messages); index++ {
		value := messages[index]
		if value.Role == core.RoleSystem {
			if strings.TrimSpace(value.Content) != "" {
				systems = append(systems, value.Content)
			}
			continue
		}
		if value.Role == core.RoleTool {
			blocks := make([]contentBlock, 0)
			for index < len(messages) && messages[index].Role == core.RoleTool {
				toolMessage := messages[index]
				var content any = toolMessage.Content
				isError := false
				if toolMessage.ToolResult != nil {
					content = encodeToolResultContent(*toolMessage.ToolResult)
					isError = toolMessage.ToolResult.IsError
				}
				blocks = append(blocks, contentBlock{Type: "tool_result", ToolUseID: toolMessage.ToolCallID, Content: content, IsError: isError})
				index++
			}
			index--
			result = append(result, message{Role: "user", Content: blocks})
			continue
		}
		blocks := make([]contentBlock, 0, len(value.ToolCalls)+len(value.Parts)+len(value.Attachments)+1)
		if strings.TrimSpace(value.Content) != "" {
			blocks = append(blocks, contentBlock{Type: "text", Text: value.Content})
		}
		for _, part := range value.Parts {
			switch part.Type {
			case "text":
				blocks = append(blocks, contentBlock{Type: "text", Text: part.Text})
			case "image":
				blocks = append(blocks, contentBlock{Type: "image", Source: map[string]any{"type": "base64", "media_type": part.MIMEType, "data": base64.StdEncoding.EncodeToString(part.Data)}})
			case "json":
				blocks = append(blocks, contentBlock{Type: "text", Text: string(part.JSON)})
			default:
				encoded, _ := json.Marshal(part)
				blocks = append(blocks, contentBlock{Type: "text", Text: string(encoded)})
			}
		}
		for _, attachment := range value.Attachments {
			if attachment.Kind == "image" {
				blocks = append(blocks, contentBlock{Type: "image", Source: map[string]any{"type": "base64", "media_type": attachment.MIMEType, "data": base64.StdEncoding.EncodeToString(attachment.Data)}})
			} else if attachment.Kind == "pdf" {
				blocks = append(blocks, contentBlock{Type: "document", Source: map[string]any{"type": "base64", "media_type": attachment.MIMEType, "data": base64.StdEncoding.EncodeToString(attachment.Data)}})
			} else {
				blocks = append(blocks, contentBlock{Type: "text", Text: fmt.Sprintf("<attachment name=%q type=%q>\n%s\n</attachment>", attachment.Name, attachment.MIMEType, string(attachment.Data))})
			}
		}
		for _, call := range value.ToolCalls {
			arguments := call.Arguments
			if len(arguments) == 0 {
				arguments = json.RawMessage(`{}`)
			}
			blocks = append(blocks, contentBlock{Type: "tool_use", ID: call.ID, Name: call.Name, Input: arguments})
		}
		role := string(value.Role)
		if role != "assistant" {
			role = "user"
		}
		result = append(result, message{Role: role, Content: blocks})
	}
	return strings.Join(systems, "\n\n"), result
}

// encodeToolResultContent preserves the content types accepted by Anthropic's
// native tool_result block. Protocols that only accept a string continue to use
// ToolResult.ModelText at their own adapter boundary.
func encodeToolResultContent(result core.ToolResult) []contentBlock {
	blocks := make([]contentBlock, 0, len(result.Content)+3)
	appendText := func(text string) {
		if strings.TrimSpace(text) != "" {
			blocks = append(blocks, contentBlock{Type: "text", Text: text})
		}
	}
	if len(result.StructuredContent) > 0 {
		appendText(string(result.StructuredContent))
	}
	for _, block := range result.Content {
		switch block.Type {
		case "text":
			appendText(block.Text)
		case "json":
			appendText(string(block.JSON))
		case "image":
			if len(block.Data) > 0 && strings.HasPrefix(strings.ToLower(block.MIMEType), "image/") {
				blocks = append(blocks, contentBlock{Type: "image", Source: map[string]any{
					"type": "base64", "media_type": block.MIMEType, "data": base64.StdEncoding.EncodeToString(block.Data),
				}})
			} else {
				appendText(toolResultBlockMetadata(block))
			}
		default:
			appendText(toolResultBlockMetadata(block))
		}
	}
	if result.Error != nil {
		encoded, _ := json.Marshal(map[string]any{"ok": false, "error": result.Error})
		appendText(string(encoded))
	}
	if result.Truncation != nil {
		encoded, _ := json.Marshal(map[string]any{"truncation": result.Truncation})
		appendText(string(encoded))
	}
	if len(blocks) == 0 {
		blocks = append(blocks, contentBlock{Type: "text", Text: result.ModelText()})
	}
	return blocks
}

func toolResultBlockMetadata(block core.ContentBlock) string {
	value := map[string]any{"type": block.Type}
	if block.Name != "" {
		value["name"] = block.Name
	}
	if block.MIMEType != "" {
		value["mimeType"] = block.MIMEType
	}
	if block.URI != "" {
		value["uri"] = block.URI
	}
	if block.ArtifactID != "" {
		value["artifactId"] = block.ArtifactID
	}
	if len(block.Data) > 0 {
		value["bytes"] = len(block.Data)
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func encodeToolChoice(choice core.ToolChoice) any {
	if choice.Name != "" {
		return map[string]string{"type": "tool", "name": choice.Name}
	}
	switch choice.Mode {
	case core.ToolChoiceRequired:
		return map[string]string{"type": "any"}
	case core.ToolChoiceAuto:
		return map[string]string{"type": "auto"}
	default:
		return nil
	}
}

func filterTools(tools []core.ToolSpec, name string) []core.ToolSpec {
	for _, spec := range tools {
		if spec.Name == name {
			return []core.ToolSpec{spec}
		}
	}
	return nil
}

func normalizeStopReason(reason string, hasTools bool) (core.StopReason, string) {
	normalized := strings.ToLower(strings.TrimSpace(reason))
	switch normalized {
	case "tool_use":
		return core.StopReasonToolUse, ""
	case "end_turn", "stop_sequence", "":
		if hasTools {
			return core.StopReasonToolUse, ""
		}
		return core.StopReasonStop, ""
	case "max_tokens":
		return core.StopReasonLength, normalized
	case "pause_turn":
		return core.StopReasonIncomplete, normalized
	case "refusal":
		return core.StopReasonError, normalized
	default:
		return core.StopReasonIncomplete, normalized
	}
}
