package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
	"unicode/utf8"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/store"
)

// TestUnknownAPIIsNotSPA 确保接口地址拼错时返回可识别的 JSON 404，
// 而不是被前端 history fallback 吞掉后返回 index.html 和 200。
func TestUnknownAPIIsNotSPA(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	application := New(database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("frontend")}})
	defer application.Shutdown(context.Background())

	request := httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil)
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("未知 API 状态码错误: %d", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("未知 API 应返回 JSON，实际为 %q", contentType)
	}
	if strings.Contains(response.Body.String(), "frontend") || !strings.Contains(response.Body.String(), "API 不存在") {
		t.Fatalf("未知 API 响应异常: %s", response.Body.String())
	}
}

// TestMultiTurnSession 从 HTTP API 一直走到模型适配器、Runner 和 SQLite，
// 确认第二轮不是新任务，而是在同一条标准消息历史上继续。
func TestMultiTurnSession(t *testing.T) {
	modelCalls := 0
	modelServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		modelCalls++
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if modelCalls == 2 && len(body.Messages) < 4 {
			t.Fatalf("第二轮没有携带完整历史: %+v", body.Messages)
		}
		answer := "第一轮回答"
		if modelCalls == 2 {
			answer = "第二轮回答"
		}
		_ = json.NewEncoder(response).Encode(map[string]any{
			"id": "chat-test", "model": "fixture",
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": answer}}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12},
		})
	}))
	defer modelServer.Close()

	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.SaveModel(store.ModelSettings{Provider: "test", Protocol: "chat_completions", BaseURL: modelServer.URL, Model: "fixture", MaxOutputTokens: 200}); err != nil {
		t.Fatal(err)
	}
	assets := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}
	application := New(database, assets)
	defer application.Shutdown(context.Background())
	httpServer := httptest.NewServer(application.Handler())
	defer httpServer.Close()

	first := postMessage(t, httpServer.URL+"/api/v1/sessions", "记住数字 7")
	first = waitSession(t, database, first.ID)
	if first.Status != "idle" || len(first.Messages) != 2 || first.Messages[1].Content != "第一轮回答" {
		t.Fatalf("第一轮异常: %+v", first)
	}
	second := postMessage(t, httpServer.URL+"/api/v1/sessions/"+first.ID+"/messages", "刚才的数字是什么？")
	second = waitSession(t, database, second.ID)
	if second.Status != "idle" || len(second.Messages) != 4 || second.Messages[3].Content != "第二轮回答" {
		t.Fatalf("第二轮异常: %+v", second)
	}
	if second.Usage.ModelCalls != 2 || second.Usage.TotalTokens != 24 {
		t.Fatalf("Usage 未累计: %+v", second.Usage)
	}
}

