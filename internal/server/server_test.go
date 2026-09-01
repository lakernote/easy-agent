package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"
	"unicode/utf8"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/appenv"
	"github.com/lakernote/easy-agent/internal/store"
)

func TestTraceOmitsAttachmentBase64(t *testing.T) {
	input := `{"image_url":{"url":"data:image/png;base64,c2VjcmV0"},"text":"keep"}`
	output := redactTraceAttachmentData(input)
	if strings.Contains(output, "c2VjcmV0") || !strings.Contains(output, "image/png attachment data omitted") || !strings.Contains(output, `"text":"keep"`) {
		t.Fatalf("Trace 附件脱敏错误: %s", output)
	}
}

func TestUserTurnCountOnlyCountsUserMessages(t *testing.T) {
	messages := []store.Message{
		{Role: "user"},
		{Role: "assistant"},
		{Role: "assistant"},
		{Role: "tool"},
		{Role: "user"},
	}
	if turns := userTurnCount(messages); turns != 2 {
		t.Fatalf("用户轮次只能由 user 消息计算: %d", turns)
	}
}

func TestSaveSkillValidatesSkillMarkdown(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	application := newTestApplication(t, database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}})
	defer application.Shutdown(context.Background())
	httpServer := httptest.NewServer(application.Handler())
	defer httpServer.Close()

	tests := []struct {
		name    string
		content string
	}{
		{name: "missing-frontmatter", content: "# Instructions"},
		{name: "wrong-name", content: "---\nname: another-skill\ndescription: test\n---\n\n# Instructions"},
	}
	for _, test := range tests {
		payload, _ := json.Marshal(map[string]any{"description": "测试 Skill", "content": test.content, "enabled": true})
		request, _ := http.NewRequest(http.MethodPut, httpServer.URL+"/api/v1/skills/test-skill", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s 应拒绝非法 SKILL.md，实际 HTTP %d", test.name, response.StatusCode)
		}
	}
	validContent := "---\nname: test-skill\ndescription: frontmatter 描述\n---\n\n# Instructions"
	payload, _ := json.Marshal(map[string]any{"description": "表单旧描述", "content": validContent, "enabled": true})
	request, _ := http.NewRequest(http.MethodPut, httpServer.URL+"/api/v1/skills/test-skill", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var saved store.SkillOverride
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&saved) != nil || saved.Description != "frontmatter 描述" {
		t.Fatalf("保存后应使用 frontmatter 元数据: HTTP=%d value=%+v", response.StatusCode, saved)
	}
}

func TestSelectedSkillsUsesExplicitMentionOnly(t *testing.T) {
	catalog := &skillCatalog{items: []store.SkillOverride{{Name: "api-design", Description: "API", Content: "instructions", Enabled: true}}}
	tests := []struct {
		message string
		want    int
	}{
		{"请设计接口", 0},
		{"@tool:calculate 请计算 9*9", 0},
		{"@skill:api-design 设计接口", 1},
		{"@skill:missing 不存在", 0},
		{"@skill:api-design @skill:api-design", 1},
	}
	for _, test := range tests {
		selected := selectedSkills([]store.Message{{Role: "user", Content: test.message}}, catalog)
		if len(selected) != test.want {
			t.Fatalf("mention %q 选择错误: got=%d want=%d", test.message, len(selected), test.want)
		}
	}
}

func TestSelectedMCPIDsUsesExplicitMentionOnly(t *testing.T) {
	tests := []struct {
		message string
		want    []string
	}{
		{"请打开浏览器", nil},
		{"@mcp:playwright 打开页面", []string{"playwright"}},
		{"@mcp:playwright @mcp:playwright 截图", []string{"playwright"}},
	}
	for _, test := range tests {
		got := selectedMCPIDs([]store.Message{{Role: "user", Content: test.message}})
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("mention %q 选择错误: got=%v want=%v", test.message, got, test.want)
		}
	}
}

