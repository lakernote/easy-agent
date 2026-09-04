package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/store"
	"github.com/lakernote/easy-agent/internal/weixin"
)

func (manager *weixinManager) handleCommand(account store.WeixinAccount, message weixin.Message, text string) bool {
	command := strings.ToLower(strings.TrimSpace(text))
	switch command {
	case "/help", "帮助":
		manager.send(account, message.FromUserID, message.ContextToken, "直接发送文字即可创建或继续任务。\n/new 新会话\n/status 查看状态\n/stop 停止当前任务")
		return true
	case "/new", "新会话":
		if session, err := manager.currentSession(account); err == nil && activeSessionStatus(session.Status) {
			manager.send(account, message.FromUserID, message.ContextToken, "当前任务仍在执行，请先发送 /stop。")
			return true
		}
		_ = manager.server.store.SetWeixinCurrentSession(account.ID, "", time.Now())
		manager.send(account, message.FromUserID, message.ContextToken, "已切换到新会话，下一条消息会创建新任务。")
		return true
	case "/status", "状态":
		session, err := manager.currentSession(account)
		if errors.Is(err, sql.ErrNoRows) || strings.TrimSpace(account.CurrentSessionID) == "" {
			manager.send(account, message.FromUserID, message.ContextToken, "当前没有会话。")
		} else if err != nil {
			manager.send(account, message.FromUserID, message.ContextToken, "暂时无法读取任务状态。")
		} else {
			manager.send(account, message.FromUserID, message.ContextToken, fmt.Sprintf("当前任务：%s\n状态：%s\n运行时：%s", session.Title, session.Status, session.Runtime))
		}
		return true
	case "/stop", "停止":
		session, err := manager.currentSession(account)
		if err != nil || !activeSessionStatus(session.Status) {
			manager.send(account, message.FromUserID, message.ContextToken, "当前没有正在执行的任务。")
			return true
		}
		changed, cancelErr := manager.server.store.CancelSession(session.ID, time.Now())
		if cancelErr != nil || !changed {
			manager.send(account, message.FromUserID, message.ContextToken, "任务已经结束，未执行停止。")
			return true
		}
		manager.server.tasks.cancel(session.ID)
		manager.send(account, message.FromUserID, message.ContextToken, "已停止当前任务。")
		return true
	default:
		return false
	}
}

func (manager *weixinManager) submit(account store.WeixinAccount, message weixin.Message, text string, messageID int64, createdAt time.Time) error {
	if current, err := manager.currentSession(account); err == nil && activeSessionStatus(current.Status) {
		return errors.New("上一条任务仍在执行，可发送 /status 或 /stop")
	}
	now := time.Now()
	sessionID := account.CurrentSessionID
	var model store.ModelSettings
	if sessionID != "" {
		current, err := manager.server.store.LoadSessionWindow(sessionID, 1, 1)
		if err == nil {
			model, err = manager.server.store.GetModelSettingsByProfileID(current.ProfileID)
			if err != nil {
				return err
			}
			if err := manager.server.enqueueTurn(sessionID, text, nil, model); err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		} else {
			sessionID = ""
		}
	}
	if sessionID == "" {
		var err error
		model, err = manager.server.store.GetModelSettings()
		if err != nil {
			return err
		}
		runEnvironment, err := manager.server.env.WithWorkspace("")
		if err != nil {
			return err
		}
		sessionID = newID()
		runtimeSettings, _ := manager.server.store.GetRuntimeSettings()
		workspace := manager.server.prepareSessionWorkspace(manager.server.context, sessionID, runEnvironment.Workspace(), runtimeSettings)
		if _, err := manager.server.store.CreateSessionWithProfile(sessionID, makeTitle(text), model.Runtime, model.ProfileID, model.Model, workspace.Execution, now); err != nil {
			return err
		}
		if err := manager.server.store.SetSessionWorkspace(sessionID, workspace.Execution, workspace.Source, workspace.Branch); err != nil {
			_ = manager.server.store.DeleteSession(sessionID)
			return err
		}
		if err := manager.server.enqueueTurn(sessionID, text, nil, model); err != nil {
			_ = manager.server.store.DeleteSession(sessionID)
			return err
		}
	}
	if createdAt.IsZero() {
		createdAt = now
	}
	if err := manager.server.store.SetWeixinTask(account.ID, sessionID, messageID, message.ContextToken, createdAt, now); err != nil {
		return err
	}
	manager.resumeDelivery(account.ID)
	return nil
}

