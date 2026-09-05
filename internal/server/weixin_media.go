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

func (manager *weixinManager) decodeWeixinMessage(ctx context.Context, account store.WeixinAccount, message weixin.Message) (string, []store.Attachment, error) {
	text := messageText(message)
	attachments := make([]store.Attachment, 0, len(message.Items))
	total := 0
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
			hasNativeText := strings.TrimSpace(item.VoiceItem.Text) != ""
			if item.VoiceItem.Media == nil {
				if hasNativeText {
					continue
				}
				return "", nil, errors.New("微信语音没有文字或可下载的音频")
			}
			encrypted, err := manager.gateway.DownloadMedia(ctx, *item.VoiceItem.Media, item.VoiceItem.Media.AESKey)
			if err != nil {
				if hasNativeText {
					continue
				}
				return "", nil, fmt.Errorf("下载语音: %w", err)
			}
			audio, mimeType, name, err := weixin.DecodeVoice(encrypted, item.VoiceItem.SampleRate)
			if err != nil {
				if hasNativeText {
					continue
				}
				return "", nil, err
			}
			if len(audio) > maxAttachmentBytes {
				return "", nil, errors.New("解码后的微信语音超过 5 MiB")
			}
			total += len(audio)
			attachments = append(attachments, store.Attachment{ID: newID(), Name: name, MIMEType: mimeType, Kind: "audio", Size: int64(len(audio)), Data: audio})
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
			return "", nil, errors.New("微信视频尚未接入；请改发图片、语音或文件")
		}
		if len(attachments) > maxAttachmentCount {
			return "", nil, fmt.Errorf("一条微信消息最多处理 %d 个附件", maxAttachmentCount)
		}
		if total > maxAttachmentTotalBytes {
			return "", nil, errors.New("微信附件总大小超过 10 MiB")
		}
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

func onlyAudioAttachments(attachments []store.Attachment) bool {
	if len(attachments) == 0 {
		return false
	}
	for _, attachment := range attachments {
		if attachment.Kind != "audio" {
			return false
		}
	}
	return true
}
