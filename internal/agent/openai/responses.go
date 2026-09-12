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
	"strings"
	"time"

	core "github.com/lakernote/easy-agent/internal/agent"
)

type responsesRequest struct {
	Model              string         `json:"model"`
	Instructions       string         `json:"instructions,omitempty"`
	Input              []any          `json:"input"`
	Tools              []functionTool `json:"tools,omitempty"`
	ToolChoice         any            `json:"tool_choice,omitempty"`
	PromptCacheKey     string         `json:"prompt_cache_key,omitempty"`
	Temperature        float64        `json:"temperature,omitempty"`
	MaxOutputTokens    int            `json:"max_output_tokens,omitempty"`
	PreviousResponseID string         `json:"previous_response_id,omitempty"`
	Reasoning          map[string]any `json:"reasoning,omitempty"`
	Stream             bool           `json:"stream,omitempty"`
}

type responsesContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesOutput struct {
	Type      string             `json:"type"`
	Role      string             `json:"role"`
	Name      string             `json:"name"`
	CallID    string             `json:"call_id"`
	Arguments json.RawMessage    `json:"arguments"`
	Content   []responsesContent `json:"content"`
}

type responsesStreamEvent struct {
	Type        string            `json:"type"`
	Delta       string            `json:"delta"`
	OutputIndex int               `json:"output_index"`
	Item        responsesOutput   `json:"item"`
	Response    responsesResponse `json:"response"`
	Error       *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

type responsesStreamTrace struct {
	Stream        bool              `json:"stream"`
	Transport     string            `json:"transport"`
	FinalResponse responsesResponse `json:"final_response"`
	RawEvents     []json.RawMessage `json:"raw_events"`
}

type responsesResponse struct {
	ID                string `json:"id"`
	Model             string `json:"model"`
	Status            string `json:"status"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Output []responsesOutput `json:"output"`
	Usage  struct {
		InputTokens       int           `json:"input_tokens"`
		OutputTokens      int           `json:"output_tokens"`
		TotalTokens       int           `json:"total_tokens"`
		InputTokenDetails *tokenDetails `json:"input_tokens_details"`
		CacheReadTokens   *int          `json:"cache_read_input_tokens"`
		CacheWriteTokens  *int          `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

func (client *Client) generateResponse(ctx context.Context, request core.Request) (core.Response, error) {
	messages := request.Messages
	if request.PreviousResponseID != "" {
		messages = request.NewMessages
	}
	// Responses 的 previous_response_id 会续接历史输出，但不会继承上一轮的
	// instructions。基础 System Prompt 每轮都可能包含新的日期和 Skill 元数据，
	// 因此始终从完整消息取出并再次发送；普通 input 只发送本轮新增消息。
	instructions, _ := encodeResponsesInput(request.Messages, true)
	_, input := encodeResponsesInput(messages, false)
	payload := responsesRequest{
		Model: request.Model, Instructions: instructions, Input: input, Tools: toolSpecs(request.Tools),
		ToolChoice: encodeResponsesToolChoice(request.ToolChoice), PromptCacheKey: request.PromptCacheKey,
		Temperature: request.Temperature, MaxOutputTokens: request.MaxOutputTokens, PreviousResponseID: request.PreviousResponseID,
	}
	if client.thinkingDisabledFor(request) {
		payload.Reasoning = map[string]any{"effort": "none"}
	} else if request.ReasoningEffort != "" {
		payload.Reasoning = map[string]any{"effort": request.ReasoningEffort}
	}
	if request.OnTextDelta != nil {
		payload.Stream = true
		return client.streamResponse(ctx, payload, request.OnTextDelta)
	}
	return client.post(ctx, "/responses", payload, string(Responses), decodeResponsesResponse)
}

func (client *Client) streamResponse(ctx context.Context, payload responsesRequest, onTextDelta func(string)) (core.Response, error) {
	startedAt := time.Now()
	body, err := json.Marshal(payload)
	if err != nil {
		return core.Response{}, err
	}
	exchange := core.Exchange{Model: payload.Model, Protocol: string(Responses), Request: string(body)}
	decorateRequestShape(&exchange, body)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/responses", bytes.NewReader(body))
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
		return core.Response{Exchange: exchange}, modelHTTPError(httpResponse, responseBody)
	}

	var final responsesResponse
	var textOutput strings.Builder
	items := make([]responsesOutput, 0)
	chunks := make([]json.RawMessage, 0, 32)
	totalBytes := 0
	terminal := false
	recordPartialTrace := func() {
		if final.Model == "" {
			final.Model = payload.Model
		}
		if len(final.Output) == 0 {
			final.Output = append(final.Output, items...)
			if textOutput.Len() > 0 && !hasResponseMessage(final.Output) {
				final.Output = append(final.Output, responsesOutput{Type: "message", Role: "assistant", Content: []responsesContent{{Type: "output_text", Text: textOutput.String()}}})
			}
		}
		trace, _ := json.Marshal(responsesStreamTrace{Stream: true, Transport: "sse", FinalResponse: final, RawEvents: chunks})
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
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		totalBytes += len(data)
		if totalBytes > 8*1024*1024 {
			recordPartialTrace()
			return core.Response{Exchange: exchange}, errors.New("Responses 流响应超过 8 MiB")
		}
		if !json.Valid(data) {
			recordPartialTrace()
			return core.Response{Exchange: exchange}, errors.New("Responses 流返回了无效 JSON")
		}
		chunks = append(chunks, append(json.RawMessage(nil), data...))
		var event responsesStreamEvent
		if err := json.Unmarshal(data, &event); err != nil {
			recordPartialTrace()
			return core.Response{Exchange: exchange}, err
		}
		switch event.Type {
		case "response.output_text.delta":
			textOutput.WriteString(event.Delta)
			onTextDelta(event.Delta)
		case "response.output_item.done":
			items = append(items, event.Item)
		case "response.completed", "response.incomplete", "response.failed":
			final = event.Response
			terminal = true
			if final.Status == "" {
				final.Status = strings.TrimPrefix(event.Type, "response.")
			}
		case "error":
			message := "Responses 流返回错误"
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
	if final.Model == "" {
		final.Model = payload.Model
	}
	if len(final.Output) == 0 {
		final.Output = items
		if textOutput.Len() > 0 && !hasResponseMessage(final.Output) {
			final.Output = append(final.Output, responsesOutput{Type: "message", Role: "assistant", Content: []responsesContent{{Type: "output_text", Text: textOutput.String()}}})
		}
	}
	if !terminal {
		final.Status = "incomplete"
		final.IncompleteDetails = &struct {
			Reason string `json:"reason"`
		}{Reason: "stream ended without terminal response"}
	}
	trace, _ := json.Marshal(responsesStreamTrace{Stream: true, Transport: "sse", FinalResponse: final, RawEvents: chunks})
	exchange.Response = string(trace)
	exchange.Duration = time.Since(startedAt)
	response, decodeErr := decodeResponsesPayload(final)
	response.Exchange = exchange
	response.Exchange.Usage = response.Usage
	response.Exchange.StopReason = response.StopReason
	response.Exchange.IncompleteReason = response.IncompleteReason
	return response, decodeErr
}

func hasResponseMessage(items []responsesOutput) bool {
	for _, item := range items {
		if item.Type == "message" {
			return true
		}
	}
	return false
}

func encodeResponsesToolChoice(choice core.ToolChoice) any {
	if choice.Name != "" {
		return map[string]string{"type": "function", "name": choice.Name}
	}
	switch choice.Mode {
	case core.ToolChoiceAuto, core.ToolChoiceNone, core.ToolChoiceRequired:
		return choice.Mode
	default:
		return nil
	}
}

func encodeResponsesInput(messages []core.Message, includeSystem bool) (string, []any) {
	instructions := []string{}
	input := make([]any, 0, len(messages))
	for _, message := range messages {
		if message.Role == core.RoleSystem {
			if includeSystem && strings.TrimSpace(message.Content) != "" {
				instructions = append(instructions, message.Content)
			}
			continue
		}
		if message.Role == core.RoleTool {
			output := message.Content
			if message.ToolResult != nil {
				output = message.ToolResult.ModelText()
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": message.ToolCallID, "output": output})
			continue
		}
		if strings.TrimSpace(message.Content) != "" || len(message.Attachments) > 0 {
			input = append(input, map[string]any{"role": string(message.Role), "content": encodeResponsesContent(message)})
		}
		for _, call := range message.ToolCalls {
			arguments := string(call.Arguments)
			if strings.TrimSpace(arguments) == "" {
				arguments = "{}"
			}
			input = append(input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": arguments})
		}
	}
	return strings.Join(instructions, "\n\n"), input
}

func encodeResponsesContent(message core.Message) any {
	if len(message.Parts) == 0 && len(message.Attachments) == 0 {
		return message.Content
	}
	parts := make([]any, 0, len(message.Parts)+len(message.Attachments)+1)
	if strings.TrimSpace(message.Content) != "" {
		parts = append(parts, map[string]any{"type": "input_text", "text": message.Content})
	}
	for _, block := range message.Parts {
		switch block.Type {
		case "text":
			parts = append(parts, map[string]any{"type": "input_text", "text": block.Text})
		case "image":
			imageURL := block.URI
			if imageURL == "" {
				imageURL = contentBlockDataURL(block)
			}
			parts = append(parts, map[string]any{"type": "input_image", "image_url": imageURL, "detail": "auto"})
		case "json":
			parts = append(parts, map[string]any{"type": "input_text", "text": string(block.JSON)})
		default:
			encoded, _ := json.Marshal(block)
			parts = append(parts, map[string]any{"type": "input_text", "text": string(encoded)})
		}
	}
	for _, attachment := range message.Attachments {
		switch attachment.Kind {
		case "image":
			parts = append(parts, map[string]any{"type": "input_image", "image_url": attachmentDataURL(attachment), "detail": "auto"})
		case "pdf":
			parts = append(parts, map[string]any{"type": "input_file", "filename": attachment.Name, "file_data": attachmentDataURL(attachment)})
		default:
			parts = append(parts, map[string]any{"type": "input_text", "text": textAttachment(attachment)})
		}
	}
	return parts
}

func decodeResponsesResponse(body []byte) (core.Response, error) {
	var payload responsesResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return core.Response{}, err
	}
	return decodeResponsesPayload(payload)
}

func decodeResponsesPayload(payload responsesResponse) (core.Response, error) {
	message := core.Message{Role: core.RoleAssistant}
	for _, item := range payload.Output {
		switch item.Type {
		case "message":
			for _, content := range item.Content {
				if content.Type == "output_text" || content.Type == "text" {
					message.Content += content.Text
				}
			}
		case "function_call":
			arguments, err := normalizeArguments(item.Arguments)
			if err != nil {
				return core.Response{}, fmt.Errorf("工具 %s 参数无法解析: %w", item.Name, err)
			}
			message.ToolCalls = append(message.ToolCalls, core.ToolCall{ID: item.CallID, Name: item.Name, Arguments: arguments})
		}
	}
	var detailCached, detailWrite, detailCreated *int
	if payload.Usage.InputTokenDetails != nil {
		detailCached = payload.Usage.InputTokenDetails.CachedTokens
		detailWrite = payload.Usage.InputTokenDetails.CacheWriteTokens
		detailCreated = payload.Usage.InputTokenDetails.CacheCreationInputTokens
	}
	usage := core.Usage{
		InputTokens: payload.Usage.InputTokens, OutputTokens: payload.Usage.OutputTokens, TotalTokens: payload.Usage.TotalTokens,
		CachedInputTokens: optionalMax(detailCached, payload.Usage.CacheReadTokens),
		CacheWriteTokens:  optionalMax(detailWrite, detailCreated, payload.Usage.CacheWriteTokens),
		CacheReported:     anyReported(detailCached, payload.Usage.CacheReadTokens, detailWrite, detailCreated, payload.Usage.CacheWriteTokens),
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	stopReason, incompleteReason := normalizeResponsesStopReason(payload, len(message.ToolCalls) > 0)
	response := core.Response{
		ID: payload.ID, Message: message, Usage: usage,
		StopReason: stopReason, IncompleteReason: incompleteReason,
	}
	if strings.TrimSpace(message.Content) == "" && len(message.ToolCalls) == 0 && (stopReason == core.StopReasonStop || stopReason == core.StopReasonToolUse) {
		return response, core.ErrEmptyModelResponse
	}
	return response, nil
}

func normalizeResponsesStopReason(payload responsesResponse, hasToolCalls bool) (core.StopReason, string) {
	status := strings.ToLower(strings.TrimSpace(payload.Status))
	switch status {
	case "", "completed":
		if hasToolCalls {
			return core.StopReasonToolUse, ""
		}
		return core.StopReasonStop, ""
	case "incomplete":
		reason := ""
		if payload.IncompleteDetails != nil {
			reason = strings.TrimSpace(payload.IncompleteDetails.Reason)
		}
		if strings.Contains(strings.ToLower(reason), "max_output") || strings.Contains(strings.ToLower(reason), "token") {
			return core.StopReasonLength, reason
		}
		return core.StopReasonIncomplete, reason
	case "cancelled", "canceled":
		return core.StopReasonInterrupted, status
	case "failed":
		reason := status
		if payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "" {
			reason = payload.Error.Message
		}
		return core.StopReasonError, reason
	default:
		return core.StopReasonIncomplete, status
	}
}
