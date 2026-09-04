//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package tools

import (
	"context"
	"os/exec"
)

func newShellCommand(ctx context.Context, script string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", script)
}