func TestDecodeJSONRejectsTrailingData(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api", strings.NewReader(`{"message":"ok"}{"extra":true}`))
	response := httptest.NewRecorder()
	var value map[string]string
	if decodeJSON(response, request, &value) {
		t.Fatal("尾随 JSON 不应被接受")
	}
	if response.Code != http.StatusBadRequest {
		t.Fatalf("尾随 JSON 应返回 400，实际 %d", response.Code)
	}
}

func TestGetSessionUsesBoundedHistoryWindow(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.CreateSession("window", "窗口", "fixture", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	messages := make([]store.Message, 0, 201)
	for index := 0; index < 201; index++ {
		role := "assistant"
		if index%2 == 0 {
			role = "user"
		}
		messages = append(messages, store.Message{Role: role, Content: fmt.Sprintf("消息 %d", index)})
	}
	if err := database.AppendMessages("window", messages); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 301; index++ {
		if err := database.AppendEvent("window", store.Event{Kind: "trace", Detail: fmt.Sprintf("事件 %d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	application := newTestApplication(t, database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}})
	defer application.Shutdown(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/window", nil)
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("会话窗口接口状态错误: %d %s", response.Code, response.Body.String())
	}
	var value store.Session
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	if len(value.Messages) != 200 || value.MessageCount != 201 || !value.MessagesTruncated || value.Messages[0].Content != "消息 1" || value.Messages[199].Content != "消息 200" {
		t.Fatalf("会话消息没有按最近窗口返回: count=%d truncated=%v first=%q last=%q", value.MessageCount, value.MessagesTruncated, value.Messages[0].Content, value.Messages[199].Content)
	}
	if len(value.Events) != 300 || value.EventCount != 301 || !value.EventsTruncated || value.Events[0].Detail != "事件 1" || value.Events[299].Detail != "事件 300" {
		t.Fatalf("会话 Trace 没有按最近窗口返回: count=%d truncated=%v", value.EventCount, value.EventsTruncated)
	}
}

func TestShellKeepsRawPathForAgentAndTrace(t *testing.T) {
	workingDirectory := t.TempDir()
	workingDirectory, _ = filepath.EvalSymlinks(workingDirectory)
	callCount := 0
	modelServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		callCount++
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if callCount == 1 {
			toolName := "load_tools"
			arguments := `{"groups":["execution"]}`
			tools, _ := body["tools"].([]any)
			hasLoader := false
			for _, rawTool := range tools {
				if item, ok := rawTool.(map[string]any); ok {
					if function, ok := item["function"].(map[string]any); ok && function["name"] == "load_tools" {
						hasLoader = true
					}
				}
			}
			if !hasLoader {
				toolName = "shell"
				arguments = `{"command":"pwd"}`
			}
			_ = json.NewEncoder(response).Encode(map[string]any{
				"id": "load-call", "model": "fixture",
				"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
					map[string]any{"id": "call-load", "type": "function", "function": map[string]any{"name": toolName, "arguments": arguments}},
				}}}},
			})
			return
		}
		messages := body["messages"].([]any)
		toolMessage := messages[len(messages)-1].(map[string]any)
		toolOutput, _ := toolMessage["content"].(string)
		if callCount == 2 {
			if strings.Contains(toolOutput, `"loaded_groups":["execution"]`) {
				if toolMessage["role"] != "tool" || !strings.Contains(toolOutput, `"shell"`) {
					t.Fatalf("模型没有收到工具加载结果: %+v", toolMessage)
				}
				_ = json.NewEncoder(response).Encode(map[string]any{
					"id": "shell-call", "model": "fixture",
					"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
						map[string]any{"id": "call-shell", "type": "function", "function": map[string]any{"name": "shell", "arguments": `{"command":"pwd"}`}},
					}}}},
				})
				return
			}
			if toolMessage["role"] != "tool" || !strings.Contains(toolOutput, workingDirectory) || strings.Contains(toolOutput, "<workspace>") {
				t.Fatalf("模型没有收到 Shell 原始路径: %+v", toolMessage)
			}
			_ = json.NewEncoder(response).Encode(map[string]any{
				"id": "shell-answer", "model": "fixture",
				"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "目录是 " + workingDirectory}}},
			})
			return
		}
		if toolMessage["role"] != "tool" || !strings.Contains(toolOutput, workingDirectory) || strings.Contains(toolOutput, "<workspace>") {
			t.Fatalf("模型没有收到 Shell 原始路径: %+v", toolMessage)
		}
		_ = json.NewEncoder(response).Encode(map[string]any{
			"id": "shell-answer", "model": "fixture",
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "目录是 " + workingDirectory}}},
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
	environment, err := appenv.Open(appenv.Config{Home: filepath.Join(t.TempDir(), "home")})
	if err != nil {
		t.Fatal(err)
	}
	application := New(database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}, environment)
	defer application.Shutdown(context.Background())
	httpServer := httptest.NewServer(application.Handler())
	defer httpServer.Close()

	payload, _ := json.Marshal(map[string]string{"message": "执行 pwd 并原样回答", "workspace": workingDirectory})
	response, err := http.Post(httpServer.URL+"/api/v1/sessions", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var queued store.Session
	if err := json.NewDecoder(response.Body).Decode(&queued); err != nil {
		t.Fatal(err)
	}
	finished := waitSession(t, database, queued.ID)
	if finished.Workspace != workingDirectory || finished.Status != "idle" || !strings.Contains(finished.Messages[len(finished.Messages)-1].Content, workingDirectory) {
		t.Fatalf("Shell 原始路径任务没有完成: %+v", finished)
	}
	modelResponses := 0
	for _, event := range finished.Events {
		if event.Kind == "model_end" {
			modelResponses++
			if event.StatusCode != http.StatusOK {
				t.Fatalf("模型 Trace 缺少真实 HTTP 状态码: %+v", event)
			}
		}
		if event.Kind == "tool_end" && event.Name == "shell" && (!strings.Contains(event.Output, workingDirectory) || strings.Contains(event.Output, "<workspace>")) {
			t.Fatalf("Shell Trace 没有保留真实路径: %s", event.Output)
		}
	}
	if modelResponses != 3 {
		t.Fatalf("Shell 任务应有加载工具、执行工具和最终回答三次模型响应，实际 %d", modelResponses)
	}
}

