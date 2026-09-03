package server

import (
	"context"
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
	partial  string
	progress string
	usage    store.Usage
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
	manager.tasks[id] = taskHandle{token: token, cancel: cancel, progress: "任务排队中 · 等待本地执行槽"}
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

func (manager *taskManager) clear(id, token string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if current, ok := manager.tasks[id]; ok && current.token == token {
		delete(manager.tasks, id)
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
