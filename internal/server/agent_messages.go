package server

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/store"
)

func toCoreMessage(value store.Message) agent.Message {
	message := agent.Message{Role: agent.Role(value.Role), Content: value.Content, ToolCallID: value.ToolCallID, Name: value.Name}
	if len(value.ToolResult) > 0 {
		var result agent.ToolResult
		if json.Unmarshal(value.ToolResult, &result) == nil {
			artifacts := make(map[string]store.Attachment, len(value.Attachments))
			for _, attachment := range value.Attachments {
				artifacts[attachment.ID] = attachment
			}
			for index := range result.Content {
				block := &result.Content[index]
				if block.ArtifactID == "" || len(block.Data) > 0 {
					continue
				}
				if artifact, exists := artifacts[block.ArtifactID]; exists {
					block.Data = append([]byte(nil), artifact.Data...)
				}
			}
			message.ToolResult = &result
		}
	}
	for _, attachment := range value.Attachments {
		if value.Role == string(agent.RoleTool) {
			continue
		}
		// EasyAgent's OpenAI-compatible adapters currently have no portable
		// audio input shape. Keep legacy persisted audio out of model requests.
		if attachment.Kind == "audio" {
			continue
		}
		message.Attachments = append(message.Attachments, agent.Attachment{Name: attachment.Name, MIMEType: attachment.MIMEType, Kind: attachment.Kind, Data: attachment.Data})
	}
	for _, call := range value.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, agent.ToolCall{ID: call.ID, Name: call.Name, Arguments: json.RawMessage(call.Arguments), ActivityKind: call.ActivityKind, ActivitySource: call.ActivitySource, DisplayName: call.DisplayName})
	}
	return message
}

func fromCoreMessage(value agent.Message) store.Message {
	message := store.Message{Role: string(value.Role), Content: value.Content, Attachments: []store.Attachment{}, ToolCallID: value.ToolCallID, Name: value.Name, ToolCalls: []store.ToolCall{}, CreatedAt: time.Now()}
	if value.ToolResult != nil {
		result := *value.ToolResult
		result.Content = append([]agent.ContentBlock(nil), value.ToolResult.Content...)
		for index := range result.Content {
			block := &result.Content[index]
			if len(block.Data) == 0 {
				continue
			}
			artifactID := newID()
			name := block.Name
			if name == "" {
				name = fmt.Sprintf("tool-result-%d", index+1)
			}
			message.Attachments = append(message.Attachments, store.Attachment{
				ID: artifactID, Name: name, MIMEType: block.MIMEType, Kind: block.Type,
				Size: int64(len(block.Data)), Data: append([]byte(nil), block.Data...),
			})
			block.ArtifactID = artifactID
			block.Data = nil
		}
		message.ToolResult, _ = json.Marshal(result)
	}
	for _, call := range value.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, store.ToolCall{ID: call.ID, Name: call.Name, Arguments: string(call.Arguments), ActivityKind: call.ActivityKind, ActivitySource: call.ActivitySource, DisplayName: call.DisplayName})
	}
	return message
}
