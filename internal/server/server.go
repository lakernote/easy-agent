// Package server 组装 EasyAgent 的 HTTP API、单 Agent 运行时和静态页面。
//
// 这里是应用层，不是 Agent 核心。它负责 SQLite、配置和进程生命周期；核心循环
// 位于 internal/agent，内置能力位于 internal/builtin，彼此通过很小的接口连接。
package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

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
	semaphore   chan struct{}
	tasks       *taskManager
	runtimes    *runtimeRegistry
	codexEnvMu  sync.RWMutex
	codexEnv    map[string]string
	authMu      sync.Mutex
	sessions    map[string]authSession
	authEnabled bool
}

const authCookieName = "easyagent_session"

type authSession struct {
	ExpiresAt time.Time
}

func New(database *store.Store, assets fs.FS, environment *appenv.Environment) *Server {
	return newServer(database, assets, environment, true)
}

// NewForTests keeps existing handler-focused tests independent from browser
// cookie setup. Production always uses New and therefore always enables auth.
func NewForTests(database *store.Store, assets fs.FS, environment *appenv.Environment) *Server {
	return newServer(database, assets, environment, false)
}

func newServer(database *store.Store, assets fs.FS, environment *appenv.Environment, authEnabled bool) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	codexEnv, _ := codexruntime.LoadManagedEnvironment()
	server := &Server{store: database, assets: assets, env: environment, mux: http.NewServeMux(), context: ctx, cancel: cancel, semaphore: make(chan struct{}, 1), tasks: newTaskManager(), codexEnv: codexEnv, sessions: make(map[string]authSession), authEnabled: authEnabled}
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
		if server.requiresAuthentication(request) && !server.isAuthenticated(request) {
			response.Header().Set("Cache-Control", "no-store")
			writeError(response, http.StatusUnauthorized, "需要登录")
			return
		}
		server.mux.ServeHTTP(response, request)
	})
}

func (server *Server) requiresAuthentication(request *http.Request) bool {
	if !server.authEnabled {
		return false
	}
	if request.URL.Path == "/api/v1/auth/login" || request.URL.Path == "/api/v1/health" {
		return false
	}
	return strings.HasPrefix(request.URL.Path, "/api/v1/")
}

func (server *Server) isAuthenticated(request *http.Request) bool {
	cookie, err := request.Cookie(authCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	server.authMu.Lock()
	defer server.authMu.Unlock()
	session, ok := server.sessions[cookie.Value]
	if !ok || time.Now().After(session.ExpiresAt) {
		delete(server.sessions, cookie.Value)
		return false
	}
	return true
}

func (server *Server) createAuthSession(response http.ResponseWriter, request *http.Request) bool {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		writeError(response, http.StatusInternalServerError, "无法创建登录会话")
		return false
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	expiresAt := time.Now().Add(12 * time.Hour)
	server.authMu.Lock()
	now := time.Now()
	for value, session := range server.sessions {
		if now.After(session.ExpiresAt) {
			delete(server.sessions, value)
		}
	}
	server.sessions[token] = authSession{ExpiresAt: expiresAt}
	server.authMu.Unlock()
	http.SetCookie(response, &http.Cookie{Name: authCookieName, Value: token, Path: "/", Expires: expiresAt, MaxAge: 12 * 60 * 60, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: request.TLS != nil})
	return true
}

func (server *Server) clearAuthSession(response http.ResponseWriter, request *http.Request) {
	if cookie, err := request.Cookie(authCookieName); err == nil {
		server.authMu.Lock()
		delete(server.sessions, cookie.Value)
		server.authMu.Unlock()
	}
	http.SetCookie(response, &http.Cookie{Name: authCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: request.TLS != nil})
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
