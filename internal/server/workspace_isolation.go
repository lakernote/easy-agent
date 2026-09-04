package server

import (
	"context"
	"errors"
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
	Notice    string
}

// prepareSessionWorkspace gives each new conversation a stable Git worktree.
// A session, rather than an individual turn, is the isolation boundary so
// thread/resume and both runtimes keep seeing previous file changes.
func (server *Server) prepareSessionWorkspace(ctx context.Context, sessionID, source string, settings store.RuntimeSettings) sessionWorkspace {
	source = resolvedPath(source)
	plan := sessionWorkspace{Execution: source, Source: source}
	if !settings.GitWorktrees {
		plan.Notice = "自动 Git worktree 已关闭；同一工作区的任务将串行执行"
		return plan
	}
	if strings.TrimSpace(source) == "" {
		return plan
	}
	if pathWithin(source, filepath.Join(server.env.Runtime(), "worktrees")) {
		return plan
	}
	worktree, err := server.createGitWorktree(ctx, sessionID, source, source)
	if err != nil {
		plan.Notice = err.Error() + "；本会话使用原目录并与同项目任务互斥"
		return plan
	}
	return worktree
}

// createForkWorkspace starts an independent fork from the exact committed HEAD
// visible to the source conversation. Dirty state is rejected so a fork never
// silently loses edits that only exist in the original checkout.
func (server *Server) createForkWorkspace(ctx context.Context, sessionID string, source store.Session) (sessionWorkspace, error) {
	original := strings.TrimSpace(source.SourceWorkspace)
	if original == "" {
		original = source.Workspace
	}
	return server.createGitWorktree(ctx, sessionID, source.Workspace, original)
}

func (server *Server) createGitWorktree(ctx context.Context, sessionID, base, original string) (sessionWorkspace, error) {
	base = resolvedPath(base)
	original = resolvedPath(original)
	git, err := server.env.ResolveCommand("git")
	if err != nil {
		return sessionWorkspace{}, errors.New("未找到 Git")
	}
	probeContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rootOutput, err := server.runGit(probeContext, git, base, "rev-parse", "--show-toplevel")
	if err != nil {
		return sessionWorkspace{}, errors.New("当前目录不是 Git 项目")
	}
	root := resolvedPath(strings.TrimSpace(rootOutput))
	relative, err := filepath.Rel(root, base)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return sessionWorkspace{}, errors.New("无法确定项目在 Git 仓库中的位置")
	}
	status, err := server.runGit(probeContext, git, base, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return sessionWorkspace{}, errors.New("无法检查 Git 工作区状态")
	}
	if strings.TrimSpace(status) != "" {
		return sessionWorkspace{}, errors.New("项目存在未提交修改，为避免遗漏改动未创建 worktree")
	}
	parent := filepath.Join(server.env.Runtime(), "worktrees")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return sessionWorkspace{}, fmt.Errorf("无法创建 worktree 目录：%v", err)
	}
	shortID := shortSessionID(sessionID)
	branch := "easyagent/" + shortID
	target := filepath.Join(parent, shortID)
	worktreeContext, worktreeCancel := context.WithTimeout(ctx, 30*time.Second)
	defer worktreeCancel()
	output, err := server.runGit(worktreeContext, git, root, "worktree", "add", "-b", branch, target, "HEAD")
	if err != nil {
		return sessionWorkspace{}, fmt.Errorf("Git worktree 创建失败：%s", conciseCommandOutput(output, err))
	}
	execution := target
	if relative != "." {
		execution = filepath.Join(target, relative)
	}
	if info, err := os.Stat(execution); err != nil || !info.IsDir() {
		server.discardFreshWorktree(root, target, branch)
		return sessionWorkspace{}, errors.New("Git worktree 创建后目录不可用")
	}
	return sessionWorkspace{Execution: execution, Source: original, Branch: branch, Notice: "已为本会话创建独立 Git worktree"}, nil
}

