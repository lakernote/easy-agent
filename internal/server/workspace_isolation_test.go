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
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git 不可用")
	}
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit := func(arguments ...string) string {
		t.Helper()
		command := exec.Command(git, arguments...)
		command.Dir = repository
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=EasyAgent Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=EasyAgent Test", "GIT_COMMITTER_EMAIL=test@example.com")
		output, runErr := command.CombinedOutput()
		if runErr != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), runErr, output)
		}
		return string(output)
	}
	runGit("init")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "initial")
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
}
