//go:build darwin || linux

package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lakernote/easy-agent/internal/permissions"
)

func newPermissionedShellCommand(ctx context.Context, script string, policy permissions.Policy, roots []string) (*exec.Cmd, error) {
	if policy.IsFullAccess() {
		return newShellCommand(ctx, script), nil
	}
	roots = existingRoots(roots)
	if len(roots) == 0 {
		return nil, fmt.Errorf("权限模式 %s 没有可写目录", policy.Mode)
	}
	if runtimeSandbox, err := exec.LookPath("bwrap"); err == nil && runtimeSandbox != "" && runtime.GOOS != "darwin" {
		args := []string{"--die-with-parent", "--new-session", "--ro-bind", "/", "/", "--proc", "/proc", "--dev", "/dev", "--bind", os.TempDir(), os.TempDir()}
		if !policy.NetworkAccess {
			args = append(args, "--unshare-net")
		}
		for _, root := range roots {
			args = append(args, "--bind", root, root)
		}
		args = append(args, "/bin/sh", "-c", script)
		return exec.CommandContext(ctx, runtimeSandbox, args...), nil
	}
	if runtimeSandbox, err := exec.LookPath("sandbox-exec"); err == nil && runtimeSandbox != "" && runtime.GOOS == "darwin" {
		profile := darwinSandboxProfile(policy, roots)
		return exec.CommandContext(ctx, runtimeSandbox, "-p", profile, "/bin/sh", "-c", script), nil
	}
	return nil, fmt.Errorf("当前系统没有可用的 EasyAgent 工作区沙盒；请安装 bwrap，或选择“完全访问”")
}

func existingRoots(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = filepath.Clean(strings.TrimSpace(value))
		if value == "." || value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		info, err := os.Stat(value)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func darwinSandboxProfile(policy permissions.Policy, roots []string) string {
	parts := []string{"(version 1)", "(allow default)", "(deny file-write*)"}
	for _, root := range roots {
		parts = append(parts, `(allow file-write* (subpath "`+root+`"))`)
	}
	parts = append(parts, `(allow file-write* (subpath "`+os.TempDir()+`"))`)
	if !policy.NetworkAccess {
		parts = append(parts, "(deny network*)")
	}
	return strings.Join(parts, " ")
}