func TestLongSessionCreatesCompactionCheckpoint(t *testing.T) {
	modelCalls := 0
	normalCalls := 0
	finalRequestChecked := false
	modelServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		modelCalls++
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		isCompaction := len(body.Messages) > 0 && strings.HasPrefix(body.Messages[0].Content, "你正在为同一个 EasyAgent 创建")
		answer, promptTokens, completionTokens := "", 0, 10
		if isCompaction {
			answer, promptTokens, completionTokens = "## 目标\n- 继续记住第一轮信息\n\n## 下一步\n1. 回答当前问题", 110, 20
		} else {
			normalCalls++
			answer, promptTokens = strings.Repeat("第一轮回答", 50), 60
			if normalCalls == 2 {
				answer, promptTokens = strings.Repeat("第二轮回答", 50), 95
			}
		}
		if !isCompaction && normalCalls >= 3 {
			encoded, _ := json.Marshal(body.Messages)
			requestText := string(encoded)
			if strings.Contains(requestText, "第一轮机密") {
				t.Fatalf("压缩后不应再次发送已由摘要表示的第一轮原文: %s", requestText)
			}
			if !strings.Contains(requestText, "此前会话的上下文检查点") || !strings.Contains(requestText, "第三轮问题") {
				t.Fatalf("压缩后请求缺少摘要或最近消息: %s", requestText)
			}
			finalRequestChecked = true
			answer, promptTokens = "第三轮回答", 90
		}
		_ = json.NewEncoder(response).Encode(map[string]any{
			"id": fmt.Sprintf("chat-%d", modelCalls), "model": "fixture",
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": answer}}},
			"usage":   map[string]any{"prompt_tokens": promptTokens, "completion_tokens": completionTokens, "total_tokens": promptTokens + completionTokens},
		})
	}))
	defer modelServer.Close()

	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.SaveModel(store.ModelSettings{
		Provider: "test", Protocol: "chat_completions", BaseURL: modelServer.URL, Model: "fixture",
		MaxOutputTokens: 50, ContextWindowTokens: 200, CompressionThresholdPercent: 50,
	}); err != nil {
		t.Fatal(err)
	}
	application := New(database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}})
	defer application.Shutdown(context.Background())
	httpServer := httptest.NewServer(application.Handler())
	defer httpServer.Close()

	first := waitSession(t, database, postMessage(t, httpServer.URL+"/api/v1/sessions", "第一轮机密").ID)
	second := waitSession(t, database, postMessage(t, httpServer.URL+"/api/v1/sessions/"+first.ID+"/messages", "第二轮问题").ID)
	third := waitSession(t, database, postMessage(t, httpServer.URL+"/api/v1/sessions/"+second.ID+"/messages", "第三轮问题").ID)
	if !finalRequestChecked || modelCalls != 5 || normalCalls != 3 {
		t.Fatalf("应执行三次正常调用和两次压缩调用: calls=%d normal=%d checked=%v", modelCalls, normalCalls, finalRequestChecked)
	}
	if len(third.Compactions) != 2 || third.Compactions[1].ThroughMessageID != second.Messages[3].ID {
		t.Fatalf("SQLite 压缩检查点异常: %+v", third.Compactions)
	}
	if third.Usage.ModelCalls != 5 || third.Usage.TotalTokens != 535 {
		t.Fatalf("压缩调用必须计入 Usage: %+v", third.Usage)
	}
	compactionTraces := 0
	for _, event := range third.Events {
		if event.Kind == "compaction_end" && event.Status == "success" && event.InputTokens == 110 {
			compactionTraces++
		}
	}
	if compactionTraces != 2 {
		t.Fatalf("Trace 缺少压缩模型调用: %+v", third.Events)
	}
}

func TestRunningSessionCanBeCanceled(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	modelServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started <- struct{}{}
		select {
		case <-request.Context().Done():
		case <-release:
		}
		_ = json.NewEncoder(response).Encode(map[string]any{
			"id": "late-response", "model": "fixture",
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "不应覆盖取消状态"}}},
		})
	}))
	defer modelServer.Close()

	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.SaveModel(store.ModelSettings{Provider: "test", Protocol: "chat_completions", BaseURL: modelServer.URL, Model: "fixture", MaxOutputTokens: 200}); err != nil {
		t.Fatal(err)
	}
	application := New(database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}})
	defer application.Shutdown(context.Background())
	httpServer := httptest.NewServer(application.Handler())
	defer httpServer.Close()

	session := postMessage(t, httpServer.URL+"/api/v1/sessions", "执行一个长任务")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("模型任务没有开始")
	}
	response, err := http.Post(httpServer.URL+"/api/v1/sessions/"+session.ID+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("取消接口状态异常: %d", response.StatusCode)
	}
	var canceled store.Session
	if err := json.NewDecoder(response.Body).Decode(&canceled); err != nil {
		t.Fatal(err)
	}
	if canceled.Status != "canceled" {
		t.Fatalf("任务未进入 canceled: %+v", canceled)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for application.hasTask(session.ID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	stored, _ := database.Session(session.ID)
	if stored.Status != "canceled" {
		t.Fatalf("后台请求结束后不应覆盖 canceled: %+v", stored)
	}
	if stored.Usage.ModelCalls != 1 {
		t.Fatalf("已取消的模型调用仍应计入统计: %+v", stored.Usage)
	}
}

func TestFailedModelCallPersistsAttemptAndTrace(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Error(response, "provider unavailable", http.StatusServiceUnavailable)
	}))
	defer modelServer.Close()

	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.SaveModel(store.ModelSettings{Provider: "test", Protocol: "chat_completions", BaseURL: modelServer.URL, Model: "fixture", MaxOutputTokens: 200}); err != nil {
		t.Fatal(err)
	}
	application := New(database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}})
	defer application.Shutdown(context.Background())
	httpServer := httptest.NewServer(application.Handler())
	defer httpServer.Close()

	failed := waitSession(t, database, postMessage(t, httpServer.URL+"/api/v1/sessions", "触发失败").ID)
	if failed.Status != "failed" || failed.Usage.ModelCalls != 1 {
		t.Fatalf("失败调用及统计未闭环: %+v", failed)
	}
	if len(failed.Events) < 2 || failed.Events[len(failed.Events)-1].Status != "error" || failed.Events[len(failed.Events)-1].Name != "fixture" {
		t.Fatalf("失败 Trace 缺少模型或错误信息: %+v", failed.Events)
	}
	for _, event := range failed.Events {
		if event.ID <= 0 {
			t.Fatalf("Trace ID 必须来自 SQLite: %+v", event)
		}
	}
}

