package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lakernote/easy-agent/internal/appenv"
	"github.com/lakernote/easy-agent/internal/store"
	"github.com/lakernote/easy-agent/internal/weixin"
)

type fakeWeixinGateway struct {
	mu            sync.Mutex
	count         int
	media         []byte
	downloadCalls int
}

func (gateway *fakeWeixinGateway) StartLogin(context.Context, []string) (weixin.QRStart, error) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	gateway.count++
	code := strconv.Itoa(gateway.count)
	return weixin.QRStart{Code: code, Content: "https://example.test/qr/" + code}, nil
}

func (gateway *fakeWeixinGateway) PollLogin(_ context.Context, _ string, code string, _ string) (weixin.QRStatus, error) {
	return weixin.QRStatus{Status: "confirmed", BotToken: "token-" + code, AccountID: "bot-" + code, UserID: "weixin-user-00000" + code, BaseURL: "ilinkai.weixin.qq.com"}, nil
}

func (gateway *fakeWeixinGateway) GetUpdates(ctx context.Context, _ weixin.Account, _ string) (weixin.Updates, error) {
	<-ctx.Done()
	return weixin.Updates{}, ctx.Err()
}

func (gateway *fakeWeixinGateway) SendText(context.Context, weixin.Account, string, string, string) error {
	return nil
}

func (gateway *fakeWeixinGateway) DownloadMedia(context.Context, weixin.CDNMedia, string) ([]byte, error) {
	gateway.downloadCalls++
	if gateway.media == nil {
		return nil, errors.New("fake gateway has no media")
	}
	return append([]byte(nil), gateway.media...), nil
}

func TestWeixinFinalTextNeverIncludesTrace(t *testing.T) {
	session := store.Session{Title: "检查服务", Status: "idle", Events: []store.Event{{Kind: "tool_end", Detail: "private trace"}}, Messages: []store.Message{{Role: "user", Content: "任务"}, {Role: "assistant", Content: "最终结果"}}}
	value := finalSessionText(session)
	if !strings.Contains(value, "任务完成 · 检查服务") || !strings.Contains(value, "最终结果") || strings.Contains(value, "private trace") {
		t.Fatalf("微信只应收到最终 assistant 文本: %q", value)
	}
}

func TestWeixinMessageFilteringAndUnicodeChunks(t *testing.T) {
	message := weixin.Message{Items: []weixin.MessageItem{{Type: 2}, {Type: 1, TextItem: &weixin.TextItem{Text: "  执行任务  "}}, {Type: 3, VoiceItem: &weixin.VoiceItem{Text: "检查测试"}}}}
	if value := messageText(message); value != "执行任务\n检查测试" {
		t.Fatalf("文字提取错误: %q", value)
	}
	chunks := splitText("一二三四五", 2)
	if len(chunks) != 3 || chunks[0] != "一二" || chunks[2] != "五" {
		t.Fatalf("Unicode 分片错误: %#v", chunks)
	}
}