func TestAttachmentRejectsSpoofedImageMIME(t *testing.T) {
	_, _, err := classifyAttachment("fake.png", "image/png", []byte("not an image"))
	if err == nil || !strings.Contains(err.Error(), "不是有效的图片") {
		t.Fatalf("伪装图片应该被拒绝，实际错误: %v", err)
	}
}

func TestMessageAttachmentsReachModelAndDownloadEndpoint(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		messages := body["messages"].([]any)
		user := messages[len(messages)-1].(map[string]any)
		encoded, _ := json.Marshal(user["content"])
		if !strings.Contains(string(encoded), `"type":"image_url"`) || !strings.Contains(string(encoded), "error.log") || !strings.Contains(string(encoded), "stack trace") {
			t.Fatalf("附件没有进入模型多模态消息: %s", encoded)
		}
		_ = json.NewEncoder(response).Encode(map[string]any{
			"id": "attachment-test", "model": "fixture",
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "已分析"}}},
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
	application := newTestApplication(t, database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}})
	defer application.Shutdown(context.Background())
	httpServer := httptest.NewServer(application.Handler())
	defer httpServer.Close()

	png := []byte("\x89PNG\r\n\x1a\n")
	payload, _ := json.Marshal(map[string]any{"message": "找问题", "attachments": []any{
		map[string]any{"name": "screen.png", "mimeType": "image/png", "size": len(png), "data": base64.StdEncoding.EncodeToString(png)},
		map[string]any{"name": "error.log", "mimeType": "text/plain", "size": 11, "data": base64.StdEncoding.EncodeToString([]byte("stack trace"))},
	}})
	response, err := http.Post(httpServer.URL+"/api/v1/sessions", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("附件消息状态错误: %d", response.StatusCode)
	}
	var queued store.Session
	if err := json.NewDecoder(response.Body).Decode(&queued); err != nil {
		t.Fatal(err)
	}
	finished := waitSession(t, database, queued.ID)
	if finished.Status != "idle" || len(finished.Messages[0].Attachments) != 2 {
		t.Fatalf("附件会话没有完成: %+v", finished)
	}
	for _, event := range finished.Events {
		if event.Kind == "model_end" && strings.Contains(event.Input, base64.StdEncoding.EncodeToString(png)) {
			t.Fatalf("Trace 不应保存附件 Base64: %s", event.Input)
		}
	}
	attachment := finished.Messages[0].Attachments[0]
	download, err := http.Get(httpServer.URL + "/api/v1/attachments/" + attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer download.Body.Close()
	if download.StatusCode != http.StatusOK || download.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("附件下载接口异常: %d %s", download.StatusCode, download.Header.Get("Content-Type"))
	}
}

