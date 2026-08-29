package openai

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

func TestDisabledThinkingUsesOpenAIReasoningEffort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["reasoning_effort"] != "none" {
			t.Fatalf("禁用推理必须使用 OpenAI 兼容字段: %+v", body)
		}
		if _, exists := body["think"]; exists {
			t.Fatalf("Chat Completions 不应发送 Ollama 原生 think 字段: %+v", body)
		}
		_ = json.NewEncoder(response).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}},
		})
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Protocol: ChatCompletions, DisableThinking: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Generate(context.Background(), core.Request{Model: "fixture", Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}
}

func TestChatCompletionsStreamsTextAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["stream"] != true || body["reasoning_effort"] != "none" {
			t.Fatalf("流式请求或推理配置错误: %+v", body)
		}
		response.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(response, `data: {"id":"chat-stream","model":"fixture","choices":[{"delta":{"role":"assistant","content":"你"}}]}`)
		fmt.Fprintln(response)
		fmt.Fprintln(response, `data: {"id":"chat-stream","model":"fixture","choices":[{"delta":{"content":"好"}}]}`)
		fmt.Fprintln(response)
		fmt.Fprintln(response, `data: {"id":"chat-stream","model":"fixture","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":2,"total_tokens":14,"prompt_tokens_details":{"cached_tokens":5}}}`)
		fmt.Fprintln(response)
		fmt.Fprintln(response, "data: [DONE]")
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Protocol: ChatCompletions, DisableThinking: true})
	if err != nil {
		t.Fatal(err)
	}
	partial := ""
	result, err := client.Generate(context.Background(), core.Request{
		Model: "fixture", Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}},
		OnTextDelta: func(value string) { partial += value },
	})
	if err != nil {
		t.Fatal(err)
	}
	if partial != "你好" || result.Message.Content != "你好" || result.Usage.TotalTokens != 14 || result.Usage.CachedInputTokens != 5 {
		t.Fatalf("流式回答或 Usage 组装错误: partial=%q result=%+v", partial, result)
	}
	if !json.Valid([]byte(result.Exchange.Response)) || !strings.Contains(result.Exchange.Response, `"stream":true`) {
		t.Fatalf("流式原始响应应可格式化审计: %s", result.Exchange.Response)
	}
}

func TestChatCompletionsStreamsToolCallArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(response, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"weather","arguments":"{\"location\":"}}]}}]}`)
		fmt.Fprintln(response)
		fmt.Fprintln(response, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"上海\"}"}}]}}]}`)
		fmt.Fprintln(response)
		fmt.Fprintln(response, "data: [DONE]")
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Protocol: ChatCompletions})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Generate(context.Background(), core.Request{
		Model: "fixture", Messages: []core.Message{{Role: core.RoleUser, Content: "上海天气"}}, OnTextDelta: func(string) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Message.ToolCalls) != 1 || result.Message.ToolCalls[0].Name != "weather" || string(result.Message.ToolCalls[0].Arguments) != `{"location":"上海"}` {
		t.Fatalf("流式 Tool Call 组装错误: %+v", result.Message.ToolCalls)
	}
}

