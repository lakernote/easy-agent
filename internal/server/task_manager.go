package server

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/lakernote/easy-agent/internal/store"
)

// taskManager 只负责进程内任务状态，不参与 SQLite 持久化。
//
// 会话状态由 store.Store 负责，partial/progress/usage/cancel 这些短生命周期
// 状态由这里负责，避免 Server 同时承担 HTTP、持久化和并发状态管理。
type taskManager struct {
	mu    sync.Mutex
	tasks map[string]taskHandle
}

type taskHandle struct {
	token    string
	cancel   context.CancelFunc
	done     chan struct{}
	partial  string
	progress string
	usage    store.Usage
	pending  *pendingCodexRequest
}

type pendingCodexRequest struct {
	ID       string
	Method   string
	Params   json.RawMessage
	Response chan pendingCodexResponse
}

type pendingCodexResponse struct {
	Value any
	Err   error
}

func newTaskManager() *taskManager {
	return &taskManager{tasks: make(map[string]taskHandle)}
}

// hasTask 保留在 Server 上作为测试和同包旧调用的窄兼容入口；新的任务状态
// 操作都应直接通过 taskManager 完成。
func (server *Server) hasTask(id string) bool {
	return server.tasks.has(id)
}

func (manager *taskManager) has(id string) bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	_, exists := manager.tasks[id]
	return exists
}

func (manager *taskManager) set(id, token string, cancel context.CancelFunc) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.tasks[id] = taskHandle{token: token, cancel: cancel, done: make(chan struct{}), progress: "任务排队中 · 等待本地执行槽"}
}

func (manager *taskManager) appendPartial(id, delta string) {
	if delta == "" {
		return
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current, ok := manager.tasks[id]
	if !ok {
		return
	}
	current.partial += delta
	manager.tasks[id] = current
}

func (manager *taskManager) resetPartial(id string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current, ok := manager.tasks[id]
	if !ok {
		return
	}
	current.partial = ""
	manager.tasks[id] = current
}

func (manager *taskManager) partial(id string) string {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.tasks[id].partial
}

func (manager *taskManager) setUsage(id string, usage store.Usage) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current, ok := manager.tasks[id]
	if !ok {
		return
	}
	current.usage = usage
	manager.tasks[id] = current
}

func (manager *taskManager) usage(id string) store.Usage {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.tasks[id].usage
}

func (manager *taskManager) setProgress(id, progress string) {
	if progress == "" {
		return
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current, ok := manager.tasks[id]
	if !ok {
		return
	}
	current.progress = progress
	manager.tasks[id] = current
}

func (manager *taskManager) progress(id string) string {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.tasks[id].progress
}

func (manager *taskManager) setPending(id string, request pendingCodexRequest) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current, ok := manager.tasks[id]
	if !ok {
		return context.Canceled
	}
	if current.pending != nil {
		return errors.New("已有一个待处理的 Codex 请求")
	}
	current.pending = &request
	current.progress = "Codex · 等待 UI 确认"
	manager.tasks[id] = current
	return nil
}

func (manager *taskManager) pending(id string) *pendingCodexRequest {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current, ok := manager.tasks[id]
	if !ok || current.pending == nil {
		return nil
	}
	value := *current.pending
	return &value
}

func (manager *taskManager) resolvePending(id, requestID string, value any) bool {
	manager.mu.Lock()
	current, ok := manager.tasks[id]
	if !ok || current.pending == nil || current.pending.ID != requestID {
		manager.mu.Unlock()
		return false
	}
	pending := current.pending
	current.pending = nil
	manager.tasks[id] = current
	manager.mu.Unlock()
	select {
	case pending.Response <- pendingCodexResponse{Value: value}:
	default:
	}
	return true
}

func (manager *taskManager) clearPending(id string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current, ok := manager.tasks[id]
	if ok {
		current.pending = nil
		manager.tasks[id] = current
	}
}

func (manager *taskManager) clear(id, token string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if current, ok := manager.tasks[id]; ok && current.token == token {
		delete(manager.tasks, id)
		close(current.done)
	}
}

func (manager *taskManager) cancel(id string) {
	manager.mu.Lock()
	current := manager.tasks[id]
	manager.mu.Unlock()
	if current.cancel != nil {
		current.cancel()
	}
}

func (manager *taskManager) wait(ctx context.Context, id string) error {
	manager.mu.Lock()
	current, ok := manager.tasks[id]
	manager.mu.Unlock()
	if !ok {
		return nil
	}
	select {
	case <-current.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
