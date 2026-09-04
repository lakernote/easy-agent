//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package codexruntime

import "os/exec"

func configureProcessTree(cmd *exec.Cmd) {}

func terminateProcessTree(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
