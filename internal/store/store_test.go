package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionMessagesAndTraceUseSeparateRows(t *testing.T) {
	value, err := Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer value.Close()
	now := time.Now()
	if _, err := value.CreateSession("s1", "第一轮", "fixture", now); err != nil {
		t.Fatal(err)
	}
	if err := value.AppendMessage("s1", Message{Role: "user", Content: "你好"}); err != nil {
		t.Fatal(err)
	}
	if err := value.AppendEvent("s1", Event{Kind: "llm", Status: "success", InputTokens: 10}); err != nil {
		t.Fatal(err)
	}
	loaded, err := value.Session("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 1 || len(loaded.Events) != 1 || loaded.Messages[0].Content != "你好" {
		t.Fatalf("SQLite 会话关系错误: %+v", loaded)
	}
	if loaded.Events[0].ID <= 0 {
		t.Fatalf("Trace 事件必须返回 SQLite 主键，实际为 %+v", loaded.Events[0])
	}
}

func TestMessageAttachmentsStayInSQLite(t *testing.T) {
	value, err := Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer value.Close()
	now := time.Now()
	if _, err := value.CreateSession("s1", "附件", "fixture", now); err != nil {
		t.Fatal(err)
	}
	attachment := Attachment{ID: "a1", Name: "error.log", MIMEType: "text/plain", Kind: "text", Size: 12, Data: []byte("stack trace")}
	if err := value.AppendMessage("s1", Message{Role: "user", Content: "分析", Attachments: []Attachment{attachment}}); err != nil {
		t.Fatal(err)
	}
	loaded, err := value.Session("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 1 || len(loaded.Messages[0].Attachments) != 1 || string(loaded.Messages[0].Attachments[0].Data) != "stack trace" {
		t.Fatalf("附件没有随消息持久化: %+v", loaded.Messages)
	}
	downloaded, err := value.Attachment("a1")
	if err != nil || downloaded.Name != "error.log" || string(downloaded.Data) != "stack trace" {
		t.Fatalf("按 ID 读取附件失败: value=%+v err=%v", downloaded, err)
	}
}

func TestSessionQueueRunAndCancelStates(t *testing.T) {
	value, err := Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer value.Close()
	now := time.Now()
	if _, err := value.CreateSession("s1", "任务", "fixture", now); err != nil {
		t.Fatal(err)
	}
	if err := value.QueueSession("s1", "fixture", now); err != nil {
		t.Fatal(err)
	}
	queued, _ := value.Session("s1")
	if queued.Status != "queued" {
		t.Fatalf("任务应先进入 queued，实际为 %s", queued.Status)
	}
	if err := value.MarkRunning("s1", now); err != nil {
		t.Fatal(err)
	}
	changed, err := value.CancelSession("s1", now)
	if err != nil || !changed {
		t.Fatalf("取消运行中任务失败: changed=%v err=%v", changed, err)
	}
	canceled, _ := value.Session("s1")
	if canceled.Status != "canceled" {
		t.Fatalf("任务应进入 canceled，实际为 %s", canceled.Status)
	}
	if err := value.FailSession("s1", context.Canceled, Usage{ModelCalls: 1, ModelDurationMS: 90000}, now); err != nil {
		t.Fatal(err)
	}
	stillCanceled, _ := value.Session("s1")
	if stillCanceled.Status != "canceled" {
		t.Fatalf("后台退出不应覆盖用户取消状态: %s", stillCanceled.Status)
	}
	if stillCanceled.Usage.ModelCalls != 1 || stillCanceled.Usage.ModelDurationMS != 90000 {
		t.Fatalf("取消后的调用统计未保存: %+v", stillCanceled.Usage)
	}
}
