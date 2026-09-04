package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lakernote/easy-agent/internal/appenv"
	"github.com/lakernote/easy-agent/internal/store"
	"github.com/lakernote/easy-agent/internal/weixin"
)

type fakeWeixinGateway struct {
	mu    sync.Mutex
	count int
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

func TestWeixinFinalTextNeverIncludesTrace(t *testing.T) {
	session := store.Session{Status: "idle", Events: []store.Event{{Kind: "tool_end", Detail: "private trace"}}, Messages: []store.Message{{Role: "user", Content: "任务"}, {Role: "assistant", Content: "最终结果"}}}
	if value := finalSessionText(session); value != "最终结果" {
		t.Fatalf("微信只应收到最终 assistant 文本: %q", value)
	}
}

func TestWeixinMessageFilteringAndUnicodeChunks(t *testing.T) {
	message := weixin.Message{Items: []weixin.MessageItem{{Type: 2}, {Type: 1, TextItem: &weixin.TextItem{Text: "  执行任务  "}}}}
	if value := messageText(message); value != "执行任务" {
		t.Fatalf("文字提取错误: %q", value)
	}
	chunks := splitText("一二三四五", 2)
	if len(chunks) != 3 || chunks[0] != "一二" || chunks[2] != "五" {
		t.Fatalf("Unicode 分片错误: %#v", chunks)
	}
}

func TestWeixinFailedAndCanceledFinalText(t *testing.T) {
	if value := finalSessionText(store.Session{Status: "failed", Error: "模型错误"}); value != "任务失败：模型错误" {
		t.Fatalf("失败结果错误: %q", value)
	}
	if value := finalSessionText(store.Session{Status: "canceled", UpdatedAt: time.Now()}); value != "任务已停止。" {
		t.Fatalf("停止结果错误: %q", value)
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
