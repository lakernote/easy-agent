//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package codexruntime

import (
	"os/exec"
	"syscall"
)

func configureProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// app-server may create shell/MCP/tool descendants. Killing only the
	// top-level process leaves those descendants behind after cancellation.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
