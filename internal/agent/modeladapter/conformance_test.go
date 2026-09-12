package modeladapter

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/agent/qualification"
)

func TestBuiltInAdaptersShareNativeStreamingQualification(t *testing.T) {
	for _, protocol := range []string{ProtocolChatCompletions, ProtocolResponses, ProtocolAnthropic, ProtocolOllamaChat} {
		t.Run(protocol, func(t *testing.T) {
			var calls atomic.Int32
			var requestMu sync.Mutex
			requests := make([]string, 0, 2)
			accepts := make([]string, 0, 2)
			provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				body, _ := io.ReadAll(request.Body)
				requestMu.Lock()
				requests = append(requests, string(body))
				accepts = append(accepts, request.Header.Get("Accept"))
				requestMu.Unlock()
				writeQualificationFixture(response, protocol, int(calls.Add(1)))
			}))
			defer provider.Close()

			model, err := New(Config{Provider: "fixture", Protocol: protocol, BaseURL: provider.URL, DisableThinking: true})
			if err != nil {
				t.Fatal(err)
			}
			capabilities := agent.CapabilitiesOf(model)
			if !capabilities.Streaming || !capabilities.NativeTools {
				t.Fatalf("capabilities = %+v", capabilities)
			}
			result, err := qualification.Run(t.Context(), model, "fixture")
			if err != nil || result.ToolCall != qualification.ToolName || result.Answer != "EASYAGENT_OK" || calls.Load() != 2 {
				t.Fatalf("result=%+v calls=%d err=%v", result, calls.Load(), err)
			}
			requestMu.Lock()
			defer requestMu.Unlock()
			if len(requests) != 2 || strings.Contains(requests[0], "EASYAGENT_OK") || !strings.Contains(requests[1], "EASYAGENT_OK") {
				t.Fatalf("typed tool result was not returned on the second request: %#v", requests)
			}
			for _, body := range requests {
				var payload map[string]any
				if json.Unmarshal([]byte(body), &payload) != nil || payload["stream"] != true {
					t.Fatalf("qualification request is not streaming: %s", body)
				}
			}
			wantAccept := "text/event-stream"
			if protocol == ProtocolOllamaChat {
				wantAccept = "application/x-ndjson"
			}
			if len(accepts) != 2 || accepts[0] != wantAccept || accepts[1] != wantAccept {
				t.Fatalf("Accept headers = %#v, want %q", accepts, wantAccept)
			}
		})
	}
}

func writeQualificationFixture(response http.ResponseWriter, protocol string, call int) {
	if protocol == ProtocolOllamaChat {
		response.Header().Set("Content-Type", "application/x-ndjson")
		if call == 1 {
			_, _ = fmt.Fprintln(response, `{"model":"fixture","message":{"role":"assistant","tool_calls":[{"id":"call-1","function":{"name":"easyagent_diagnostic_echo","arguments":{"text":"ping"}}}]},"done":true,"done_reason":"stop","prompt_eval_count":10,"eval_count":3}`)
			return
		}
		_, _ = fmt.Fprintln(response, `{"model":"fixture","message":{"role":"assistant","content":"EASYAGENT_OK"},"done":true,"done_reason":"stop","prompt_eval_count":16,"eval_count":2}`)
		return
	}

	response.Header().Set("Content-Type", "text/event-stream")
	switch protocol {
	case ProtocolChatCompletions:
		if call == 1 {
			writeSSE(response, `{"id":"one","model":"fixture","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"easyagent_diagnostic_echo","arguments":"{\"text\":\"ping\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`)
		} else {
			writeSSE(response, `{"id":"two","model":"fixture","choices":[{"delta":{"content":"EASYAGENT_OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":16,"completion_tokens":2,"total_tokens":18}}`)
		}
		_, _ = fmt.Fprintln(response, "data: [DONE]")
	case ProtocolResponses:
		if call == 1 {
			writeSSE(response, `{"type":"response.output_item.done","item":{"type":"function_call","call_id":"call-1","name":"easyagent_diagnostic_echo","arguments":"{\"text\":\"ping\"}"}}`)
			writeSSE(response, `{"type":"response.completed","response":{"id":"one","model":"fixture","status":"completed","usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13}}}`)
		} else {
			writeSSE(response, `{"type":"response.output_text.delta","delta":"EASYAGENT_OK"}`)
			writeSSE(response, `{"type":"response.completed","response":{"id":"two","model":"fixture","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"EASYAGENT_OK"}]}],"usage":{"input_tokens":16,"output_tokens":2,"total_tokens":18}}}`)
		}
	case ProtocolAnthropic:
		if call == 1 {
			writeSSE(response, `{"type":"message_start","message":{"id":"one","model":"fixture","usage":{"input_tokens":10,"output_tokens":0}}}`)
			writeSSE(response, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-1","name":"easyagent_diagnostic_echo","input":{}}}`)
			writeSSE(response, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"text\":\"ping\"}"}}`)
			writeSSE(response, `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}`)
		} else {
			writeSSE(response, `{"type":"message_start","message":{"id":"two","model":"fixture","usage":{"input_tokens":16,"output_tokens":0}}}`)
			writeSSE(response, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
			writeSSE(response, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"EASYAGENT_OK"}}`)
			writeSSE(response, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`)
		}
		writeSSE(response, `{"type":"message_stop"}`)
	}
}

func writeSSE(response http.ResponseWriter, value string) {
	_, _ = fmt.Fprintf(response, "data: %s\n\n", value)
}
