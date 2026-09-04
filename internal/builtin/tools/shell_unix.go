//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package tools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func newShellCommand(ctx context.Context, script string) *exec.Cmd {
	command := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	// Run each command in its own process group so cancellation also stops
	// test runners, installers, and other descendants created by the shell.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return command
}