func TestDecodeWeixinImageCreatesSharedAttachment(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0, 'I', 'H', 'D', 'R'}
	gateway := &fakeWeixinGateway{media: png}
	manager := &weixinManager{gateway: gateway}
	text, attachments, err := manager.decodeWeixinMessage(context.Background(), store.WeixinAccount{}, weixin.Message{MessageID: 42, Items: []weixin.MessageItem{{Type: 2, ImageItem: &weixin.ImageItem{Media: &weixin.CDNMedia{FullURL: "https://novac2c.cdn.weixin.qq.com/image"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if text != "" || len(attachments) != 1 || attachments[0].Kind != "image" || attachments[0].Name != "weixin-image-42.png" || gateway.downloadCalls != 1 {
		t.Fatalf("unexpected image decode: text=%q attachments=%+v calls=%d", text, attachments, gateway.downloadCalls)
	}
}

func TestNativeWeixinVoiceTextKeepsPlayableAttachment(t *testing.T) {
	gateway := &fakeWeixinGateway{media: append([]byte("RIFF"), make([]byte, 48)...)}
	manager := &weixinManager{gateway: gateway}
	text, attachments, err := manager.decodeWeixinMessage(context.Background(), store.WeixinAccount{}, weixin.Message{Items: []weixin.MessageItem{{Type: 3, VoiceItem: &weixin.VoiceItem{Text: "运行全部测试", Media: &weixin.CDNMedia{FullURL: "https://novac2c.cdn.weixin.qq.com/voice"}}}}})
	if err != nil || text != "运行全部测试" || len(attachments) != 1 || attachments[0].Kind != "audio" || gateway.downloadCalls != 1 {
		t.Fatalf("unexpected native voice decode: text=%q attachments=%+v calls=%d err=%v", text, attachments, gateway.downloadCalls, err)
	}
}

func TestUntranscribedWeixinVoiceIsDecodedButNotGivenText(t *testing.T) {
	gateway := &fakeWeixinGateway{media: append([]byte("RIFF"), make([]byte, 48)...)}
	manager := &weixinManager{gateway: gateway}
	text, attachments, err := manager.decodeWeixinMessage(context.Background(), store.WeixinAccount{}, weixin.Message{Items: []weixin.MessageItem{{Type: 3, VoiceItem: &weixin.VoiceItem{Media: &weixin.CDNMedia{FullURL: "https://novac2c.cdn.weixin.qq.com/voice"}}}}})
	if err != nil || text != "" || !onlyAudioAttachments(attachments) || gateway.downloadCalls != 1 {
		t.Fatalf("unexpected untranscribed voice decode: text=%q attachments=%+v calls=%d err=%v", text, attachments, gateway.downloadCalls, err)
	}
}

func TestEasyAgentUsesNativeVoiceTextWithoutBinaryAudio(t *testing.T) {
	message := toCoreMessage(store.Message{Role: "user", Content: "运行全部测试", Attachments: []store.Attachment{
		{Name: "voice.wav", MIMEType: "audio/wav", Kind: "audio", Data: []byte("RIFF")},
		{Name: "notes.txt", MIMEType: "text/plain", Kind: "text", Data: []byte("notes")},
	}})
	if message.Content != "运行全部测试" || len(message.Attachments) != 1 || message.Attachments[0].Kind != "text" {
		t.Fatalf("EasyAgent voice mapping leaked binary audio: %+v", message)
	}
}

func TestWeixinFailedAndCanceledFinalText(t *testing.T) {
	if value := finalSessionText(store.Session{Title: "失败任务", Status: "failed", Error: "模型错误"}); !strings.Contains(value, "任务失败 · 失败任务") || !strings.Contains(value, "模型错误") {
		t.Fatalf("失败结果错误: %q", value)
	}
	if value := finalSessionText(store.Session{Title: "停止任务", Status: "canceled", UpdatedAt: time.Now()}); !strings.Contains(value, "任务已停止 · 停止任务") {
		t.Fatalf("停止结果错误: %q", value)
	}
}

func TestWeixinNaturalCommands(t *testing.T) {
	tests := map[string]string{
		"创建新会话":                   "new",
		"创建新对话":                   "new",
		"我要创建一个新的对话":              "new",
		"麻烦帮我开个新聊天吧":              "new",
		"新会话。":                    "new",
		"查看状态":                    "status",
		"麻烦看一下现在任务跑到哪了":           "status",
		"当前任务完成了吗":                "status",
		"把当前任务停一下！":               "stop",
		"请帮我终止这个任务":               "stop",
		"有哪些命令":                   "help",
		"你支持哪些操作":                 "help",
		"项目列表":                    "projects",
		"当前项目":                    "current_project",
		"切换项目 EasyAgent":          "project",
		"帮我实现新会话按钮":               "",
		"创建新会话是什么意思":              "",
		"不要创建新会话":                 "",
		"创建新会话，然后检查一下当前项目":        "",
		"在新会话中继续上一个任务":            "",
		"停止任务这个功能是怎么实现的":          "",
		"不要停止当前任务":                "",
		"查看状态后帮我修复失败的测试":          "",
		"写一份关于如何使用新会话的文档":         "",
		"please stop the service": "",
	}
	for input, expected := range tests {
		if actual := weixinCommand(input); actual != expected {
			t.Fatalf("命令 %q 解析为 %q，期望 %q", input, actual, expected)
		}
	}
}

func TestWeixinIntentParserReportsExplicitAndGrammarConfidence(t *testing.T) {
	parser := newWeixinIntentParser()
	explicit := parser.Parse("/new")
	if explicit.Command != "new" || !explicit.Explicit || explicit.Confidence != 100 {
		t.Fatalf("明确命令解析错误: %+v", explicit)
	}
	grammar := parser.Parse("请创建一个新对话")
	if grammar.Command != "new" || grammar.Explicit || grammar.Confidence < 90 {
		t.Fatalf("自然语言意图解析错误: %+v", grammar)
	}
	unknown := parser.Parse("请解释创建新对话的实现方式")
	if unknown.Command != "" || unknown.Confidence != 0 {
		t.Fatalf("普通对话不应被控制层拦截: %+v", unknown)
	}
	project := parser.Parse("切换项目 EasyAgent Web")
	if project.Command != "project" || project.Argument != "easyagentweb" || project.Confidence < 90 {
		t.Fatalf("项目切换意图解析错误: %+v", project)
	}
	explicitProject := parser.Parse("/project EasyAgent Web")
	if explicitProject.Command != "project" || explicitProject.Argument != "EasyAgent Web" || !explicitProject.Explicit {
		t.Fatalf("明确项目命令解析错误: %+v", explicitProject)
	}
}

func TestWeixinDeliveryStatus(t *testing.T) {
	account := store.WeixinAccount{PendingMessageID: 8}
	if actual := weixinDeliveryStatus(account, "running", false); actual != "processing" {
		t.Fatalf("运行中的任务状态错误: %q", actual)
	}
	if actual := weixinDeliveryStatus(account, "idle", true); actual != "sending" {
		t.Fatalf("回传中的任务状态错误: %q", actual)
	}
	if actual := weixinDeliveryStatus(account, "idle", false); actual != "pending" {
		t.Fatalf("待重试状态错误: %q", actual)
	}
	account.DeliveredMessageID = 8
	if actual := weixinDeliveryStatus(account, "idle", false); actual != "delivered" {
		t.Fatalf("已送达状态错误: %q", actual)
	}
}

func TestWeixinAPIBindsMultipleAccountsWithoutExposingSecrets(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	environment, err := appenv.Open(appenv.Config{Home: filepath.Join(t.TempDir(), "home")})
	if err != nil {
		t.Fatal(err)
	}
	application := NewForTests(database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}, environment)
	application.weixin.gateway = &fakeWeixinGateway{}
	defer application.Shutdown(context.Background())
	httpServer := httptest.NewServer(application.Handler())
	defer httpServer.Close()

	for _, label := range []string{"小王", "值班同学"} {
		response, err := http.Post(httpServer.URL+"/api/v1/channels/weixin/login", "application/json", bytes.NewBufferString(`{"label":"`+label+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("开始扫码失败: %d", response.StatusCode)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		accounts, loadErr := database.ListWeixinAccounts()
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if len(accounts) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("多人绑定没有完成: %+v", accounts)
		}
		time.Sleep(10 * time.Millisecond)
	}
	response, err := http.Get(httpServer.URL + "/api/v1/channels/weixin")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var state weixinStateView
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(state)
	if len(state.Accounts) != 2 || bytes.Contains(encoded, []byte("token-")) || bytes.Contains(encoded, []byte("weixin-user-000001")) || bytes.Contains(encoded, []byte("lastMessageAt")) {
		t.Fatalf("绑定列表数量或脱敏错误: %s", encoded)
	}
}

func TestUntranscribedVoiceIsSavedWithoutQueueingAgent(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	environment, err := appenv.Open(appenv.Config{Home: filepath.Join(t.TempDir(), "home")})
	if err != nil {
		t.Fatal(err)
	}
	application := NewForTests(database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}, environment)
	defer application.Shutdown(context.Background())
	now := time.Now().UTC()
	account := store.WeixinAccount{ID: "bot-voice", Label: "小王", UserID: "user-voice", Token: "token-voice", BaseURL: weixin.DefaultBaseURL, Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := database.SaveWeixinAccount(account); err != nil {
		t.Fatal(err)
	}
	attachments := []store.Attachment{{ID: "voice-1", Name: "weixin-voice.wav", MIMEType: "audio/wav", Kind: "audio", Size: 8, Data: []byte("RIFFtest")}}
	sessionID, err := application.weixin.saveUntranscribedVoice(account, attachments, now)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := database.LoadSessionWindow(sessionID, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	savedAccount, err := database.GetWeixinAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != "idle" || len(loaded.Messages) != 1 || !onlyAudioAttachments(loaded.Messages[0].Attachments) || application.tasks.has(sessionID) {
		t.Fatalf("untranscribed voice should be stored without a task: session=%+v", loaded)
	}
	if savedAccount.CurrentSessionID != sessionID || savedAccount.PendingMessageID != 0 || savedAccount.DeliveredMessageID != 0 {
		t.Fatalf("untranscribed voice corrupted delivery state: %+v", savedAccount)
	}
}

func TestSavingEnabledWeixinChannelDoesNotSuppressPendingResult(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	environment, err := appenv.Open(appenv.Config{Home: filepath.Join(t.TempDir(), "home")})
	if err != nil {
		t.Fatal(err)
	}
	application := NewForTests(database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}, environment)
	application.weixin.gateway = &fakeWeixinGateway{}
	defer application.Shutdown(context.Background())
	httpServer := httptest.NewServer(application.Handler())
	defer httpServer.Close()

	now := time.Now().UTC()
	if _, err := database.SaveWeixinSettings(store.WeixinSettings{Enabled: true, IgnoreBefore: now}); err != nil {
		t.Fatal(err)
	}
	if err := database.SaveWeixinAccount(store.WeixinAccount{ID: "bot-pending", Label: "小王", UserID: "user-pending", Token: "token-pending", BaseURL: weixin.DefaultBaseURL, Enabled: true, IgnoreBefore: now, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := database.SetWeixinTask("bot-pending", "session-pending", 88, "context-pending", now, now); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPut, httpServer.URL+"/api/v1/channels/weixin", bytes.NewBufferString(`{"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("重复保存启用状态失败: %d", response.StatusCode)
	}
	account, err := database.GetWeixinAccount("bot-pending")
	if err != nil {
		t.Fatal(err)
	}
	if account.DeliveredMessageID != 0 || account.PendingContextToken != "context-pending" {
		t.Fatalf("重复保存启用状态不应吞掉待回传结果: %+v", account)
	}
}

func TestWeixinAPIShowsCurrentTaskAndRetriesDelivery(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	environment, err := appenv.Open(appenv.Config{Home: filepath.Join(t.TempDir(), "home")})
	if err != nil {
		t.Fatal(err)
	}
	application := NewForTests(database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}, environment)
	application.weixin.gateway = &fakeWeixinGateway{}
	defer application.Shutdown(context.Background())
	httpServer := httptest.NewServer(application.Handler())
	defer httpServer.Close()

	now := time.Now().UTC()
	if _, err := database.SaveWeixinSettings(store.WeixinSettings{Enabled: true, IgnoreBefore: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateSessionWithRuntime("session-current", "检查发布状态", store.RuntimeCodex, "gpt-5", t.TempDir(), now); err != nil {
		t.Fatal(err)
	}
	if err := database.SaveWeixinAccount(store.WeixinAccount{ID: "bot-current", Label: "值班同学", UserID: "user-current", Token: "token-current", BaseURL: weixin.DefaultBaseURL, Enabled: true, IgnoreBefore: now, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := database.SetWeixinTask("bot-current", "session-current", 99, "context-current", now, now); err != nil {
		t.Fatal(err)
	}

	response, err := http.Get(httpServer.URL + "/api/v1/channels/weixin")
	if err != nil {
		t.Fatal(err)
	}
	var before weixinStateView
	if err := json.NewDecoder(response.Body).Decode(&before); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if len(before.Accounts) != 1 || before.Accounts[0].CurrentSession == nil || before.Accounts[0].CurrentSession.Title != "检查发布状态" || before.Accounts[0].CurrentSession.Runtime != store.RuntimeCodex || before.Accounts[0].DeliveryStatus != "pending" {
		t.Fatalf("当前任务视图错误: %+v", before.Accounts)
	}

	retryResponse, err := http.Post(httpServer.URL+"/api/v1/channels/weixin/accounts/bot-current/retry", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	retryResponse.Body.Close()
	if retryResponse.StatusCode != http.StatusOK {
		t.Fatalf("重试回传失败: %d", retryResponse.StatusCode)
	}
	deadline := time.Now().Add(time.Second)
	for {
		account, loadErr := database.GetWeixinAccount("bot-current")
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if account.DeliveredMessageID == 99 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("结果没有在重试后标记为送达: %+v", account)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
