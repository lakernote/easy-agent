package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadCodexSessionLogPaginatesAndRedacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	content := strings.Join([]string{
		`{"timestamp":"2026-09-11T01:02:03Z","type":"session_meta","payload":{"id":"thread-test","source":"appServer"}}`,
		`{"timestamp":"2026-09-11T01:02:04Z","type":"response_item","payload":{"type":"message","role":"user","content":"hello","api_key":"private"}}`,
		`{"timestamp":"2026-09-11T01:02:05Z","type":"event_msg","payload":{"type":"task_complete"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	page, err := readCodexSessionLog(path, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalLines != 3 || len(page.Lines) != 2 || page.NextCursor != 2 || page.TotalBytes != int64(len(content)) {
		t.Fatalf("Session JSONL 分页错误: %+v", page)
	}
	if page.Lines[1].Line != 2 || page.Lines[1].Type != "response_item" || page.Lines[1].Subtype != "message" || strings.Contains(page.Lines[1].Payload, "private") || (!strings.Contains(page.Lines[1].Payload, "<redacted>") && !strings.Contains(page.Lines[1].Payload, `\u003credacted\u003e`)) {
		t.Fatalf("Session JSONL 解析或脱敏错误: %+v", page.Lines[1])
	}

	next, err := readCodexSessionLog(path, page.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Lines) != 1 || next.Lines[0].Line != 3 || next.NextCursor != 0 {
		t.Fatalf("Session JSONL 下一页错误: %+v", next)
	}
}

func TestValidateCodexSessionLogPathRequiresMatchingThread(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	directory := filepath.Join(root, "sessions", "2026", "09", "11")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "rollout-thread-test.jsonl")
	data, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"id": "thread-test"}})
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	expected, _ := filepath.EvalSymlinks(path)
	resolved, err := validateCodexSessionLogPath(path, "thread-test")
	if err != nil || resolved != expected {
		t.Fatalf("合法 Session JSONL 没有通过校验: path=%q err=%v", resolved, err)
	}
	if _, err := validateCodexSessionLogPath(path, "another-thread"); err == nil || !strings.Contains(err.Error(), "不匹配") {
		t.Fatalf("错误 Thread ID 应被拒绝: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "rollout-thread-test.jsonl")
	if err := os.WriteFile(outside, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateCodexSessionLogPath(outside, "thread-test"); err == nil || !strings.Contains(err.Error(), "不在 CODEX_HOME") {
		t.Fatalf("CODEX_HOME 外的路径应被拒绝: %v", err)
	}
}

func TestFindCodexSessionLogFindsArchivedLog(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	directory := filepath.Join(root, "archived_sessions")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "rollout-2026-09-11-thread-archived.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"thread-archived"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	expected, _ := filepath.EvalSymlinks(path)
	found, err := findCodexSessionLog("thread-archived")
	if err != nil || found != expected {
		t.Fatalf("归档 Session JSONL 未找到: path=%q err=%v", found, err)
	}
	if _, err := findCodexSessionLog("../escape"); err == nil {
		t.Fatal("非法 Thread ID 应被拒绝")
	}
}
