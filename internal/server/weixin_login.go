package server

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/store"
	"github.com/lakernote/easy-agent/internal/weixin"
)

type weixinLogin struct {
	ID         string
	Label      string
	Code       string
	Content    string
	Status     string
	Message    string
	BaseURL    string
	VerifyCode string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (manager *weixinManager) beginLogin(ctx context.Context, label string) (*weixinLogin, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return nil, errors.New("请填写绑定备注")
	}
	accounts, err := manager.server.store.ListWeixinAccounts()
	if err != nil {
		return nil, err
	}
	tokens := make([]string, 0, len(accounts))
	for _, account := range accounts {
		if account.Token != "" {
			tokens = append(tokens, account.Token)
		}
	}
	started, err := manager.gateway.StartLogin(ctx, tokens)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	login := &weixinLogin{ID: newID(), Label: label, Code: started.Code, Content: started.Content, Status: "wait", Message: "等待微信扫码", BaseURL: weixin.DefaultBaseURL, CreatedAt: now, UpdatedAt: now}
	manager.mu.Lock()
	manager.logins[login.ID] = login
	manager.mu.Unlock()
	manager.server.wait.Add(1)
	go func(id string) {
		defer manager.server.wait.Done()
		manager.pollLogin(manager.server.context, id)
	}(login.ID)
	copy := *login
	return &copy, nil
}

func (manager *weixinManager) getLogin(id string) (*weixinLogin, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	login, ok := manager.logins[id]
	if !ok {
		return nil, false
	}
	copy := *login
	return &copy, true
}

func (manager *weixinManager) setVerifyCode(id, code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return errors.New("请输入手机微信显示的数字")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	login, ok := manager.logins[id]
	if !ok {
		return sql.ErrNoRows
	}
	login.VerifyCode = code
	login.Message = "正在验证"
	login.UpdatedAt = time.Now()
	return nil
}

func (manager *weixinManager) cancelLogin(id string) bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, ok := manager.logins[id]; !ok {
		return false
	}
	delete(manager.logins, id)
	return true
}

func (manager *weixinManager) pollLogin(ctx context.Context, id string) {
	defer manager.cleanupLoginLater(id)
	deadline := time.Now().Add(8 * time.Minute)
	for ctx.Err() == nil && time.Now().Before(deadline) {
		login, ok := manager.getLogin(id)
		if !ok {
			return
		}
		status, err := manager.gateway.PollLogin(ctx, login.BaseURL, login.Code, login.VerifyCode)
		if err != nil {
			manager.updateLogin(id, "failed", "扫码连接失败："+err.Error(), "")
			return
		}
		if _, stillActive := manager.getLogin(id); !stillActive {
			return
		}
		switch status.Status {
		case "wait":
			manager.updateLogin(id, "wait", "等待微信扫码", "")
		case "scaned":
			manager.updateLogin(id, "scaned", "已扫码，请在手机上确认", "")
		case "need_verifycode":
			manager.updateLogin(id, "need_verifycode", "请输入手机微信显示的数字", "")
		case "scaned_but_redirect":
			if status.RedirectHost != "" {
				baseURL := status.RedirectHost
				if !strings.HasPrefix(baseURL, "https://") {
					baseURL = "https://" + baseURL
				}
				manager.updateLogin(id, "scaned", "已扫码，正在连接", baseURL)
			}
		case "confirmed":
			if status.AccountID == "" || status.UserID == "" || status.BotToken == "" {
				manager.updateLogin(id, "failed", "微信确认成功，但返回的绑定信息不完整", "")
				return
			}
			baseURL := strings.TrimSpace(status.BaseURL)
			if baseURL == "" {
				baseURL = login.BaseURL
			} else if !strings.HasPrefix(baseURL, "https://") {
				baseURL = "https://" + baseURL
			}
			accounts, _ := manager.server.store.ListWeixinAccounts()
			for _, previous := range accounts {
				if previous.UserID == status.UserID && previous.ID != status.AccountID {
					manager.stopAccount(previous.ID)
				}
			}
			now := time.Now()
			projectID := ""
			if err := manager.server.ensureDefaultProject(); err == nil {
				if project, projectErr := manager.server.store.DefaultProject(); projectErr == nil {
					projectID = project.ID
				}
			}
			account := store.WeixinAccount{ID: status.AccountID, Label: login.Label, UserID: status.UserID, Token: status.BotToken, BaseURL: baseURL, Enabled: true, ProjectID: projectID, IgnoreBefore: now, CreatedAt: now, UpdatedAt: now}
			if err := manager.server.store.SaveWeixinAccount(account); err != nil {
				manager.updateLogin(id, "failed", "保存微信绑定失败："+err.Error(), "")
				return
			}
			manager.updateLogin(id, "confirmed", "绑定成功", baseURL)
			manager.restartAccount(account.ID)
			return
		case "expired":
			manager.updateLogin(id, "expired", "二维码已过期，请重新生成", "")
			return
		case "binded_redirect":
			manager.updateLogin(id, "already_bound", "这个微信已经绑定，无需重复扫码", "")
			return
		case "verify_code_blocked":
			manager.updateLogin(id, "failed", "验证次数过多，请稍后重新扫码", "")
			return
		default:
			manager.updateLogin(id, status.Status, "等待微信确认", "")
		}
		if !waitContext(ctx, time.Second) {
			return
		}
	}
	if ctx.Err() == nil {
		manager.updateLogin(id, "expired", "扫码登录已超时，请重新生成", "")
	}
}

func (manager *weixinManager) cleanupLoginLater(id string) {
	manager.mu.Lock()
	_, exists := manager.logins[id]
	manager.mu.Unlock()
	if !exists {
		return
	}
	manager.server.wait.Add(1)
	go func() {
		defer manager.server.wait.Done()
		timer := time.NewTimer(10 * time.Minute)
		defer timer.Stop()
		select {
		case <-manager.server.context.Done():
		case <-timer.C:
			manager.mu.Lock()
			delete(manager.logins, id)
			manager.mu.Unlock()
		}
	}()
}

func (manager *weixinManager) updateLogin(id, status, message, baseURL string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if login := manager.logins[id]; login != nil {
		login.Status = status
		login.Message = message
		if baseURL != "" {
			login.BaseURL = baseURL
		}
		if status == "scaned" {
			login.VerifyCode = ""
		}
		login.UpdatedAt = time.Now()
	}
}
