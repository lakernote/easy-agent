package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	core "github.com/lakernote/easy-agent/internal/agent"
)

func TestNativeChatPreservesToolsResultsAndUsage(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/chat" {
			t.Fatalf("native Ollama path = %q", request.URL.Path)
		}
		var body struct {
			Messages []chatMessage  `json:"messages"`
			Tools    []tool         `json:"tools"`
			Think    *bool          `json:"think"`
			Options  map[string]any `json:"options"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Tools) != 1 || body.Tools[0].Function.Name != "lookup" || body.Think == nil || *body.Think {
			t.Fatalf("native options/tools not encoded: %+v", body)
		}
		if body.Options["num_ctx"] != float64(8192) || body.Options["num_predict"] != float64(256) {
			t.Fatalf("Ollama options = %#v", body.Options)
		}
		if len(body.Messages) != 2 || body.Messages[1].Role != "tool" || body.Messages[1].ToolName != "lookup" || !strings.Contains(body.Messages[1].Content, `"answer":42`) {
			t.Fatalf("typed tool result not encoded: %+v", body.Messages)
		}
		calls.Add(1)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"model":"qwen","message":{"role":"assistant","tool_calls":[{"function":{"name":"lookup","arguments":{"id":42}}}]},"done":true,"done_reason":"stop","prompt_eval_count":20,"prompt_eval_cached_count":7,"eval_count":5}`))
	}))
	defer provider.Close()

	client, err := New(Config{BaseURL: provider.URL + "/v1", DisableThinking: true, ContextWindow: 8192})
	if err != nil {
		t.Fatal(err)
	}
	result := core.NewToolResult(`{"answer":42}`)
	request := core.Request{
		Model: "qwen", MaxOutputTokens: 256,
		Messages:   []core.Message{{Role: core.RoleUser, Content: "find"}, {Role: core.RoleTool, Name: "lookup", ToolCallID: "old", Content: result.ModelText(), ToolResult: &result}},
		Tools:      []core.ToolSpec{{Name: "lookup", Parameters: map[string]any{"type": "object"}}},
		ToolChoice: core.ToolChoice{Mode: core.ToolChoiceAuto},
	}
	first, err := client.Generate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Generate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.StopReason != core.StopReasonToolUse || first.Usage.CachedInputTokens != 7 || !first.Usage.CacheReported || first.Exchange.HistoryMode != "full_history" {
		t.Fatalf("normalized response = %+v", first)
	}
	if len(first.Message.ToolCalls) != 1 || string(first.Message.ToolCalls[0].Arguments) != `{"id":42}` {
		t.Fatalf("tool call = %+v", first.Message.ToolCalls)
	}
	if first.Message.ToolCalls[0].ID == second.Message.ToolCalls[0].ID || calls.Load() != 2 {
		t.Fatalf("synthetic call IDs must be unique: %q %q", first.Message.ToolCalls[0].ID, second.Message.ToolCalls[0].ID)
	}
}

func TestNativeChatStreamsNDJSONAndKeepsFinalAudit(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body chatRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.Stream || request.Header.Get("Accept") != "application/x-ndjson" || request.Header.Get("Authorization") != "Bearer remote-secret" {
			t.Fatalf("stream request mismatch: stream=%v headers=%v", body.Stream, request.Header)
		}
		response.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = fmt.Fprintln(response, `{"model":"qwen","message":{"role":"assistant","content":"你"},"done":false}`)
		_, _ = fmt.Fprintln(response, `{"model":"qwen","message":{"role":"assistant","content":"好"},"done":false}`)
		_, _ = fmt.Fprintln(response, `{"model":"qwen","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":4,"eval_count":2}`)
	}))
	defer provider.Close()

	client, err := New(Config{BaseURL: provider.URL, APIKey: "remote-secret"})
	if err != nil {
		t.Fatal(err)
	}
	var deltas strings.Builder
	response, err := client.Generate(context.Background(), core.Request{
		Model: "qwen", Messages: []core.Message{{Role: core.RoleUser, Content: "hello"}},
		OnTextDelta: func(delta string) { deltas.WriteString(delta) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Content != "你好" || deltas.String() != "你好" || response.StopReason != core.StopReasonStop || response.Usage.TotalTokens != 6 {
		t.Fatalf("stream response = %+v deltas=%q", response, deltas.String())
	}
	var trace streamTrace
	if json.Unmarshal([]byte(response.Exchange.Response), &trace) != nil || !trace.Stream || trace.Transport != "ndjson" || len(trace.RawEvents) != 3 || trace.FinalResponse.Message.Content != "你好" {
		t.Fatalf("stream trace = %s", response.Exchange.Response)
	}
}

func TestNativeChatKeepsPartialTraceOnMalformedStream(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = fmt.Fprintln(response, `{"model":"qwen","message":{"role":"assistant","content":"partial"},"done":false}`)
		_, _ = fmt.Fprintln(response, `{malformed`)
	}))
	defer provider.Close()
	client, _ := New(Config{BaseURL: provider.URL})
	result, err := client.Generate(context.Background(), core.Request{Model: "qwen", OnTextDelta: func(string) {}})
	if err == nil {
		t.Fatal("malformed stream should fail")
	}
	var trace streamTrace
	if json.Unmarshal([]byte(result.Exchange.Response), &trace) != nil || trace.Transport != "ndjson" || len(trace.RawEvents) != 1 || trace.FinalResponse.Message.Content != "partial" {
		t.Fatalf("partial Ollama trace = %s", result.Exchange.Response)
	}
}

func TestNativeChatDoesNotParseTextAsToolCall(t *testing.T) {
	response, err := normalizeResponse(chatResponse{
		Message: chatMessage{Content: `{"name":"lookup","arguments":{"id":9}}`}, Done: true, DoneReason: "stop",
	}, 1)
	if err != nil || len(response.Message.ToolCalls) != 0 || response.Message.Content == "" {
		t.Fatalf("text must remain text, response=%+v err=%v", response, err)
	}
}
