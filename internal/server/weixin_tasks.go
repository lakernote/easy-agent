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
	command := weixinCommand(text)
	switch command {
	case "help":
		manager.send(account, message.FromUserID, message.ContextToken, "直接发送文字即可创建或继续任务。\n“新会话” 开始独立任务\n“状态” 查看当前进度\n“停止” 中断当前任务\n\n也支持 /new、/status、/stop。")
		return true
	case "new":
		if session, err := manager.currentSession(account); err == nil && activeSessionStatus(session.Status) {
			manager.send(account, message.FromUserID, message.ContextToken, "当前任务仍在执行。请先发送“停止”，任务结束后再开始新会话。")
			return true
		}
		_ = manager.server.store.SetWeixinCurrentSession(account.ID, "", time.Now())
		manager.send(account, message.FromUserID, message.ContextToken, "已切换到新会话。下一条消息会创建一个独立任务。")
		return true
	case "status":
		session, err := manager.currentSession(account)
		if errors.Is(err, sql.ErrNoRows) || strings.TrimSpace(account.CurrentSessionID) == "" {
			manager.send(account, message.FromUserID, message.ContextToken, "当前没有会话。直接发送任务即可开始。")
		} else if err != nil {
			manager.send(account, message.FromUserID, message.ContextToken, "暂时无法读取任务状态，请稍后重试。")
		} else {
			progress := manager.server.tasks.progress(session.ID)
			if progress == "" {
				progress = weixinSessionStatus(session.Status)
			}
			manager.send(account, message.FromUserID, message.ContextToken, fmt.Sprintf("当前会话：%s\n状态：%s\n进度：%s\n运行时：%s", session.Title, weixinSessionStatus(session.Status), progress, weixinRuntimeLabel(session.Runtime)))
		}
		return true
	case "stop":
		session, err := manager.currentSession(account)
		if err != nil || !activeSessionStatus(session.Status) {
			manager.send(account, message.FromUserID, message.ContextToken, "当前没有正在执行的任务。发送“新会话”可开始独立任务。")
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

func (manager *weixinManager) submit(account store.WeixinAccount, message weixin.Message, text string, messageID int64, createdAt time.Time) (string, error) {
	if current, err := manager.currentSession(account); err == nil && activeSessionStatus(current.Status) {
		return "", errors.New("上一条任务仍在执行，可发送“状态”查看或发送“停止”中断")
	}
	now := time.Now()
	sessionID := account.CurrentSessionID
	var model store.ModelSettings
	if sessionID != "" {
		current, err := manager.server.store.LoadSessionWindow(sessionID, 1, 1)
		if err == nil {
			model, err = manager.server.store.GetModelSettingsByProfileID(current.ProfileID)
			if err != nil {
				return "", err
			}
			if err := manager.server.enqueueTurn(sessionID, text, nil, model); err != nil {
				return "", err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		} else {
			sessionID = ""
		}
	}
	if sessionID == "" {
		var err error
		model, err = manager.server.store.GetModelSettings()
		if err != nil {
			return "", err
		}
		runEnvironment, err := manager.server.env.WithWorkspace("")
		if err != nil {
			return "", err
		}
		sessionID = newID()
		runtimeSettings, _ := manager.server.store.GetRuntimeSettings()
		workspace := manager.server.prepareSessionWorkspace(manager.server.context, sessionID, runEnvironment.Workspace(), runtimeSettings)
		if _, err := manager.server.store.CreateSessionWithProfile(sessionID, makeTitle(text), model.Runtime, model.ProfileID, model.Model, workspace.Execution, now); err != nil {
			return "", err
		}
		if err := manager.server.store.SetSessionWorkspace(sessionID, workspace.Execution, workspace.Source, workspace.Branch, workspace.Notice); err != nil {
			_ = manager.server.store.DeleteSession(sessionID)
			return "", err
		}
		if err := manager.server.enqueueTurn(sessionID, text, nil, model); err != nil {
			_ = manager.server.store.DeleteSession(sessionID)
			return "", err
		}
	}
	if createdAt.IsZero() {
		createdAt = now
	}
	if err := manager.server.store.SetWeixinTask(account.ID, sessionID, messageID, message.ContextToken, createdAt, now); err != nil {
		return "", err
	}
	return sessionID, nil
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
		return fmt.Sprintf("任务失败 · %s\n\n%s\n\n发送“状态”查看会话，或发送“新会话”开始独立任务。", session.Title, strings.TrimSpace(session.Error))
	}
	if session.Status == "canceled" {
		return fmt.Sprintf("任务已停止 · %s\n\n可以继续发送消息，或发送“新会话”开始独立任务。", session.Title)
	}
	for index := len(session.Messages) - 1; index >= 0; index-- {
		if session.Messages[index].Role == "assistant" && strings.TrimSpace(session.Messages[index].Content) != "" {
			return fmt.Sprintf("任务完成 · %s\n\n%s\n\n继续发送消息可在本会话追问；发送“新会话”开始独立任务。", session.Title, strings.TrimSpace(session.Messages[index].Content))
		}
	}
	return ""
}

func weixinSessionStatus(status string) string {
	switch status {
	case "queued":
		return "排队中"
	case "running":
		return "运行中"
	case "paused":
		return "已暂停"
	case "failed":
		return "失败"
	case "canceled":
		return "已停止"
	default:
		return "已完成"
	}
}

func weixinRuntimeLabel(runtime string) string {
	if runtime == "codex" {
		return "Codex Runtime"
	}
	return "EasyAgent Runtime"
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