func (manager *weixinManager) resumeDelivery(accountID string) {
	manager.mu.Lock()
	if _, exists := manager.delivery[accountID]; exists {
		manager.mu.Unlock()
		return
	}
	manager.delivery[accountID] = struct{}{}
	manager.mu.Unlock()
	manager.server.wait.Add(1)
	go func() {
		defer manager.server.wait.Done()
		defer func() {
			manager.mu.Lock()
			delete(manager.delivery, accountID)
			manager.mu.Unlock()
		}()
		account, err := manager.server.store.GetWeixinAccount(accountID)
		if err != nil || account.PendingMessageID == 0 || account.PendingMessageID <= account.DeliveredMessageID || account.CurrentSessionID == "" {
			return
		}
		_ = manager.server.tasks.wait(manager.server.context, account.CurrentSessionID)
		if manager.server.context.Err() != nil {
			return
		}
		account, err = manager.server.store.GetWeixinAccount(accountID)
		if err != nil || account.PendingMessageID <= account.DeliveredMessageID {
			return
		}
		settings, _ := manager.server.store.GetWeixinSettings()
		if !settings.Enabled || !account.Enabled {
			return
		}
		session, err := manager.server.store.LoadSessionWindow(account.CurrentSessionID, 40, 1)
		if err != nil {
			return
		}
		answer := finalSessionText(session)
		if answer == "" {
			answer = "任务已结束，但没有可发送的文字结果。"
		}
		if err := manager.sendChunks(account, account.UserID, account.PendingContextToken, answer); err != nil {
			log.Printf("微信远程：发送最终结果失败 account=%s: %v", account.ID, err)
			return
		}
		_ = manager.server.store.MarkWeixinDelivered(account.ID, account.PendingMessageID, time.Now())
	}()
}

func (manager *weixinManager) currentSession(account store.WeixinAccount) (store.Session, error) {
	if strings.TrimSpace(account.CurrentSessionID) == "" {
		return store.Session{}, sql.ErrNoRows
	}
	return manager.server.store.LoadSessionWindow(account.CurrentSessionID, 1, 1)
}

func (manager *weixinManager) send(account store.WeixinAccount, userID, contextToken, text string) {
	ctx, cancel := context.WithTimeout(manager.server.context, 20*time.Second)
	defer cancel()
	if err := manager.gateway.SendText(ctx, gatewayAccount(account), userID, contextToken, text); err != nil && ctx.Err() == nil {
		log.Printf("微信远程：发送消息失败 account=%s: %v", account.ID, err)
	}
}

func (manager *weixinManager) sendChunks(account store.WeixinAccount, userID, contextToken, text string) error {
	for _, chunk := range splitText(text, 3500) {
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			ctx, cancel := context.WithTimeout(manager.server.context, 20*time.Second)
			err = manager.gateway.SendText(ctx, gatewayAccount(account), userID, contextToken, chunk)
			cancel()
			if err == nil {
				break
			}
			if manager.server.context.Err() != nil || !waitContext(manager.server.context, time.Duration(attempt+1)*time.Second) {
				return err
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func finalSessionText(session store.Session) string {
	if session.Status == "failed" {
		return "任务失败：" + strings.TrimSpace(session.Error)
	}
	if session.Status == "canceled" {
		return "任务已停止。"
	}
	for index := len(session.Messages) - 1; index >= 0; index-- {
		if session.Messages[index].Role == "assistant" && strings.TrimSpace(session.Messages[index].Content) != "" {
			return strings.TrimSpace(session.Messages[index].Content)
		}
	}
	return ""
}

func splitText(value string, limit int) []string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) == 0 {
		return nil
	}
	result := []string{}
	for len(runes) > 0 {
		end := limit
		if len(runes) < end {
			end = len(runes)
		}
		result = append(result, string(runes[:end]))
		runes = runes[end:]
	}
	return result
}

func activeSessionStatus(status string) bool {
	return status == "queued" || status == "running" || status == "paused"
}
