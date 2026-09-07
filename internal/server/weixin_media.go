package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/lakernote/easy-agent/internal/store"
	"github.com/lakernote/easy-agent/internal/weixin"
)

var errWeixinVoiceNeedsText = errors.New("微信语音没有取得可用文字")

func (manager *weixinManager) decodeWeixinMessage(ctx context.Context, account store.WeixinAccount, message weixin.Message) (string, []store.Attachment, error) {
	text := messageText(message)
	attachments := make([]store.Attachment, 0, len(message.Items))
	total := 0
	hasUntranscribedVoice := false
	for _, item := range message.Items {
		switch item.Type {
		case 1:
			continue
		case 2:
			if item.ImageItem == nil || item.ImageItem.Media == nil {
				continue
			}
			key := item.ImageItem.Media.AESKey
			if strings.TrimSpace(item.ImageItem.AESKey) != "" {
				key = item.ImageItem.AESKey
			}
			data, err := manager.gateway.DownloadMedia(ctx, *item.ImageItem.Media, key)
			if err != nil {
				return "", nil, fmt.Errorf("下载图片: %w", err)
			}
			name := weixinImageName(message.MessageID, data)
			attachment, err := downloadedWeixinAttachment(name, "", data)
			if err != nil {
				return "", nil, err
			}
			total += len(data)
			attachments = append(attachments, attachment)
		case 3:
			if item.VoiceItem == nil {
				continue
			}
			// ClawBot normally includes WeChat's own transcript in VoiceItem.Text.
			// Use that text directly and avoid downloading or decoding SILK audio.
			if strings.TrimSpace(item.VoiceItem.Text) == "" {
				hasUntranscribedVoice = true
			}
		case 4:
			if item.FileItem == nil || item.FileItem.Media == nil {
				continue
			}
			data, err := manager.gateway.DownloadMedia(ctx, *item.FileItem.Media, item.FileItem.Media.AESKey)
			if err != nil {
				return "", nil, fmt.Errorf("下载文件: %w", err)
			}
			attachment, err := downloadedWeixinAttachment(item.FileItem.FileName, "", data)
			if err != nil {
				return "", nil, err
			}
			total += len(data)
			attachments = append(attachments, attachment)
		case 5:
			return "", nil, errors.New("微信视频尚未接入；请改发文字、图片或文件")
		}
		if len(attachments) > maxAttachmentCount {
			return "", nil, fmt.Errorf("一条微信消息最多处理 %d 个附件", maxAttachmentCount)
		}
		if total > maxAttachmentTotalBytes {
			return "", nil, errors.New("微信附件总大小超过 10 MiB")
		}
	}
	if text == "" && len(attachments) == 0 && hasUntranscribedVoice {
		return "", nil, errWeixinVoiceNeedsText
	}
	return text, attachments, nil
}

func downloadedWeixinAttachment(name, declared string, data []byte) (store.Attachment, error) {
	name = strings.TrimSpace(filepath.Base(name))
	if name == "" || name == "." {
		name = "weixin-file"
	}
	if len(data) == 0 {
		return store.Attachment{}, fmt.Errorf("微信附件 %s 为空", name)
	}
	if len(data) > maxAttachmentBytes {
		return store.Attachment{}, fmt.Errorf("微信附件 %s 超过 5 MiB", name)
	}
	mimeType, kind, err := classifyAttachment(name, declared, data)
	if err != nil {
		return store.Attachment{}, err
	}
	return store.Attachment{ID: newID(), Name: name, MIMEType: mimeType, Kind: kind, Size: int64(len(data)), Data: data}, nil
}

func weixinImageName(messageID int64, data []byte) string {
	extension := ".img"
	switch strings.Split(http.DetectContentType(data), ";")[0] {
	case "image/png":
		extension = ".png"
	case "image/jpeg":
		extension = ".jpg"
	case "image/gif":
		extension = ".gif"
	case "image/webp":
		extension = ".webp"
	}
	return fmt.Sprintf("weixin-image-%d%s", messageID, extension)
}

func joinWeixinText(left, right string) string {
	if strings.TrimSpace(left) == "" {
		return strings.TrimSpace(right)
	}
	return strings.TrimSpace(left) + "\n" + strings.TrimSpace(right)
}

func defaultWeixinMediaPrompt(attachments []store.Attachment) string {
	for _, attachment := range attachments {
		switch attachment.Kind {
		case "image":
			return "请分析微信发送的图片。"
		}
	}
	return "请分析微信发送的文件。"
}
