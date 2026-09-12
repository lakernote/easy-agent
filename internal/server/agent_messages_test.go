package server

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/store"
)

func TestToolResultBinaryIsExternalizedAndRehydratedOnlyForRuntime(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.CreateSession(store.CreateSessionParams{ID: "session", Title: "artifact", Model: "fixture", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte{0x7f}, 32*1024)
	result := agent.ToolResult{Content: []agent.ContentBlock{{Type: "image", Name: "result.png", MIMEType: "image/png", Data: payload}}}
	stored := fromCoreMessage(agent.Message{Role: agent.RoleTool, ToolCallID: "call-1", Name: "render", ToolResult: &result})
	if len(stored.Attachments) != 1 || len(stored.Attachments[0].Data) != len(payload) {
		t.Fatalf("externalized attachment = %+v", stored.Attachments)
	}
	var persisted agent.ToolResult
	if err := json.Unmarshal(stored.ToolResult, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Content[0].ArtifactID == "" || len(persisted.Content[0].Data) != 0 {
		t.Fatalf("persisted tool result still embeds bytes: %+v", persisted.Content[0])
	}
	if err := database.AppendMessage("session", stored); err != nil {
		t.Fatal(err)
	}

	window, err := database.LoadSessionWindow("session", 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Messages[0].Attachments) != 1 || len(window.Messages[0].Attachments[0].Data) != 0 {
		t.Fatalf("HTTP window loaded binary data: %+v", window.Messages[0].Attachments)
	}
	full, err := database.LoadSession("session")
	if err != nil {
		t.Fatal(err)
	}
	rehydrated := toCoreMessage(full.Messages[0])
	if rehydrated.ToolResult == nil || !bytes.Equal(rehydrated.ToolResult.Content[0].Data, payload) {
		t.Fatal("runtime load did not rehydrate artifact bytes")
	}
}
