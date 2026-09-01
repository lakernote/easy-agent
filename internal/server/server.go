// Package server 组装 EasyAgent 的 HTTP API、单 Agent 运行时和静态页面。
//
// 这里是应用层，不是 Agent 核心。它负责 SQLite、配置和进程生命周期；核心循环
// 位于 internal/agent，内置能力位于 internal/builtin，彼此通过很小的接口连接。
package server

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/lakernote/easy-agent/internal/appenv"
	"github.com/lakernote/easy-agent/internal/store"
)

type Server struct {
	store  *store.Store
	assets fs.FS
	env    *appenv.Environment
	mux    *http.ServeMux

	context context.Context
	cancel  context.CancelFunc
	wait    sync.WaitGroup
	// 单机版默认只同时运行一个模型任务，避免本地模型争抢内存。
	semaphore chan struct{}
	taskMu    sync.Mutex
	tasks     map[string]taskHandle
}

type taskHandle struct {
	token   string
	cancel  context.CancelFunc
	partial string
}

func New(database *store.Store, assets fs.FS, environment *appenv.Environment) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	server := &Server{store: database, assets: assets, env: environment, mux: http.NewServeMux(), context: ctx, cancel: cancel, semaphore: make(chan struct{}, 1), tasks: make(map[string]taskHandle)}
	server.routes()
	return server
}

func (server *Server) Handler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		response.Header().Set("Referrer-Policy", "no-referrer")
		server.mux.ServeHTTP(response, request)
	})
}

func (server *Server) Shutdown(ctx context.Context) error {
	server.cancel()
	done := make(chan struct{})
	go func() { server.wait.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (server *Server) routes() {
	server.mux.HandleFunc("GET /api/v1/health", server.health)
	server.mux.HandleFunc("GET /api/v1/bootstrap", server.bootstrap)
	server.mux.HandleFunc("GET /api/v1/sessions/{id}", server.getSession)
	server.mux.HandleFunc("GET /api/v1/attachments/{id}", server.getAttachment)
	server.mux.HandleFunc("POST /api/v1/sessions", server.createSession)
	server.mux.HandleFunc("POST /api/v1/sessions/{id}/messages", server.continueSession)
	server.mux.HandleFunc("POST /api/v1/sessions/{id}/cancel", server.cancelSession)
	server.mux.HandleFunc("DELETE /api/v1/sessions/{id}", server.deleteSession)
	server.mux.HandleFunc("PUT /api/v1/model", server.saveModel)
	server.mux.HandleFunc("POST /api/v1/model/test", server.testModel)
	server.mux.HandleFunc("GET /api/v1/ollama", server.getOllama)
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

func (server *Server) static(response http.ResponseWriter, request *http.Request) {
	name := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	data, err := fs.ReadFile(server.assets, name)
	if err != nil {
		// 前端使用 history API 时，未知路径回退到入口页面。
		data, err = fs.ReadFile(server.assets, "index.html")
		name = "index.html"
	}
	if err != nil {
		http.Error(response, "frontend not built", http.StatusNotFound)
		return
	}
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		response.Header().Set("Content-Type", contentType)
	}
	response.Header().Set("Cache-Control", "no-store")
	_, _ = response.Write(data)
}

func (server *Server) health(response http.ResponseWriter, request *http.Request) {
	if err := server.store.Ping(request.Context()); err != nil {
		writeError(response, http.StatusServiceUnavailable, "SQLite 不可用")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "name": "EasyAgent", "time": time.Now()})
}

func decodeJSON(response http.ResponseWriter, request *http.Request, value any) bool {
	// 附件使用 JSON Base64 传输；10 MiB 原始数据编码后约为 13.4 MiB。
	request.Body = http.MaxBytesReader(response, request.Body, 16*1024*1024)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(response, http.StatusBadRequest, "请求格式不正确: "+err.Error())
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			writeError(response, http.StatusBadRequest, "请求只能包含一个 JSON 对象")
		} else {
			writeError(response, http.StatusBadRequest, "请求格式不正确: "+err.Error())
		}
		return false
	}
	return true
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, map[string]string{"error": message})
}
