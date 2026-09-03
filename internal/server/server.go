// Package server 组装 EasyAgent 的 HTTP API、单 Agent 运行时和静态页面。
//
// 这里是应用层，不是 Agent 核心。它负责 SQLite、配置和进程生命周期；核心循环
// 位于 internal/agent，内置能力位于 internal/builtin，彼此通过很小的接口连接。
package server

import (
	"context"
	"io/fs"
	"net/http"
	"sync"

	"github.com/lakernote/easy-agent/internal/appenv"
	"github.com/lakernote/easy-agent/internal/codexruntime"
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
	semaphore  chan struct{}
	tasks      *taskManager
	runtimes   *runtimeRegistry
	codexEnvMu sync.RWMutex
	codexEnv   map[string]string
}

func New(database *store.Store, assets fs.FS, environment *appenv.Environment) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	codexEnv, _ := codexruntime.LoadManagedEnvironment()
	server := &Server{store: database, assets: assets, env: environment, mux: http.NewServeMux(), context: ctx, cancel: cancel, semaphore: make(chan struct{}, 1), tasks: newTaskManager(), codexEnv: codexEnv}
	server.runtimes = newRuntimeRegistry(server)
	server.routes()
	return server
}

func (server *Server) codexEnvironment() []string {
	server.codexEnvMu.RLock()
	values := make(map[string]string, len(server.codexEnv))
	for key, value := range server.codexEnv {
		values[key] = value
	}
	server.codexEnvMu.RUnlock()
	return server.env.Environ(values)
}

func (server *Server) reloadCodexEnvironment() error {
	values, err := codexruntime.LoadManagedEnvironment()
	if err != nil {
		return err
	}
	server.codexEnvMu.Lock()
	server.codexEnv = values
	server.codexEnvMu.Unlock()
	return nil
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
