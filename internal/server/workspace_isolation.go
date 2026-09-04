package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/store"
)

type sessionWorkspace struct {
	Execution string
	Source    string
	Branch    string
}

// prepareSessionWorkspace gives each new conversation a stable Git worktree.
// A session, rather than an individual turn, is the isolation boundary so
// thread/resume and both runtimes keep seeing previous file changes.
func (server *Server) prepareSessionWorkspace(ctx context.Context, sessionID, source string, settings store.RuntimeSettings) sessionWorkspace {
	if resolved, err := filepath.EvalSymlinks(source); err == nil {
		source = resolved
	}
	plan := sessionWorkspace{Execution: source, Source: source}
	if !settings.GitWorktrees || strings.TrimSpace(source) == "" || strings.Contains(source, filepath.Join(server.env.Runtime(), "worktrees")) {
		return plan
	}
	git, err := server.env.ResolveCommand("git")
	if err != nil {
		return plan
	}
	probeContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rootOutput, err := exec.CommandContext(probeContext, git, "-C", source, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return plan
	}
	root := strings.TrimSpace(string(rootOutput))
	if root == "" {
		return plan
	}
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	}
	relative, err := filepath.Rel(root, source)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return plan
	}
	parent := filepath.Join(server.env.Runtime(), "worktrees")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return plan
	}
	shortID := strings.ReplaceAll(sessionID, "-", "")
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	branch := "easyagent/" + shortID
	target := filepath.Join(parent, shortID)
	worktreeContext, worktreeCancel := context.WithTimeout(ctx, 30*time.Second)
	defer worktreeCancel()
	command := exec.CommandContext(worktreeContext, git, "-C", root, "worktree", "add", "-b", branch, target, "HEAD")
	command.Env = server.env.Environ(nil)
	if output, err := command.CombinedOutput(); err != nil {
		_ = output // Auto mode deliberately falls back to the project lock.
		return plan
	}
	execution := target
	if relative != "." {
		execution = filepath.Join(target, relative)
	}
	if info, err := os.Stat(execution); err != nil || !info.IsDir() {
		return plan
	}
	return sessionWorkspace{Execution: execution, Source: source, Branch: branch}
}

func workspaceIsolationLabel(session store.Session) string {
	if session.WorktreeBranch != "" {
		return fmt.Sprintf("Git worktree · %s", session.WorktreeBranch)
	}
	return "工作区互斥"
}
