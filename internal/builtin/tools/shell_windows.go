//go:build windows

package tools

import (
	"context"
	"os"
	"os/exec"
	"strconv"
)

func newShellCommand(ctx context.Context, script string) *exec.Cmd {
	command := exec.CommandContext(ctx, "cmd.exe", "/S", "/C", script)
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		// /T includes descendants and /F keeps cancellation bounded.
		if err := exec.Command("taskkill", "/PID", strconv.Itoa(command.Process.Pid), "/T", "/F").Run(); err != nil {
			return command.Process.Kill()
		}
		return nil
	}
	return command
}
