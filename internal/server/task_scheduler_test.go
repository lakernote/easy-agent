package server

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/lakernote/easy-agent/internal/store"
)

func TestTaskSchedulerLimitsGlobalConcurrency(t *testing.T) {
	scheduler := newTaskScheduler(2)
	if err := scheduler.acquire(context.Background(), "project-a"); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.acquire(context.Background(), "project-b"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := scheduler.acquire(ctx, "project-c"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("第三个任务应等待总并发槽，实际错误: %v", err)
	}
	scheduler.release("project-a")
	scheduler.release("project-b")
	if active, limit := scheduler.snapshot(); active != 0 || limit != 2 {
		t.Fatalf("调度器计数异常: active=%d limit=%d", active, limit)
	}
}

func TestTaskConflictKeySerializesProjectsWithSharedSources(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	if _, err := database.CreateProject("multi", "多源项目", []string{"/srv/api", "/srv/web"}, false, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateProject("single", "单源项目", []string{"/srv/api"}, false, now); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: database}
	if key := server.taskConflictKey(store.Session{ProjectID: "multi", Workspace: "/tmp/worktree", SourceWorkspace: "/srv/api"}); key != "project:multi" {
		t.Fatalf("多源项目应按项目串行，实际 key=%q", key)
	}
	if key := server.taskConflictKey(store.Session{ProjectID: "single", Workspace: "/tmp/worktree", SourceWorkspace: "/srv/api"}); key != "/tmp/worktree" {
		t.Fatalf("单源 worktree 应保持并行，实际 key=%q", key)
	}
}

func TestTaskSchedulerSerializesSameWorkspace(t *testing.T) {
	scheduler := newTaskScheduler(2)
	if err := scheduler.acquire(context.Background(), "shared-project"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := scheduler.acquire(ctx, "shared-project"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("同一工作区应保持串行，实际错误: %v", err)
	}
	if err := scheduler.acquire(context.Background(), "other-project"); err != nil {
		t.Fatalf("不同工作区应可并行: %v", err)
	}
	scheduler.release("other-project")
	scheduler.release("shared-project")
}

func TestTaskSchedulerDoesNotAcquireCanceledContext(t *testing.T) {
	scheduler := newTaskScheduler(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := scheduler.acquire(ctx, "project"); !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消任务不应获取执行槽: %v", err)
	}
	if active, _ := scheduler.snapshot(); active != 0 {
		t.Fatalf("已取消任务泄漏执行槽: %d", active)
	}
}