// TestUnknownAPIIsNotSPA 确保接口地址拼错时返回可识别的 JSON 404，
// 而不是被前端 history fallback 吞掉后返回 index.html 和 200。
func TestUnknownAPIIsNotSPA(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	application := newTestApplication(t, database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("frontend")}})
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
	environment, err := appenv.Open(appenv.Config{Home: filepath.Join(t.TempDir(), "home")})
	if err != nil {
		t.Fatal(err)
	}
	application := New(database, assets, environment)
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
	application := newTestApplication(t, database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}})
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
	application := newTestApplication(t, database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}})
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
	application := newTestApplication(t, database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}})
	defer application.Shutdown(context.Background())
	httpServer := httptest.NewServer(application.Handler())
	defer httpServer.Close()

	failed := waitSession(t, database, postMessage(t, httpServer.URL+"/api/v1/sessions", "触发失败").ID)
	if failed.Status != "failed" || failed.Usage.ModelCalls != 2 {
		t.Fatalf("失败调用及统计未闭环: %+v", failed)
	}
	if len(failed.Events) < 4 || failed.Events[len(failed.Events)-1].Status != "error" || failed.Events[len(failed.Events)-1].Name != "fixture" || failed.Events[len(failed.Events)-1].Attempt != 2 {
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
	settings := store.ModelSettings{ContextWindowTokens: 1000, CompressionThresholdPercent: 75}
	plan := makeCompactionPlan(session, settings, "system", nil, runtimeCompactionThreshold(settings), false)
	if plan == nil {
		t.Fatal("达到阈值后应生成压缩计划")
	}
	if plan.ThroughMessageID != 4 || plan.SourceMessages != 4 || plan.CompactedMessages != 4 {
		t.Fatalf("压缩边界错误: %+v", plan)
	}
}

func TestCompactionThresholdDoesNotReserveConfiguredOutput(t *testing.T) {
	shortOutput := store.ModelSettings{ContextWindowTokens: 4096, MaxOutputTokens: 200, CompressionThresholdPercent: 75}
	longOutput := store.ModelSettings{ContextWindowTokens: 4096, MaxOutputTokens: 2000, CompressionThresholdPercent: 75}
	if runtimeCompactionThreshold(shortOutput) != runtimeCompactionThreshold(longOutput) {
		t.Fatal("配置的理论输出上限不应改变输入压缩阈值")
	}
}

func TestRuntimeThresholdDoesNotWasteSmallContextWindow(t *testing.T) {
	settings := store.ModelSettings{ContextWindowTokens: 4096, MaxOutputTokens: 1600, CompressionThresholdPercent: 75}
	if threshold := runtimeCompactionThreshold(settings); threshold != 3072 {
		t.Fatalf("4K 窗口不应为理论输出上限提前压缩，得到阈值 %d", threshold)
	}
}

