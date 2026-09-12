package server

import (
	"context"
	"sync"
)

// taskScheduler 是两个 Runtime 共用的进程内执行门。它同时限制总并发，并让
// 共享工作区或同一个本地模型端点等互斥资源保持串行。queued 状态仍以 SQLite 为准。
type taskScheduler struct {
	mu        sync.Mutex
	limit     int
	active    int
	resources map[string]int
	changed   chan struct{}
}

func newTaskScheduler(limit int) *taskScheduler {
	if limit < 1 {
		limit = 1
	}
	return &taskScheduler{limit: limit, resources: make(map[string]int), changed: make(chan struct{})}
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

func (scheduler *taskScheduler) acquire(ctx context.Context, resources ...string) error {
	resources = uniqueResourceKeys(resources)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		scheduler.mu.Lock()
		resourcesAvailable := true
		for _, resource := range resources {
			if scheduler.resources[resource] > 0 {
				resourcesAvailable = false
				break
			}
		}
		if scheduler.active < scheduler.limit && resourcesAvailable {
			scheduler.active++
			for _, resource := range resources {
				scheduler.resources[resource]++
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

func (scheduler *taskScheduler) release(resources ...string) {
	resources = uniqueResourceKeys(resources)
	scheduler.mu.Lock()
	if scheduler.active > 0 {
		scheduler.active--
	}
	for _, resource := range resources {
		if scheduler.resources[resource] <= 1 {
			delete(scheduler.resources, resource)
		} else {
			scheduler.resources[resource]--
		}
	}
	scheduler.signalLocked()
	scheduler.mu.Unlock()
}

func uniqueResourceKeys(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (scheduler *taskScheduler) signalLocked() {
	close(scheduler.changed)
	scheduler.changed = make(chan struct{})
}
