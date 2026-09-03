package server

import (
	"context"
	"fmt"

	"github.com/lakernote/easy-agent/internal/appenv"
	"github.com/lakernote/easy-agent/internal/store"
)

// runtimeTurnRequest 是应用层交给 Runtime 的统一输入。
// Runtime 只负责执行一轮，HTTP、任务状态和持久化仍由 Server 管理。
type runtimeTurnRequest struct {
	Context     context.Context
	ID          string
	Session     store.Session
	Settings    store.ModelSettings
	Environment *appenv.Environment
	Usage       *store.Usage
}

type runtimeExecutor interface {
	Name() string
	Run(request runtimeTurnRequest) error
}

type runtimeRegistry struct {
	executors map[string]runtimeExecutor
}

func newRuntimeRegistry(server *Server) *runtimeRegistry {
	return &runtimeRegistry{executors: map[string]runtimeExecutor{
		store.RuntimeEasyAgent: easyAgentExecutor{server: server},
		store.RuntimeCodex:     codexExecutor{server: server},
	}}
}

func (registry *runtimeRegistry) resolve(name string) (runtimeExecutor, error) {
	if name == "" {
		name = store.RuntimeEasyAgent
	}
	executor, ok := registry.executors[name]
	if !ok {
		return nil, fmt.Errorf("不支持的 Runtime: %s", name)
	}
	return executor, nil
}

type easyAgentExecutor struct{ server *Server }

func (executor easyAgentExecutor) Name() string { return store.RuntimeEasyAgent }

func (executor easyAgentExecutor) Run(request runtimeTurnRequest) error {
	return executor.server.runEasyAgentTurn(request.Context, request.ID, request.Session, request.Settings, request.Environment, request.Usage)
}

type codexExecutor struct{ server *Server }

func (executor codexExecutor) Name() string { return store.RuntimeCodex }

func (executor codexExecutor) Run(request runtimeTurnRequest) error {
	return executor.server.runCodexTurn(request.Context, request.Session, request.Settings, request.Environment.Workspace(), request.Usage)
}
