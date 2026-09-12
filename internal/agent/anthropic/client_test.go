package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/lakernote/easy-agent/internal/agent"
)

func TestMessagesAPIUsesNativeToolBlocks(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/messages" || request.Header.Get("x-api-key") != "secret" || request.Header.Get("anthropic-version") == "" {
			t.Fatalf("request boundary mismatch: %s headers=%v", request.URL.Path, request.Header)
		}
		var body struct {
			System   string    `json:"system"`
			Messages []message `json:"messages"`
			Tools    []tool    `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.System != "system" || len(body.Messages) != 3 || len(body.Tools) != 1 {
			t.Fatalf("request = %+v", body)
		}
		blocks, ok := body.Messages[2].Content.([]any)
		if !ok || len(blocks) != 2 {
			t.Fatalf("contiguous tool results were not grouped: %#v", body.Messages[2].Content)
		}
		first, _ := blocks[0].(map[string]any)
		second, _ := blocks[1].(map[string]any)
		if first["type"] != "tool_result" || first["tool_use_id"] != "one" || second["is_error"] != true {
			t.Fatalf("tool_result blocks = %#v", blocks)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"id":"msg_1","model":"claude-test","content":[{"type":"text","text":"checking"},{"type":"tool_use","id":"next","name":"lookup","input":{"id":9}}],"stop_reason":"tool_use","usage":{"input_tokens":30,"output_tokens":8,"cache_read_input_tokens":10}}`))
	}))
	defer provider.Close()

	client, err := New(Config{BaseURL: provider.URL + "/v1", APIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	okResult := core.NewToolResult(`{"answer":1}`)
	failedResult := core.NewToolResult("not found")
	failedResult.IsError = true
	response, err := client.Generate(context.Background(), core.Request{
		Model: "claude-test", MaxOutputTokens: 100,
		Messages: []core.Message{
			{Role: core.RoleSystem, Content: "system"},
			{Role: core.RoleUser, Content: "find"},
			{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "one", Name: "lookup", Arguments: json.RawMessage(`{"id":1}`)}, {ID: "two", Name: "lookup", Arguments: json.RawMessage(`{"id":2}`)}}},
			{Role: core.RoleTool, ToolCallID: "one", ToolResult: &okResult},
			{Role: core.RoleTool, ToolCallID: "two", ToolResult: &failedResult},
		},
		Tools:      []core.ToolSpec{{Name: "lookup", Parameters: map[string]any{"type": "object"}}},
		ToolChoice: core.ToolChoice{Mode: core.ToolChoiceAuto},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.StopReason != core.StopReasonToolUse || response.Message.Content != "checking" || len(response.Message.ToolCalls) != 1 || string(response.Message.ToolCalls[0].Arguments) != `{"id":9}` {
		t.Fatalf("response = %+v", response)
	}
	if response.Usage.CachedInputTokens != 10 || !response.Usage.CacheReported {
		t.Fatalf("usage = %+v", response.Usage)
	}
}

func TestMessagesAPIStreamsTextAndNativeToolUse(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body requestBody
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.Stream || request.Header.Get("Accept") != "text/event-stream" {
			t.Fatalf("stream request mismatch: stream=%v headers=%v", body.Stream, request.Header)
		}
		response.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`{"type":"message_start","message":{"id":"msg_stream","model":"claude-test","usage":{"input_tokens":12,"output_tokens":0,"cache_read_input_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"好"}}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_native","name":"lookup","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"id\":"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"9}"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
			`{"type":"message_stop"}`,
		}
		for _, event := range events {
			_, _ = fmt.Fprintf(response, "event: update\ndata: %s\n\n", event)
		}
	}))
	defer provider.Close()

	client, err := New(Config{BaseURL: provider.URL})
	if err != nil {
		t.Fatal(err)
	}
	var deltas strings.Builder
	result, err := client.Generate(context.Background(), core.Request{
		Model: "claude-test", Messages: []core.Message{{Role: core.RoleUser, Content: "find"}},
		Tools:       []core.ToolSpec{{Name: "lookup", Parameters: map[string]any{"type": "object"}}},
		OnTextDelta: func(delta string) { deltas.WriteString(delta) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message.Content != "你好" || deltas.String() != "你好" || result.StopReason != core.StopReasonToolUse || result.Usage.TotalTokens != 19 {
		t.Fatalf("stream response = %+v deltas=%q", result, deltas.String())
	}
	if len(result.Message.ToolCalls) != 1 || result.Message.ToolCalls[0].Name != "lookup" || string(result.Message.ToolCalls[0].Arguments) != `{"id":9}` {
		t.Fatalf("native stream tool call = %+v", result.Message.ToolCalls)
	}
	if !result.Usage.CacheReported || result.Usage.CachedInputTokens != 0 {
		t.Fatalf("explicit cache zero was lost: %+v", result.Usage)
	}
	var trace streamTrace
	if json.Unmarshal([]byte(result.Exchange.Response), &trace) != nil || !trace.Stream || len(trace.RawEvents) != 9 {
		t.Fatalf("stream trace = %s", result.Exchange.Response)
	}
}

func TestMessagesAPIKeepsPartialTraceOnStreamError(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(response, `data: {"type":"message_start","message":{"id":"partial","model":"claude-test"}}`)
		_, _ = fmt.Fprintln(response, `data: {"type":"error","error":{"message":"provider stopped"}}`)
	}))
	defer provider.Close()
	client, _ := New(Config{BaseURL: provider.URL})
	result, err := client.Generate(context.Background(), core.Request{Model: "claude-test", OnTextDelta: func(string) {}})
	if err == nil {
		t.Fatal("stream error should fail")
	}
	var trace streamTrace
	if json.Unmarshal([]byte(result.Exchange.Response), &trace) != nil || trace.Transport != "sse" || len(trace.RawEvents) != 2 || trace.FinalResponse.ID != "partial" {
		t.Fatalf("partial Anthropic trace = %s", result.Exchange.Response)
	}
}

func TestMessagesAPIDoesNotParseTextAsToolCall(t *testing.T) {
	response, err := decodeResponse(responseBody{
		Content:    []contentBlock{{Type: "text", Text: `{"name":"lookup","arguments":{"id":9}}`}},
		StopReason: "end_turn",
	})
	if err != nil || len(response.Message.ToolCalls) != 0 || response.Message.Content == "" {
		t.Fatalf("text must remain text, response=%+v err=%v", response, err)
	}
}

func TestMessagesAPIPreservesRichToolResultBlocks(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		messages := body["messages"].([]any)
		toolMessage := messages[1].(map[string]any)
		toolResult := toolMessage["content"].([]any)[0].(map[string]any)
		content := toolResult["content"].([]any)
		if len(content) != 3 || content[0].(map[string]any)["text"] != `{"answer":42}` || content[1].(map[string]any)["type"] != "image" {
			t.Fatalf("rich tool result = %#v", toolResult)
		}
		if toolResult["is_error"] != true || content[2].(map[string]any)["text"] == "" {
			t.Fatalf("tool error semantics = %#v", toolResult)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"id":"msg_1","model":"claude-test","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{}}`))
	}))
	defer provider.Close()

	result := core.ToolResult{
		StructuredContent: json.RawMessage(`{"answer":42}`),
		Content:           []core.ContentBlock{{Type: "image", MIMEType: "image/png", Data: []byte("png")}},
		IsError:           true,
		Error:             &core.ToolResultError{Code: "partial", Message: "部分结果"},
	}
	client, err := New(Config{BaseURL: provider.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !client.Capabilities().StructuredToolResults {
		t.Fatal("Anthropic rich tool-result capability should be advertised")
	}
	_, err = client.Generate(context.Background(), core.Request{Model: "claude-test", Messages: []core.Message{
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{}`)}}},
		{Role: core.RoleTool, ToolCallID: "call_1", ToolResult: &result},
	}})
	if err != nil {
		t.Fatal(err)
	}
}
