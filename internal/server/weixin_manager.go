package server

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/lakernote/easy-agent/internal/store"
	"github.com/lakernote/easy-agent/internal/weixin"
)

// weixinManager coordinates worker lifecycles. Login, inbox and task behavior
// live in focused sibling files; protocol and persistence stay in their packages.
type weixinManager struct {
	server   *Server
	gateway  weixin.Gateway
	mu       sync.Mutex
	logins   map[string]*weixinLogin
	pollers  map[string]weixinPoller
	delivery map[string]struct{}
}

type weixinPoller struct {
	cancel context.CancelFunc
	token  string
}

func newWeixinManager(server *Server, gateway weixin.Gateway) *weixinManager {
	return &weixinManager{server: server, gateway: gateway, logins: make(map[string]*weixinLogin), pollers: make(map[string]weixinPoller), delivery: make(map[string]struct{})}
}

func (manager *weixinManager) start() {
	settings, err := manager.server.store.GetWeixinSettings()
	if err != nil || !settings.Enabled {
		return
	}
	accounts, err := manager.server.store.ListWeixinAccounts()
	if err != nil {
		log.Printf("微信远程：读取绑定失败: %v", err)
		return
	}
	for _, account := range accounts {
		if account.Enabled {
			manager.startPoller(account.ID)
			manager.resumeDelivery(account.ID)
		}
	}
}

func (manager *weixinManager) stopAll() {
	manager.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(manager.pollers))
	for id, poller := range manager.pollers {
		cancels = append(cancels, poller.cancel)
		delete(manager.pollers, id)
	}
	manager.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (manager *weixinManager) restartAccount(id string) {
	manager.stopAccount(id)
	settings, err := manager.server.store.GetWeixinSettings()
	if err != nil || !settings.Enabled {
		return
	}
	account, err := manager.server.store.GetWeixinAccount(id)
	if err != nil || !account.Enabled {
		return
	}
	manager.startPoller(id)
	manager.resumeDelivery(id)
}

func (manager *weixinManager) stopAccount(id string) {
	manager.mu.Lock()
	poller := manager.pollers[id]
	delete(manager.pollers, id)
	manager.mu.Unlock()
	if poller.cancel != nil {
		poller.cancel()
	}
}

func (manager *weixinManager) startPoller(id string) {
	manager.mu.Lock()
	if _, exists := manager.pollers[id]; exists {
		manager.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(manager.server.context)
	token := newID()
	manager.pollers[id] = weixinPoller{cancel: cancel, token: token}
	manager.mu.Unlock()
	manager.server.wait.Add(1)
	go func() {
		defer manager.server.wait.Done()
		defer func() {
			manager.mu.Lock()
			if current, ok := manager.pollers[id]; ok && current.token == token {
				delete(manager.pollers, id)
			}
			manager.mu.Unlock()
		}()
		manager.poll(ctx, id)
	}()
}

func gatewayAccount(account store.WeixinAccount) weixin.Account {
	return weixin.Account{ID: account.ID, Token: account.Token, BaseURL: account.BaseURL}
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
