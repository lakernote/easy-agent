package server

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/store"
	"github.com/lakernote/easy-agent/internal/weixin"
)

func (manager *weixinManager) poll(ctx context.Context, id string) {
	failures := 0
	for ctx.Err() == nil {
		settings, err := manager.server.store.GetWeixinSettings()
		account, accountErr := manager.server.store.GetWeixinAccount(id)
		if err != nil || accountErr != nil || !settings.Enabled || !account.Enabled {
			return
		}
		updates, err := manager.gateway.GetUpdates(ctx, gatewayAccount(account), account.SyncBuffer)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			failures++
			delay := 2 * time.Second
			if failures >= 5 {
				delay = 30 * time.Second
				failures = 0
			}
			if !waitContext(ctx, delay) {
				return
			}
			continue
		}
		failures = 0
		sort.SliceStable(updates.Messages, func(left, right int) bool { return updates.Messages[left].Sequence < updates.Messages[right].Sequence })
		lastSequence := account.LastSequence
		for _, message := range updates.Messages {
			if message.Sequence > lastSequence {
				lastSequence = message.Sequence
			}
			manager.handleMessage(account, settings, message)
			fresh, loadErr := manager.server.store.GetWeixinAccount(id)
			if loadErr == nil {
				account = fresh
			}
		}
		if updates.Buffer != "" || lastSequence != account.LastSequence {
			_ = manager.server.store.UpdateWeixinCursor(id, updates.Buffer, lastSequence, time.Now())
		}
	}
}

func (manager *weixinManager) handleMessage(account store.WeixinAccount, settings store.WeixinSettings, message weixin.Message) {
	freshSettings, settingsErr := manager.server.store.GetWeixinSettings()
	freshAccount, accountErr := manager.server.store.GetWeixinAccount(account.ID)
	if settingsErr != nil || accountErr != nil || !freshSettings.Enabled || !freshAccount.Enabled {
		return
	}
	settings = freshSettings
	account = freshAccount
	if message.MessageType != 1 || message.FromUserID != account.UserID || (message.Sequence > 0 && message.Sequence <= account.LastSequence) {
		return
	}
	messageID := message.MessageID
	if messageID == 0 {
		messageID = message.Sequence
	}
	if messageID == 0 {
		messageID = time.Now().UnixNano()
	}
	if messageID <= account.PendingMessageID || messageID <= account.DeliveredMessageID {
		return
	}
	createdAt := time.Time{}
	if message.CreatedAtMS > 0 {
		createdAt = time.UnixMilli(message.CreatedAtMS)
	}
	ignoreBefore := settings.IgnoreBefore
	if account.IgnoreBefore.After(ignoreBefore) {
		ignoreBefore = account.IgnoreBefore
	}
	if !ignoreBefore.IsZero() && (message.CreatedAtMS == 0 || createdAt.Before(ignoreBefore)) {
		return
	}
	text := messageText(message)
	if text == "" {
		manager.send(account, message.FromUserID, message.ContextToken, "当前仅支持文字任务。")
		return
	}
	if manager.handleCommand(account, message, text) {
		return
	}
	if err := manager.submit(account, message, text, messageID, createdAt); err != nil {
		manager.send(account, message.FromUserID, message.ContextToken, "任务未提交："+err.Error())
		return
	}
	manager.send(account, message.FromUserID, message.ContextToken, "任务已提交，完成后会在这里回复。")
}

func messageText(message weixin.Message) string {
	for _, item := range message.Items {
		if item.Type == 1 && item.TextItem != nil {
			return strings.TrimSpace(item.TextItem.Text)
		}
	}
	return ""
}