func TestRuntimeOutputBudgetShrinksOnlyCurrentRequest(t *testing.T) {
	settings := store.ModelSettings{ContextWindowTokens: 1000}
	request := agent.Request{MaxOutputTokens: 600, Messages: []agent.Message{{Role: agent.RoleUser, Content: strings.Repeat("问题", 300)}}}
	fitted := fitRuntimeOutputBudget(request, settings)
	if fitted.MaxOutputTokens <= 0 || fitted.MaxOutputTokens >= request.MaxOutputTokens {
		t.Fatalf("接近窗口时应收紧当前输出预算: before=%d after=%d", request.MaxOutputTokens, fitted.MaxOutputTokens)
	}
	if request.MaxOutputTokens != 600 {
		t.Fatal("不应修改原始请求或模型配置")
	}
}

func TestForcedCompactionIgnoresStaleLocalEstimate(t *testing.T) {
	now := time.Now()
	settings := store.ModelSettings{ContextWindowTokens: 4096, MaxOutputTokens: 200}
	session := store.Session{
		Messages: []store.Message{
			{ID: 1, Role: "user", Content: strings.Repeat("旧问题", 600), CreatedAt: now},
			{ID: 2, Role: "assistant", Content: strings.Repeat("旧回答", 600), CreatedAt: now},
			{ID: 3, Role: "user", Content: "当前问题", CreatedAt: now},
		},
		Events: []store.Event{{Kind: "model_end", InputTokens: 10, CreatedAt: now.Add(-time.Second)}},
	}
	if plan := makeCompactionPlan(session, settings, "system", nil, runtimeCompactionThreshold(settings), true); plan == nil {
		t.Fatal("Provider 已明确报超限时，不能被本地低估值阻止压缩")
	}
}

func TestCompactionPlanSplitsSingleLargeTurnAtAssistantBoundary(t *testing.T) {
	now := time.Now()
	session := store.Session{
		Messages: []store.Message{
			{ID: 1, Role: "user", Content: "执行任务", CreatedAt: now},
			{ID: 2, Role: "assistant", ToolCalls: []store.ToolCall{{ID: "call-1", Name: "shell", Arguments: "{}"}}, CreatedAt: now},
			{ID: 3, Role: "tool", ToolCallID: "call-1", Content: strings.Repeat("工具输出", 2000), CreatedAt: now},
		},
		Events: []store.Event{{Kind: "model_end", InputTokens: 900, CreatedAt: now.Add(-time.Second)}},
	}
	settings := store.ModelSettings{ContextWindowTokens: 1000, CompressionThresholdPercent: 75}
	plan := makeCompactionPlan(session, settings, "system", nil, runtimeCompactionThreshold(settings), false)
	if plan == nil || !plan.SplitTurn {
		t.Fatalf("单轮大上下文应生成 split-turn 计划: %+v", plan)
	}
	if plan.ThroughMessageID != 1 || plan.SourceMessages != 1 || plan.CompactedMessages != 1 {
		t.Fatalf("split-turn 压缩边界错误: %+v", plan)
	}
	if len(session.Messages[plan.SourceMessages:]) == 0 || session.Messages[plan.SourceMessages].Role != "assistant" {
		t.Fatalf("split-turn 必须从 assistant 开始保留: %+v", session.Messages)
	}
	if !validCompactionSuffix(session.Messages[plan.SourceMessages:]) {
		t.Fatal("保留的 assistant/tool 后缀不应出现悬空调用")
	}
}

func TestSplitTurnCheckpointRebuildsProtocolSafeMessages(t *testing.T) {
	session := store.Session{
		Messages: []store.Message{
			{ID: 1, Role: "user", Content: "前半段"},
			{ID: 2, Role: "assistant", ToolCalls: []store.ToolCall{{ID: "call-1", Name: "shell", Arguments: "{}"}}},
			{ID: 3, Role: "tool", ToolCallID: "call-1", Content: "结果"},
		},
		Compactions: []store.Compaction{{Summary: "已完成前半段", ThroughMessageID: 1, SplitTurn: true}},
	}
	messages := coreMessagesForSession(session, "系统提示")
	if len(messages) != 4 || messages[0].Role != agent.RoleSystem || messages[1].Role != agent.RoleUser || messages[2].Role != agent.RoleAssistant || messages[3].Role != agent.RoleTool {
		t.Fatalf("split-turn 恢复后的消息协议顺序错误: %+v", messages)
	}
	if !strings.Contains(messages[1].Content, "当前轮次的前半段已压缩") {
		t.Fatalf("split-turn checkpoint 缺少边界说明: %q", messages[1].Content)
	}
}

