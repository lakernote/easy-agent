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

func TestPrepareModelInputRequiresExplicitSecretDecisionAcrossProviders(t *testing.T) {
	current := store.ModelSettings{Provider: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "private"}
	candidate := store.ModelSettings{
		Provider: "openrouter", Protocol: "chat_completions", BaseURL: "https://openrouter.ai/api/v1", Model: "openrouter/free",
		MaxOutputTokens: 100, RequestTimeoutSeconds: 30, CompressionThresholdPercent: 75,
	}
	if _, err := prepareModelInput(candidate, current, false); err == nil || !strings.Contains(err.Error(), "填写新 API Key") {
		t.Fatalf("切换端点且留空时应要求明确处理密钥: %v", err)
	}
	cleared, err := prepareModelInput(candidate, current, true)
	if err != nil || cleared.APIKey != "" {
		t.Fatalf("显式清除旧密钥应允许切换端点: value=%+v err=%v", cleared, err)
	}
	candidate.APIKey = "replacement"
	replaced, err := prepareModelInput(candidate, current, false)
	if err != nil || replaced.APIKey != "replacement" {
		t.Fatalf("填写新密钥应允许切换端点: value=%+v err=%v", replaced, err)
	}
}

func TestPrepareModelInputKeepsSecretForSameProfileEndpoint(t *testing.T) {
	current := store.ModelSettings{Provider: "groq", BaseURL: "https://api.groq.com/openai/v1", APIKey: "private"}
	input, err := prepareModelInput(store.ModelSettings{
		Provider: "groq", Protocol: "chat_completions", BaseURL: "https://api.groq.com/openai/v1/", Model: "changed-model",
		MaxOutputTokens: 200, RequestTimeoutSeconds: 30, CompressionThresholdPercent: 75,
	}, current, false)
	if err != nil || input.APIKey != "private" {
		t.Fatalf("修改普通属性时应保留本配置密钥: value=%+v err=%v", input, err)
	}
}

func TestSaveModelPreservesEditedProfileSecretWithoutActivating(t *testing.T) {
	database, err := store.Open(t.TempDir() + "/easyagent.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	first := store.DefaultModelSettings()
	first.ProfileID, first.ProfileName = "first", "First"
	first.Provider, first.BaseURL, first.Model, first.APIKey = "openai", "https://api.openai.com/v1", "gpt-first", "first-secret"
	second := first
	second.ProfileID, second.ProfileName = "second", "Second"
	second.Provider, second.BaseURL, second.Model, second.APIKey = "groq", "https://api.groq.com/openai/v1", "gpt-second", "second-secret"
	if err := database.SaveModelSettings(first); err != nil {
		t.Fatal(err)
	}
	if err := database.SaveModelSettings(second); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetActiveModelProfile(first.ProfileID); err != nil {
		t.Fatal(err)
	}

	application := &Server{store: database}
	second.APIKey = ""
	second.MaxOutputTokens = 321
	payload, _ := json.Marshal(second)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/model", strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	application.saveModel(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("保存非默认配置失败: HTTP %d %s", response.Code, response.Body.String())
	}
	saved, err := database.GetModelSettingsByProfileID(second.ProfileID)
	if err != nil || saved.APIKey != "second-secret" || saved.MaxOutputTokens != 321 {
		t.Fatalf("非默认配置没有保留自己的密钥: value=%+v err=%v", saved, err)
	}
	active, err := database.GetModelSettings()
	if err != nil || active.ProfileID != first.ProfileID || active.APIKey != "first-secret" {
		t.Fatalf("普通保存不应切换默认配置: value=%+v err=%v", active, err)
	}
	if strings.Contains(response.Body.String(), "second-secret") || !strings.Contains(response.Body.String(), `"secretConfigured":true`) {
		t.Fatalf("响应必须遮蔽密钥并报告已配置: %s", response.Body.String())
	}

	activateRequest := httptest.NewRequest(http.MethodPut, "/api/v1/model/second/active", strings.NewReader(`{}`))
	activateRequest.SetPathValue("id", second.ProfileID)
	activateResponse := httptest.NewRecorder()
	application.activateModelProfile(activateResponse, activateRequest)
	if activateResponse.Code != http.StatusOK {
		t.Fatalf("设为默认失败: HTTP %d %s", activateResponse.Code, activateResponse.Body.String())
	}
	active, err = database.GetModelSettings()
	if err != nil || active.ProfileID != second.ProfileID || active.APIKey != "second-secret" {
		t.Fatalf("设为默认不应改写配置或密钥: value=%+v err=%v", active, err)
	}

	clearPayload, _ := json.Marshal(modelSettingsInput{ModelSettings: second, ClearAPIKey: true})
	clearRequest := httptest.NewRequest(http.MethodPut, "/api/v1/model", strings.NewReader(string(clearPayload)))
	clearRequest.Header.Set("Content-Type", "application/json")
	clearResponse := httptest.NewRecorder()
	application.saveModel(clearResponse, clearRequest)
	if clearResponse.Code != http.StatusOK {
		t.Fatalf("显式清除密钥失败: HTTP %d %s", clearResponse.Code, clearResponse.Body.String())
	}
	cleared, err := database.GetModelSettingsByProfileID(second.ProfileID)
	if err != nil || cleared.APIKey != "" {
		t.Fatalf("显式清除后仍存在密钥: value=%+v err=%v", cleared, err)
	}
}

func TestCodexTurnTimeoutIsIndependentFromRequestTimeout(t *testing.T) {
	input, err := prepareModelInput(store.ModelSettings{
		Runtime:         store.RuntimeCodex,
		MaxOutputTokens: 100,
	}, store.ModelSettings{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if input.TurnTimeoutSeconds != store.DefaultCodexTurnTimeoutSeconds {
		t.Fatalf("Codex 未使用独立的整轮任务默认上限: got %d, want %d", input.TurnTimeoutSeconds, store.DefaultCodexTurnTimeoutSeconds)
	}
	if input.RequestTimeoutSeconds != store.DefaultRequestTimeoutSeconds {
		t.Fatalf("请求超时默认值异常: got %d, want %d", input.RequestTimeoutSeconds, store.DefaultRequestTimeoutSeconds)
	}

	input.TurnTimeoutSeconds = store.MaxCodexTurnTimeoutSeconds
	if err := validateModel(input); err != nil {
		t.Fatalf("最大合法整轮任务上限不应失败: %v", err)
	}
	input.TurnTimeoutSeconds = store.MaxCodexTurnTimeoutSeconds + 1
	if err := validateModel(input); err == nil {
		t.Fatal("超过最大整轮任务上限的配置应失败")
	}
}
