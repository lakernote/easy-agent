package server

import "net/http"

func (server *Server) routes() {
	server.mux.HandleFunc("GET /api/v1/health", server.health)
	server.mux.HandleFunc("GET /api/v1/bootstrap", server.bootstrap)
	server.mux.HandleFunc("GET /api/v1/usage", server.usage)
	server.mux.HandleFunc("GET /api/v1/sessions/history", server.getOlderSessions)
	server.mux.HandleFunc("GET /api/v1/sessions/{id}", server.getSession)
	server.mux.HandleFunc("GET /api/v1/sessions/{id}/history", server.getSessionHistory)
	server.mux.HandleFunc("GET /api/v1/attachments/{id}", server.getAttachment)
	server.mux.HandleFunc("POST /api/v1/sessions", server.createSession)
	server.mux.HandleFunc("POST /api/v1/sessions/{id}/messages", server.continueSession)
	server.mux.HandleFunc("POST /api/v1/sessions/{id}/cancel", server.cancelSession)
	server.mux.HandleFunc("DELETE /api/v1/sessions/{id}", server.deleteSession)
	server.mux.HandleFunc("PUT /api/v1/model", server.saveModel)
	server.mux.HandleFunc("DELETE /api/v1/model/{id}", server.deleteModelProfile)
	server.mux.HandleFunc("POST /api/v1/model/test", server.testModel)
	server.mux.HandleFunc("GET /api/v1/ollama", server.getOllama)
	server.mux.HandleFunc("GET /api/v1/codex", server.getCodex)
	server.mux.HandleFunc("GET /api/v1/codex/config", server.getCodexConfig)
	server.mux.HandleFunc("PUT /api/v1/codex/config", server.saveCodexConfig)
	server.mux.HandleFunc("POST /api/v1/codex/install", server.installCodex)
	server.mux.HandleFunc("POST /api/v1/ollama/use", server.useOllama)
	server.mux.HandleFunc("PUT /api/v1/skills/{name}", server.saveSkill)
	server.mux.HandleFunc("DELETE /api/v1/skills/{name}", server.resetSkill)
	server.mux.HandleFunc("PUT /api/v1/mcp/{id}", server.saveMCP)
	server.mux.HandleFunc("DELETE /api/v1/mcp/{id}", server.deleteMCP)
	server.mux.HandleFunc("POST /api/v1/mcp/{id}/test", server.testMCP)
	server.mux.HandleFunc("POST /api/v1/mcp/presets/{id}/check", server.checkMCPPreset)
	server.mux.HandleFunc("POST /api/v1/mcp/presets/{id}/install", server.installMCPPreset)
	server.mux.HandleFunc("DELETE /api/v1/mcp/presets/{id}/install", server.uninstallMCPPreset)
	// API 拼错时必须返回 JSON 404，不能落到单页应用入口并伪装成 200 成功。
	server.mux.HandleFunc("GET /api/", func(response http.ResponseWriter, request *http.Request) {
		writeError(response, http.StatusNotFound, "API 不存在")
	})
	server.mux.HandleFunc("GET /", server.static)
}
