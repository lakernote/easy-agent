package server

import "time"

const retentionCleanupInterval = 24 * time.Hour

func (server *Server) startRetentionScheduler() {
	if !server.beginBackground() {
		return
	}
	go func() {
		defer server.completeBackground()
		server.runRetentionCleanup()
		ticker := time.NewTicker(retentionCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-server.ctx.Done():
				return
			case <-ticker.C:
				server.runRetentionCleanup()
			}
		}
	}()
}

func (server *Server) triggerRetentionCleanup() {
	if !server.beginBackground() {
		return
	}
	go func() {
		defer server.completeBackground()
		server.runRetentionCleanup()
	}()
}

func (server *Server) runRetentionCleanup() {
	server.retentionMu.Lock()
	defer server.retentionMu.Unlock()
	settings, err := server.store.GetRuntimeSettings()
	if err != nil {
		return
	}
	_, _ = server.store.PurgeExpiredSessions(time.Now(), settings.RetentionDays)
}
