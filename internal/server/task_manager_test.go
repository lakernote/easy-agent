package server

import (
	"testing"

	"github.com/lakernote/easy-agent/internal/store"
)

func TestTaskManagerTracksLiveState(t *testing.T) {
	manager := newTaskManager()
	cancelled := false
	manager.set("session-1", "token-1", func() { cancelled = true })

	if !manager.has("session-1") {
		t.Fatal("新任务没有被记录")
	}
	manager.appendPartial("session-1", "hello")
	manager.appendPartial("session-1", " world")
	manager.setProgress("session-1", "正在运行")
	manager.setUsage("session-1", store.Usage{ModelCalls: 1, TotalTokens: 12})

	if got := manager.partial("session-1"); got != "hello world" {
		t.Fatalf("partial = %q, want %q", got, "hello world")
	}
	if got := manager.progress("session-1"); got != "正在运行" {
		t.Fatalf("progress = %q, want %q", got, "正在运行")
	}
	if got := manager.usage("session-1"); got.ModelCalls != 1 || got.TotalTokens != 12 {
		t.Fatalf("usage = %+v", got)
	}

	manager.cancel("session-1")
	if !cancelled {
		t.Fatal("cancel 没有调用任务取消函数")
	}
	manager.clear("session-1", "wrong-token")
	if !manager.has("session-1") {
		t.Fatal("错误 token 不应清理任务")
	}
	manager.clear("session-1", "token-1")
	if manager.has("session-1") {
		t.Fatal("正确 token 没有清理任务")
	}
}

func TestTaskManagerCancelMissingTaskIsSafe(t *testing.T) {
	manager := newTaskManager()
	manager.cancel("missing")
	manager.appendPartial("missing", "ignored")
	manager.setProgress("missing", "ignored")
	manager.setUsage("missing", store.Usage{})
}