func TestOllamaContextUsesCurrentlyLoadedWindow(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/ps" {
			t.Fatalf("不应读取模型理论元数据: %s", request.URL.Path)
		}
		_ = json.NewEncoder(response).Encode(map[string]any{"models": []any{map[string]any{
			"name": "qwen3:14b", "model": "qwen3:14b", "context_length": 16384,
		}}})
	}))
	defer ollama.Close()
	model := enrichOllamaContextWindow(context.Background(), store.ModelSettings{
		Provider: "ollama", BaseURL: ollama.URL + "/v1", Model: "qwen3:14b", ContextWindowTokens: 40960,
	})
	if model.ContextWindowTokens != 16384 {
		t.Fatalf("应以 Ollama 当前实际窗口覆盖理论值: %+v", model)
	}
}

func TestContextLedgerUsesLatestModelEvent(t *testing.T) {
	session := store.Session{
		Messages: []store.Message{{Role: "user"}, {Role: "assistant"}, {Role: "user"}},
		Events: []store.Event{
			{Kind: "model_end", InputTokens: 100, CachedTokens: 20, CacheReported: true, HistoryMode: "full_history", RequestMessages: 2, ToolDefinitions: 5},
			{Kind: "model_end", InputTokens: 180, CachedTokens: 80, CacheWriteTokens: 10, CacheReported: true, HistoryMode: "provider_continuation", RequestMessages: 1, ToolDefinitions: 6},
		},
	}
	decorateContext(&session, store.ModelSettings{Protocol: "responses", ContextWindowTokens: 32000})
	if session.Context.UserTurns != 2 || session.Context.HistoryMessages != 3 || session.Context.LastInputTokens != 180 {
		t.Fatalf("上下文消息统计异常: %+v", session.Context)
	}
	if session.Context.HistoryMode != "provider_continuation" || !session.Context.CacheReported || session.Context.LastCachedTokens != 80 {
		t.Fatalf("最近模型调用统计异常: %+v", session.Context)
	}
	if session.Context.CompressionMode != "auto" || session.Context.CompressionThresholdPercent != 75 || session.Context.ContextWindowTokens != 32000 {
		t.Fatalf("压缩和窗口状态异常: %+v", session.Context)
	}
}

func TestCompactionPlanKeepsRecentTurns(t *testing.T) {
	now := time.Now()
	session := store.Session{
		Messages: []store.Message{
			{ID: 1, Role: "user", Content: strings.Repeat("旧问题", 80), CreatedAt: now},
			{ID: 2, Role: "assistant", Content: strings.Repeat("旧回答", 80), CreatedAt: now},
			{ID: 3, Role: "user", Content: strings.Repeat("中间问题", 80), CreatedAt: now},
			{ID: 4, Role: "assistant", Content: strings.Repeat("中间回答", 80), CreatedAt: now},
			{ID: 5, Role: "user", Content: "当前问题", CreatedAt: now},
		},
		Events: []store.Event{{Kind: "model_end", InputTokens: 900, CreatedAt: now.Add(-time.Second)}},
	}
	plan := makeCompactionPlan(session, store.ModelSettings{ContextWindowTokens: 1000, CompressionThresholdPercent: 75}, "system", nil)
	if plan == nil {
		t.Fatal("达到阈值后应生成压缩计划")
	}
	if plan.ThroughMessageID != 4 || plan.SourceMessages != 4 || plan.CompactedMessages != 4 {
		t.Fatalf("压缩边界错误: %+v", plan)
	}
}

