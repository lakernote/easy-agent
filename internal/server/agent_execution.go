package server

import (
	"context"
	"fmt"

	"github.com/lakernote/easy-agent/internal/store"
)

func (server *Server) runAgentTurn(ctx context.Context, id string, settings store.ModelSettings, usage *store.Usage) error {
	session, err := server.store.RuntimeSession(id)
	if err != nil {
		return err
	}
	// 工作区属于会话，而不是服务进程。旧会话的空值自动落到默认工作区；新会话
	// 使用页面创建时选择并保存的绝对目录。
	runEnvironment, err := server.env.WithWorkspace(session.Workspace)
	if err != nil {
		return fmt.Errorf("打开会话工作区: %w", err)
	}
	registry := server.runtimes
	if registry == nil {
		registry = newRuntimeRegistry(server)
	}
	executor, err := registry.resolve(session.Runtime)
	if err != nil {
		return err
	}
	return executor.Run(runtimeTurnRequest{
		Context: ctx, ID: id, Session: session, Settings: settings,
		Environment: runEnvironment, Usage: usage,
	})
}
