package server

import (
	"context"
	"sync"
)

// taskScheduler 是两个 Runtime 共用的进程内执行门。它同时限制总并发，并让
// 没有 worktree 隔离的同一工作区保持串行。queued 状态仍以 SQLite 为准。
type taskScheduler struct {
	mu       sync.Mutex
	limit    int
	active   int
	projects map[string]int
	changed  chan struct{}
}

func newTaskScheduler(limit int) *taskScheduler {
	if limit < 1 {
		limit = 1
	}
	return &taskScheduler{limit: limit, projects: make(map[string]int), changed: make(chan struct{})}
}

func (scheduler *taskScheduler) setLimit(limit int) {
	if limit < 1 {
		limit = 1
	}
	scheduler.mu.Lock()
	scheduler.limit = limit
	scheduler.signalLocked()
	scheduler.mu.Unlock()
}

func (scheduler *taskScheduler) snapshot() (active, limit int) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return scheduler.active, scheduler.limit
}

func (scheduler *taskScheduler) acquire(ctx context.Context, project string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		scheduler.mu.Lock()
		projectAvailable := project == "" || scheduler.projects[project] == 0
		if scheduler.active < scheduler.limit && projectAvailable {
			scheduler.active++
			if project != "" {
				scheduler.projects[project]++
			}
			scheduler.mu.Unlock()
			return nil
		}
		changed := scheduler.changed
		scheduler.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (scheduler *taskScheduler) release(project string) {
	scheduler.mu.Lock()
	if scheduler.active > 0 {
		scheduler.active--
	}
	if project != "" {
		if scheduler.projects[project] <= 1 {
			delete(scheduler.projects, project)
		} else {
			scheduler.projects[project]--
		}
	}
	scheduler.signalLocked()
	scheduler.mu.Unlock()
}

func (scheduler *taskScheduler) signalLocked() {
	close(scheduler.changed)
	scheduler.changed = make(chan struct{})
}
