package server

import (
	"context"
	"fmt"
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
			manager.handleMessage(ctx, account, settings, message)
			fresh, loadErr := manager.server.store.GetWeixinAccount(id)
			if loadErr == nil {
				account = fresh
			}
		}
		nextBuffer := account.SyncBuffer
		if updates.Buffer != "" {
			nextBuffer = updates.Buffer
		}
		// 每次成功拉取都刷新在线时间。否则没有新消息时，页面会把一个健康的
		// 长轮询连接显示成“很久没有活动”，让管理员误以为通道已经掉线。
		_ = manager.server.store.UpdateWeixinCursor(id, nextBuffer, lastSequence, time.Now())
	}
}

func (manager *weixinManager) handleMessage(ctx context.Context, account store.WeixinAccount, settings store.WeixinSettings, message weixin.Message) {
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
	message.MessageID = messageID
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
	text, attachments, err := manager.decodeWeixinMessage(ctx, account, message)
	if err != nil {
		manager.send(account, message.FromUserID, message.ContextToken, "消息处理失败："+err.Error())
		return
	}
	if text == "" && len(attachments) == 0 {
		manager.send(account, message.FromUserID, message.ContextToken, "暂时无法识别这条消息；当前支持文字、语音、图片、PDF 和文本/代码文件。")
		return
	}
	if text == "" && onlyAudioAttachments(attachments) {
		sessionID, saveErr := manager.saveUntranscribedVoice(account, attachments, createdAt)
		if saveErr != nil {
			manager.send(account, message.FromUserID, message.ContextToken, "语音保存失败："+saveErr.Error())
			return
		}
		_ = manager.server.store.RecordWeixinMessage(account.ID, createdAt, time.Now())
		sessionTitle := "未转写的微信语音"
		if session, loadErr := manager.server.store.LoadSessionWindow(sessionID, 1, 1); loadErr == nil {
			sessionTitle = session.Title
		}
		manager.send(account, message.FromUserID, message.ContextToken, "这条语音没有取得微信文字，已保存到 Web 会话，但没有启动 Agent。\n请补发文字说明后重试。\n会话："+sessionTitle)
		return
	}
	if manager.handleCommand(account, message, text) {
		return
	}
	if text == "" {
		text = defaultWeixinMediaPrompt(attachments)
	}
	sessionID, err := manager.submit(account, message, text, attachments, messageID, createdAt)
	if err != nil {
		manager.send(account, message.FromUserID, message.ContextToken, "任务未提交："+err.Error())
		return
	}
	acknowledgement := "任务已接收，完成后会在这里回复。\n发送“状态”查看进度，发送“停止”中断任务。"
	if session, loadErr := manager.server.store.LoadSessionWindow(sessionID, 1, 1); loadErr == nil {
		acknowledgement = fmt.Sprintf("任务已接收\n会话：%s\n状态：%s\n\n发送“状态”查看进度，发送“停止”中断任务。", session.Title, weixinSessionStatus(session.Status))
	}
	manager.send(account, message.FromUserID, message.ContextToken, acknowledgement)
	// 先发送接收确认，再启动结果等待，避免极短任务的最终结果先于确认消息到达。
	manager.resumeDelivery(account.ID)
}

func messageText(message weixin.Message) string {
	values := make([]string, 0, len(message.Items))
	for _, item := range message.Items {
		if item.Type == 1 && item.TextItem != nil {
			if value := strings.TrimSpace(item.TextItem.Text); value != "" {
				values = append(values, value)
			}
		}
		if item.Type == 3 && item.VoiceItem != nil {
			if value := strings.TrimSpace(item.VoiceItem.Text); value != "" {
				values = append(values, value)
			}
		}
	}
	return strings.Join(values, "\n")
}
