package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenProtectsDatabaseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "easyagent.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("SQLite 文件权限过宽: %o", got)
	}
}

func TestOpenMigratesLegacyCompactionSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`CREATE TABLE ea_compactions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		seq INTEGER NOT NULL,
		summary TEXT NOT NULL,
		through_message_id INTEGER NOT NULL,
		source_messages INTEGER NOT NULL,
		compacted_messages INTEGER NOT NULL,
		usage_json BLOB NOT NULL,
		created_at TEXT NOT NULL
	)`)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var columns int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('ea_compactions') WHERE name='split_turn'`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 1 {
		t.Fatalf("旧数据库没有迁移 split_turn 列: %d", columns)
	}
}

func TestSessionMessagesAndTraceUseSeparateRows(t *testing.T) {
	value, err := Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer value.Close()
	now := time.Now()
	if _, err := value.CreateSession("s1", "第一轮", "fixture", "", now); err != nil {
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

func TestAppendMessagesCommitsToolStepTogether(t *testing.T) {
	value, err := Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer value.Close()
	if _, err := value.CreateSession("s1", "工具", "fixture", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := value.AppendMessages("s1", []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "lookup", Arguments: "{}"}}},
		{Role: "tool", ToolCallID: "call-1", Content: "结果"},
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := value.Session("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 2 || loaded.Messages[1].ToolCallID != loaded.Messages[0].ToolCalls[0].ID {
		t.Fatalf("工具 step 批量保存后消息链异常: %+v", loaded.Messages)
	}
}

func TestMessageAttachmentsStayInSQLite(t *testing.T) {
	value, err := Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer value.Close()
	now := time.Now()
	if _, err := value.CreateSession("s1", "附件", "fixture", "", now); err != nil {
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

func TestCompactionSplitTurnRoundTripsInSQLite(t *testing.T) {
	value, err := Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer value.Close()
	if _, err := value.CreateSession("s1", "压缩", "fixture", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := value.AppendCompaction("s1", Compaction{Summary: "checkpoint", ThroughMessageID: 7, SplitTurn: true, SourceMessages: 3, CompactedMessages: 7}); err != nil {
		t.Fatal(err)
	}
	loaded, err := value.Session("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Compactions) != 1 || !loaded.Compactions[0].SplitTurn || loaded.Compactions[0].ThroughMessageID != 7 {
		t.Fatalf("split-turn checkpoint 没有正确持久化: %+v", loaded.Compactions)
	}
}

func TestSessionQueueRunAndCancelStates(t *testing.T) {
	value, err := Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer value.Close()
	now := time.Now()
	if _, err := value.CreateSession("s1", "任务", "fixture", "", now); err != nil {
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