func TestChatCompletionsPreservesReasoningDetailsForToolContinuation(t *testing.T) {
	result, err := decodeChatResponse([]byte(`{
		"choices":[{"message":{"role":"assistant","content":"","reasoning":"检查实时信息","reasoning_details":[{"type":"reasoning.text","text":"opaque"}],"tool_calls":[{"id":"call-1","type":"function","function":{"name":"current_time","arguments":"{}"}}]}}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded := encodeChatMessages([]core.Message{result.Message})
	if len(encoded) != 1 || encoded[0].Reasoning != "检查实时信息" || !strings.Contains(string(encoded[0].ReasoningDetails), `"opaque"`) {
		t.Fatalf("reasoning 上下文没有原样回传: %+v", encoded)
	}
}

func TestChatCompletionsUsesNativeToolMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, exists := body["temperature"]; exists {
			t.Fatalf("未显式配置时不应向推理模型发送 temperature: %+v", body)
		}
		messages := body["messages"].([]any)
		last := messages[len(messages)-1].(map[string]any)
		if last["role"] != "tool" || last["tool_call_id"] != "call-1" {
			t.Fatalf("tool result flattened into wrong role: %+v", last)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chat-1", "model": "fixture",
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "完成"}}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12, "prompt_tokens_details": map[string]any{"cached_tokens": 4}},
		})
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL + "/v1", Protocol: ChatCompletions})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Generate(context.Background(), core.Request{Model: "fixture", Messages: []core.Message{{Role: core.RoleTool, ToolCallID: "call-1", Name: "clock", Content: "12:00"}}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Content != "完成" || response.Usage.CachedInputTokens != 4 || !response.Usage.CacheReported {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestChatCompletionsEncodesAutoToolChoiceAndCacheKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["tool_choice"] != "auto" || body["prompt_cache_key"] != "easyagent-core-v1" {
			t.Fatalf("Chat 工具选择或缓存键错误: %+v", body)
		}
		tools, ok := body["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Fatalf("Chat 应发送完整稳定工具集: %+v", body["tools"])
		}
		_ = json.NewEncoder(response).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}},
		})
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Protocol: ChatCompletions})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Generate(context.Background(), core.Request{
		Model: "fixture", Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}},
		Tools:      []core.ToolSpec{{Name: "current_time", Parameters: map[string]any{"type": "object"}}},
		ToolChoice: core.ToolChoice{Mode: core.ToolChoiceAuto}, PromptCacheKey: "easyagent-core-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestChatCacheReportedDistinguishesMissingFromZero(t *testing.T) {
	withZero, err := decodeChatResponse([]byte(`{
		"choices":[{"message":{"role":"assistant","content":"ok"}}],
		"usage":{"prompt_tokens":10,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":0}}
	}`))
	if err != nil || !withZero.Usage.CacheReported || withZero.Usage.CachedInputTokens != 0 {
		t.Fatalf("明确上报 0 应保留已上报状态: %+v, %v", withZero.Usage, err)
	}
	withoutField, err := decodeChatResponse([]byte(`{
		"choices":[{"message":{"role":"assistant","content":"ok"}}],
		"usage":{"prompt_tokens":10,"completion_tokens":1}
	}`))
	if err != nil || withoutField.Usage.CacheReported {
		t.Fatalf("缺少缓存字段不能冒充 0%% 命中: %+v, %v", withoutField.Usage, err)
	}
	deepSeek, err := decodeChatResponse([]byte(`{
		"choices":[{"message":{"role":"assistant","content":"ok"}}],
		"usage":{"prompt_tokens":20,"completion_tokens":1,"prompt_cache_hit_tokens":12}
	}`))
	if err != nil || !deepSeek.Usage.CacheReported || deepSeek.Usage.CachedInputTokens != 12 {
		t.Fatalf("DeepSeek 兼容缓存字段未解析: %+v, %v", deepSeek.Usage, err)
	}
}

func TestResponsesUsesPreviousResponseAndFunctionOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, exists := body["temperature"]; exists {
			t.Fatalf("Responses 默认不应发送 temperature: %+v", body)
		}
		if body["previous_response_id"] != "resp-1" {
			t.Fatalf("missing previous response: %+v", body)
		}
		if body["instructions"] != "始终使用中文" {
			t.Fatalf("Responses 续聊必须重新发送 instructions: %+v", body)
		}
		if body["tool_choice"] != "auto" || body["prompt_cache_key"] != "easyagent-core-v1" {
			t.Fatalf("Responses 工具选择或缓存键错误: %+v", body)
		}
		item := body["input"].([]any)[0].(map[string]any)
		if item["type"] != "function_call_output" || item["call_id"] != "call-1" {
			t.Fatalf("wrong Responses tool output: %+v", item)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "resp-2", "model": "fixture",
			"output": []any{
				map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": "完成"}},
				},
			},
			"usage": map[string]any{"input_tokens": 8, "output_tokens": 2, "total_tokens": 10},
		})
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL + "/v1", Protocol: Responses})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Generate(context.Background(), core.Request{
		Model: "fixture", PreviousResponseID: "resp-1",
		Messages:    []core.Message{{Role: core.RoleSystem, Content: "始终使用中文"}},
		NewMessages: []core.Message{{Role: core.RoleTool, ToolCallID: "call-1", Content: "ok"}},
		ToolChoice:  core.ToolChoice{Mode: core.ToolChoiceAuto}, PromptCacheKey: "easyagent-core-v1",
	})
	if err != nil || response.ID != "resp-2" || response.Message.Content != "完成" {
		t.Fatalf("unexpected response: %+v, %v", response, err)
	}
}

func TestResponsesCacheDetails(t *testing.T) {
	response, err := decodeResponsesResponse([]byte(`{
		"id":"resp-1",
		"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],
		"usage":{"input_tokens":16,"output_tokens":2,"total_tokens":18,"input_tokens_details":{"cached_tokens":8}}
	}`))
	if err != nil || !response.Usage.CacheReported || response.Usage.CachedInputTokens != 8 {
		t.Fatalf("Responses 缓存统计异常: %+v, %v", response.Usage, err)
	}
}
