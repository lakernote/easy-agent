package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lakernote/easy-agent/internal/store"
)

func TestRunModelTestVerifiesCompleteToolRoundTrip(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if json.NewDecoder(request.Body).Decode(&body) != nil {
			t.Fatal("模型请求不是 JSON")
		}
		response.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			if body["tool_choice"] != "auto" {
				t.Fatalf("能力测试必须复现 Agent 的 auto 选择: %#v", body["tool_choice"])
			}
			_, _ = response.Write([]byte(`{"id":"one","model":"test","choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"easyagent_diagnostic_echo","arguments":"{\"text\":\"ping\"}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`))
			return
		}
		messages, _ := body["messages"].([]any)
		encoded, _ := json.Marshal(messages)
		if !strings.Contains(string(encoded), "EASYAGENT_OK") || body["tool_choice"] != "none" {
			t.Fatalf("第二次请求没有回传工具结果: %s", encoded)
		}
		_, _ = response.Write([]byte(`{"id":"two","model":"test","choices":[{"message":{"role":"assistant","content":"EASYAGENT_OK"}}],"usage":{"prompt_tokens":16,"completion_tokens":2,"total_tokens":18}}`))
	}))
	defer provider.Close()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/model/test", nil)
	result, err := runModelTest(request, store.ModelSettings{
		Provider: "test", Protocol: "chat_completions", BaseURL: provider.URL, Model: "test",
		MaxOutputTokens: 100, RequestTimeoutSeconds: 30, CompressionThresholdPercent: 75,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.ToolCall != "easyagent_diagnostic_echo" || result.Answer != "EASYAGENT_OK" || result.InputTokens != 26 || result.OutputTokens != 5 || calls.Load() != 2 {
		t.Fatalf("模型能力测试结果不完整: %+v, calls=%d", result, calls.Load())
	}
}

func TestRunModelTestRejectsTextThatPretendsToBeToolCall(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"model":"test","choices":[{"message":{"role":"assistant","content":"{\"name\":\"easyagent_diagnostic_echo\"}"}}]}`))
	}))
	defer provider.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/model/test", nil)
	_, err := runModelTest(request, store.ModelSettings{
		Protocol: "chat_completions", BaseURL: provider.URL, Model: "test", RequestTimeoutSeconds: 30,
	})
	if err == nil || !strings.Contains(err.Error(), "没有返回原生 tool_calls") {
		t.Fatalf("普通 JSON 文本不能被误判为工具调用: %v", err)
	}
}

func TestPrepareModelInputDoesNotMoveSecretAcrossProviders(t *testing.T) {
	current := store.ModelSettings{Provider: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "private"}
	input, err := prepareModelInput(store.ModelSettings{
		Provider: "openrouter", Protocol: "chat_completions", BaseURL: "https://openrouter.ai/api/v1", Model: "openrouter/free",
		MaxOutputTokens: 100, RequestTimeoutSeconds: 30, CompressionThresholdPercent: 75,
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if input.APIKey != "" {
		t.Fatal("切换 Provider 后错误继承了旧密钥")
	}
}