// cleanupSessionWorktree only removes a worktree when it has no file changes
// and its HEAD is already reachable from the source checkout. This prevents a
// cleanup button from discarding either uncommitted edits or unmerged commits.
func (server *Server) cleanupSessionWorktree(ctx context.Context, session store.Session) (string, error) {
	if strings.TrimSpace(session.WorktreeBranch) == "" {
		return "", errors.New("当前会话没有 EasyAgent 管理的 worktree")
	}
	git, err := server.env.ResolveCommand("git")
	if err != nil {
		return "", errors.New("未找到 Git，无法清理 worktree")
	}
	checkContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	status, err := server.runGit(checkContext, git, session.Workspace, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", fmt.Errorf("无法检查 worktree 状态：%s", conciseCommandOutput(status, err))
	}
	if strings.TrimSpace(status) != "" {
		return "", errors.New("worktree 仍有未提交修改；请先提交或处理这些文件，再执行清理")
	}
	worktreeRoot, err := server.runGit(checkContext, git, session.Workspace, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", errors.New("无法定位 worktree 根目录")
	}
	worktreeRoot = resolvedPath(strings.TrimSpace(worktreeRoot))
	source := resolvedPath(session.SourceWorkspace)
	sourceRoot, err := server.runGit(checkContext, git, source, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", errors.New("源项目已经不可用，暂不清理 worktree")
	}
	sourceRoot = resolvedPath(strings.TrimSpace(sourceRoot))
	head, err := server.runGit(checkContext, git, worktreeRoot, "rev-parse", "HEAD")
	if err != nil {
		return "", errors.New("无法读取 worktree HEAD")
	}
	if _, err := server.runGit(checkContext, git, sourceRoot, "merge-base", "--is-ancestor", strings.TrimSpace(head), "HEAD"); err != nil {
		return "", errors.New("worktree 包含尚未进入源项目当前分支的提交；合并或应用这些提交后再清理")
	}
	removeContext, removeCancel := context.WithTimeout(ctx, 30*time.Second)
	defer removeCancel()
	output, err := server.runGit(removeContext, git, sourceRoot, "worktree", "remove", worktreeRoot)
	if err != nil {
		return "", fmt.Errorf("清理 Git worktree 失败：%s", conciseCommandOutput(output, err))
	}
	notice := "worktree 已安全清理；该会话已切回源项目目录"
	if _, err := server.runGit(removeContext, git, sourceRoot, "branch", "-d", session.WorktreeBranch); err != nil {
		notice += "，原分支因 Git 安全检查未删除"
	}
	return notice, nil
}

func (server *Server) discardFreshWorktree(root, target, branch string) {
	git, err := server.env.ResolveCommand("git")
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = server.runGit(ctx, git, root, "worktree", "remove", "--force", target)
	_, _ = server.runGit(ctx, git, root, "branch", "-D", branch)
}

func (server *Server) runGit(ctx context.Context, git, directory string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, git, append([]string{"-C", directory}, arguments...)...)
	command.Env = server.env.Environ(nil)
	output, err := command.CombinedOutput()
	return string(output), err
}

func resolvedPath(value string) string {
	value = strings.TrimSpace(value)
	if resolved, err := filepath.EvalSymlinks(value); err == nil {
		return resolved
	}
	return value
}

func pathWithin(value, parent string) bool {
	relative, err := filepath.Rel(resolvedPath(parent), resolvedPath(value))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func shortSessionID(value string) string {
	value = strings.ReplaceAll(value, "-", "")
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func conciseCommandOutput(output string, err error) string {
	value := strings.Join(strings.Fields(strings.TrimSpace(output)), " ")
	if value == "" && err != nil {
		value = err.Error()
	}
	if len(value) > 240 {
		value = value[:240] + "…"
	}
	return value
}

func workspaceIsolationLabel(session store.Session) string {
	if session.WorktreeBranch != "" {
		return fmt.Sprintf("Git worktree · %s", session.WorktreeBranch)
	}
	return "工作区互斥"
}
