package codexruntime

import (
	"io"
	"os/exec"
	"time"
)

const appServerExitGracePeriod = 750 * time.Millisecond

// stopProcess lets app-server observe EOF and release any thread writer lock
// before falling back to a process-tree kill. This matters when a canceled turn
// is followed quickly by another turn that resumes the same persisted thread.
func stopProcess(command *exec.Cmd, stdin io.Closer) {
	if stdin != nil {
		_ = stdin.Close()
	}
	if command == nil || command.Process == nil {
		return
	}
	exited := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(exited)
	}()
	timer := time.NewTimer(appServerExitGracePeriod)
	defer timer.Stop()
	select {
	case <-exited:
		return
	case <-timer.C:
		terminateProcessTree(command)
		<-exited
	}
}
