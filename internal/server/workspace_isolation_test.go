package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lakernote/easy-agent/internal/appenv"
	"github.com/lakernote/easy-agent/internal/store"
)

func TestPrepareSessionWorkspaceCreatesGitWorktree(t *testing.T) {
	repository, git := newTestGitRepository(t)
	environment, err := appenv.Open(appenv.Config{Home: filepath.Join(t.TempDir(), "home"), ExtraPaths: []string{filepath.Dir(git)}})
	if err != nil {
		t.Fatal(err)
	}
	application := &Server{env: environment}
	plan := application.prepareSessionWorkspace(context.Background(), "12345678-1234-1234-1234-123456789abc", repository, store.RuntimeSettings{MaxConcurrentTasks: 2, GitWorktrees: true})
	resolvedRepository, _ := filepath.EvalSymlinks(repository)
	if plan.Execution == resolvedRepository || plan.Source != resolvedRepository || plan.Branch != "easyagent/123456781234" {
		t.Fatalf("Git 项目应创建独立 worktree: %+v", plan)
	}
	if data, err := os.ReadFile(filepath.Join(plan.Execution, "README.md")); err != nil || string(data) != "test\n" {
		t.Fatalf("worktree 内容异常: data=%q err=%v", data, err)
	}
	if !strings.Contains(plan.Notice, "独立 Git worktree") {
		t.Fatalf("应向用户说明隔离结果: %q", plan.Notice)
	}
}

func TestPrepareSessionWorkspaceFallsBackWhenRepositoryIsDirty(t *testing.T) {
	repository, git := newTestGitRepository(t)
	if err := os.WriteFile(filepath.Join(repository, "dirty.txt"), []byte("not committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment, err := appenv.Open(appenv.Config{Home: filepath.Join(t.TempDir(), "home"), ExtraPaths: []string{filepath.Dir(git)}})
	if err != nil {
		t.Fatal(err)
	}
	plan := (&Server{env: environment}).prepareSessionWorkspace(context.Background(), "dirty-session", repository, store.RuntimeSettings{GitWorktrees: true})
	resolvedRepository, _ := filepath.EvalSymlinks(repository)
	if plan.Execution != resolvedRepository || plan.Branch != "" || !strings.Contains(plan.Notice, "未提交修改") {
		t.Fatalf("脏工作区应明确降级为互斥: %+v", plan)
	}
}

func TestCleanupSessionWorktreeProtectsChangesAndRemovesSafeWorktree(t *testing.T) {
	repository, git := newTestGitRepository(t)
	environment, err := appenv.Open(appenv.Config{Home: filepath.Join(t.TempDir(), "home"), ExtraPaths: []string{filepath.Dir(git)}})
	if err != nil {
		t.Fatal(err)
	}
	application := &Server{env: environment}
	dirty := application.prepareSessionWorkspace(context.Background(), "dirty-cleanup", repository, store.RuntimeSettings{GitWorktrees: true})
	if err := os.WriteFile(filepath.Join(dirty.Execution, "agent-change.txt"), []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := application.cleanupSessionWorktree(context.Background(), store.Session{Workspace: dirty.Execution, SourceWorkspace: dirty.Source, WorktreeBranch: dirty.Branch}); err == nil || !strings.Contains(err.Error(), "未提交修改") {
		t.Fatalf("清理不能丢弃未提交修改: %v", err)
	}

	safe := application.prepareSessionWorkspace(context.Background(), "safe-cleanup", repository, store.RuntimeSettings{GitWorktrees: true})
	notice, err := application.cleanupSessionWorktree(context.Background(), store.Session{Workspace: safe.Execution, SourceWorkspace: safe.Source, WorktreeBranch: safe.Branch})
	if err != nil {
		t.Fatalf("干净且无独立提交的 worktree 应可清理: %v", err)
	}
	if _, err := os.Stat(safe.Execution); !os.IsNotExist(err) {
		t.Fatalf("worktree 目录仍存在: %v", err)
	}
	if !strings.Contains(notice, "安全清理") {
		t.Fatalf("清理结果提示异常: %q", notice)
	}
}

func TestCreateForkWorkspaceUsesIndependentDirectory(t *testing.T) {
	repository, git := newTestGitRepository(t)
	environment, err := appenv.Open(appenv.Config{Home: filepath.Join(t.TempDir(), "home"), ExtraPaths: []string{filepath.Dir(git)}})
	if err != nil {
		t.Fatal(err)
	}
	application := &Server{env: environment}
	source := application.prepareSessionWorkspace(context.Background(), "source-session", repository, store.RuntimeSettings{GitWorktrees: true})
	forked, err := application.createForkWorkspace(context.Background(), "fork-session", store.Session{Workspace: source.Execution, SourceWorkspace: source.Source, WorktreeBranch: source.Branch})
	if err != nil {
		t.Fatal(err)
	}
	if forked.Execution == source.Execution || forked.Source != source.Source || forked.Branch == source.Branch {
		t.Fatalf("对话分支应有独立工作树: source=%+v fork=%+v", source, forked)
	}
}

func newTestGitRepository(t *testing.T) (string, string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git 不可用")
	}
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit := func(arguments ...string) {
		t.Helper()
		command := exec.Command(git, arguments...)
		command.Dir = repository
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=EasyAgent Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=EasyAgent Test", "GIT_COMMITTER_EMAIL=test@example.com")
		if output, runErr := command.CombinedOutput(); runErr != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), runErr, output)
		}
	}
	runGit("init")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "initial")
	return repository, git
}
