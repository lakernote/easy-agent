package server

import "time"

func (server *Server) startAutomationScheduler() {
	if !server.beginBackground() {
		return
	}
	go func() {
		defer server.completeBackground()
		server.automationLoop()
	}()
}

func (server *Server) automationLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	server.runDueAutomationTasks()
	for {
		select {
		case <-server.ctx.Done():
			return
		case <-ticker.C:
			server.runDueAutomationTasks()
		}
	}
}

func (server *Server) runDueAutomationTasks() {
	tasks, err := server.store.ListDueAutomationTasks(time.Now())
	if err != nil {
		return
	}
	for _, task := range tasks {
		_, _ = server.triggerAutomationTask(server.ctx, task)
	}
}
