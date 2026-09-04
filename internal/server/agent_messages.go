package server

import (
	"encoding/json"
	"time"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/store"
)

func toCoreMessage(value store.Message) agent.Message {
	message := agent.Message{Role: agent.Role(value.Role), Content: value.Content, ToolCallID: value.ToolCallID, Name: value.Name}
	for _, attachment := range value.Attachments {
		message.Attachments = append(message.Attachments, agent.Attachment{Name: attachment.Name, MIMEType: attachment.MIMEType, Kind: attachment.Kind, Data: attachment.Data})
	}
	for _, call := range value.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, agent.ToolCall{ID: call.ID, Name: call.Name, Arguments: json.RawMessage(call.Arguments), ActivityKind: call.ActivityKind, ActivitySource: call.ActivitySource, DisplayName: call.DisplayName})
	}
	return message
}

func fromCoreMessage(value agent.Message) store.Message {
	message := store.Message{Role: string(value.Role), Content: value.Content, Attachments: []store.Attachment{}, ToolCallID: value.ToolCallID, Name: value.Name, ToolCalls: []store.ToolCall{}, CreatedAt: time.Now()}
	for _, call := range value.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, store.ToolCall{ID: call.ID, Name: call.Name, Arguments: string(call.Arguments), ActivityKind: call.ActivityKind, ActivitySource: call.ActivitySource, DisplayName: call.DisplayName})
	}
	return message
}
