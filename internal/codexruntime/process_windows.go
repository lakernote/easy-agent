//go:build windows

package codexruntime

import (
	"os/exec"
	"strconv"
)

func configureProcessTree(cmd *exec.Cmd) {}

func terminateProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// taskkill /T terminates descendants created by app-server as well as the
	// app-server process itself. Process.Kill remains a fallback if taskkill is
	// unavailable or the process exits while taskkill is inspecting the tree.
	_ = exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run()
	_ = cmd.Process.Kill()
}