func TestCoreMessagesDropIncompleteStoredToolChain(t *testing.T) {
	session := store.Session{Messages: []store.Message{
		{ID: 1, Role: "user", Content: "查询"},
		{ID: 2, Role: "assistant", ToolCalls: []store.ToolCall{{ID: "call-1", Name: "lookup", Arguments: "{}"}}},
		{ID: 3, Role: "user", Content: "后续问题"},
	}}
	messages := coreMessagesForSession(session, "系统提示")
	if len(messages) != 2 || messages[1].Role != agent.RoleUser || messages[1].Content != "查询" {
		t.Fatalf("未闭合工具链不应继续发送，实际消息: %+v", messages)
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
	settings := store.ModelSettings{ContextWindowTokens: 1000, CompressionThresholdPercent: 75}
	plan := makeCompactionPlan(session, settings, "system", nil, runtimeCompactionThreshold(settings), false)
	if plan == nil || plan.PreviousSummary != "旧检查点" || plan.ThroughMessageID != 4 || plan.CompactedMessages != 4 {
		t.Fatalf("重复压缩没有更新旧检查点: %+v", plan)
	}
}

func TestMicroCompactOldToolResultKeepsHeadAndTail(t *testing.T) {
	large := strings.Repeat("头", 1500) + strings.Repeat("中", 1500) + strings.Repeat("尾", 1500)
	messages := []agent.Message{
		{Role: agent.RoleTool, Content: large},
		{Role: agent.RoleUser, Content: "当前问题"},
	}
	compacted, changed := microCompactAgentMessages(messages)
	if changed || compacted[0].Content != large {
		t.Fatalf("最近消息不应被微压缩: changed=%v", changed)
	}
	messages = append([]agent.Message{{Role: agent.RoleTool, Content: large}}, make([]agent.Message, runtimeRecentMessages)...)
	compacted, changed = microCompactAgentMessages(messages)
	if !changed || len([]rune(compacted[0].Content)) >= len([]rune(large)) {
		t.Fatalf("旧工具结果未被微压缩: changed=%v old=%d new=%d", changed, len([]rune(large)), len([]rune(compacted[0].Content)))
	}
	if !strings.Contains(compacted[0].Content, "头") || !strings.Contains(compacted[0].Content, "尾") || !utf8.ValidString(compacted[0].Content) {
		t.Fatalf("微压缩结果没有保留头尾或破坏 UTF-8: %q", compacted[0].Content)
	}
}

func TestCompactOversizedRecentToolResultKeepsProtocolAndBounds(t *testing.T) {
	large := strings.Repeat("头", 3000) + strings.Repeat("尾", 3000)
	messages := []agent.Message{
		{Role: agent.RoleSystem, Content: "系统"},
		{Role: agent.RoleUser, Content: "checkpoint"},
		{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "shell", Arguments: []byte("{}")}}},
		{Role: agent.RoleTool, ToolCallID: "call-1", Content: large},
	}
	compacted, changed := compactOversizedToolResults(messages)
	if !changed || len([]rune(compacted[3].Content)) >= len([]rune(large)) {
		t.Fatalf("最近的大工具结果没有被请求级截断: changed=%v", changed)
	}
	if !strings.Contains(compacted[3].Content, "头") || !strings.Contains(compacted[3].Content, "尾") || !utf8.ValidString(compacted[3].Content) {
		t.Fatal("工具结果截断没有保留头尾或破坏 UTF-8")
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

func newTestApplication(t *testing.T, database *store.Store, assets fstest.MapFS) *Server {
	t.Helper()
	environment, err := appenv.Open(appenv.Config{Home: filepath.Join(t.TempDir(), "home")})
	if err != nil {
		t.Fatal(err)
	}
	return New(database, assets, environment)
}
