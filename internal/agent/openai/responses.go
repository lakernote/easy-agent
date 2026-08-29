package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
}

type responsesOutput struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Name      string          `json:"name"`
	CallID    string          `json:"call_id"`
	Arguments json.RawMessage `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type responsesResponse struct {
	ID     string            `json:"id"`
	Model  string            `json:"model"`
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
	if client.disableThinking {
		payload.Reasoning = map[string]any{"effort": "none"}
	} else if request.ReasoningEffort != "" {
		payload.Reasoning = map[string]any{"effort": request.ReasoningEffort}
	}
	return client.post(ctx, "/responses", payload, string(Responses), decodeResponsesResponse)
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
			input = append(input, map[string]any{"type": "function_call_output", "call_id": message.ToolCallID, "output": message.Content})
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
	if len(message.Attachments) == 0 {
		return message.Content
	}
	parts := make([]any, 0, len(message.Attachments)+1)
	if strings.TrimSpace(message.Content) != "" {
		parts = append(parts, map[string]any{"type": "input_text", "text": message.Content})
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
	if strings.TrimSpace(message.Content) == "" && len(message.ToolCalls) == 0 {
		return core.Response{}, errors.New("Responses API 没有返回消息或工具调用")
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
	return core.Response{ID: payload.ID, Message: message, Usage: usage}, nil
}