func TestSummaryTruncationKeepsUTF8Valid(t *testing.T) {
	result := limitSummaryText(strings.Repeat("中", 10), 5)
	if !utf8.ValidString(result) || !strings.HasPrefix(result, "中中中中中") {
		t.Fatalf("摘要截断破坏了 UTF-8: %q", result)
	}
}

func TestPromptCacheKeyOnlyTargetsOpenAI(t *testing.T) {
	if key := promptCacheKey(store.ModelSettings{Provider: "custom", BaseURL: "https://api.openai.com/v1"}); key != "easyagent-core-v1" {
		t.Fatalf("OpenAI 应使用稳定缓存键: %q", key)
	}
	if key := promptCacheKey(store.ModelSettings{Provider: "openai", BaseURL: "https://compatible.example.com/v1"}); key != "" {
		t.Fatalf("兼容 Provider 不应收到它可能不支持的缓存字段: %q", key)
	}
}

func TestModelSecretIsOnlyKeptForSameEndpoint(t *testing.T) {
	current := store.ModelSettings{Provider: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "secret"}
	if !sameModelEndpoint(store.ModelSettings{Provider: "OpenAI", BaseURL: "https://api.openai.com/v1/"}, current) {
		t.Fatal("同一模型端点应允许保留已有密钥")
	}
	if sameModelEndpoint(store.ModelSettings{Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1"}, current) {
		t.Fatal("切换模型服务时不能继承旧密钥")
	}
}

func TestOllamaURLCanBeConfigured(t *testing.T) {
	t.Setenv("EASYAGENT_OLLAMA_URL", "models.internal:11434/")
	if value := ollamaServerURL(); value != "http://models.internal:11434" {
		t.Fatalf("Ollama 地址没有正确归一化: %q", value)
	}
}

func TestShortContinuationIsExpandedOnlyForModel(t *testing.T) {
	expanded := expandContinuation(agent.RoleUser, "继续")
	if expanded == "继续" || !strings.Contains(expanded, "紧邻上一轮") || !strings.Contains(expanded, "不要询问") {
		t.Fatalf("短续写指令没有得到明确上下文: %q", expanded)
	}
	if value := expandContinuation(agent.RoleUser, "继续修复登录问题"); value != "继续修复登录问题" {
		t.Fatalf("有明确目标的消息不应被改写: %q", value)
	}
	if value := expandContinuation(agent.RoleAssistant, "继续"); value != "继续" {
		t.Fatalf("只允许扩展用户消息: %q", value)
	}
}

func TestRepeatedCompactionUpdatesPreviousCheckpoint(t *testing.T) {
	now := time.Now()
	session := store.Session{
		Messages: []store.Message{
			{ID: 1, Role: "user", Content: "已压缩问题", CreatedAt: now},
			{ID: 2, Role: "assistant", Content: "已压缩回答", CreatedAt: now},
			{ID: 3, Role: "user", Content: strings.Repeat("新增旧问题", 80), CreatedAt: now},
			{ID: 4, Role: "assistant", Content: strings.Repeat("新增旧回答", 80), CreatedAt: now},
			{ID: 5, Role: "user", Content: "当前问题", CreatedAt: now},
		},
		Compactions: []store.Compaction{{Summary: "旧检查点", ThroughMessageID: 2, CompactedMessages: 2}},
		Events:      []store.Event{{Kind: "model_end", InputTokens: 900, CreatedAt: now.Add(-time.Second)}},
	}
	plan := makeCompactionPlan(session, store.ModelSettings{ContextWindowTokens: 1000, CompressionThresholdPercent: 75}, "system", nil)
	if plan == nil || plan.PreviousSummary != "旧检查点" || plan.ThroughMessageID != 4 || plan.CompactedMessages != 4 {
		t.Fatalf("重复压缩没有更新旧检查点: %+v", plan)
	}
}

func postMessage(t *testing.T, url, message string) store.Session {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"message": message})
	response, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("unexpected status %d", response.StatusCode)
	}
	var value store.Session
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func waitSession(t *testing.T, database *store.Store, id string) store.Session {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		value, err := database.Session(id)
		if err != nil {
			t.Fatal(err)
		}
		if value.Status != "queued" && value.Status != "running" {
			return value
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("等待 Agent 超时")
	return store.Session{}
}
