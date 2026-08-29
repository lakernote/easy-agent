package server

import (
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/lakernote/easy-agent/internal/store"
)

const (
	maxAttachmentCount      = 5
	maxAttachmentBytes      = 5 * 1024 * 1024
	maxAttachmentTotalBytes = 10 * 1024 * 1024
)

type attachmentRequest struct {
	Name     string `json:"name"`
	MIMEType string `json:"mimeType"`
	Size     int64  `json:"size"`
	Data     string `json:"data"`
}

func validateAttachments(input []attachmentRequest) ([]store.Attachment, error) {
	if len(input) > maxAttachmentCount {
		return nil, fmt.Errorf("每条消息最多添加 %d 个附件", maxAttachmentCount)
	}
	result := make([]store.Attachment, 0, len(input))
	total := 0
	for _, item := range input {
		name := strings.TrimSpace(filepath.Base(item.Name))
		if name == "" || name == "." || utf8.RuneCountInString(name) > 180 {
			return nil, errors.New("附件文件名无效或过长")
		}
		encoded := strings.TrimSpace(item.Data)
		if index := strings.Index(encoded, ","); strings.HasPrefix(encoded, "data:") && index >= 0 {
			encoded = encoded[index+1:]
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("附件 %s 不是有效的 Base64 数据", name)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("附件 %s 为空", name)
		}
		if len(data) > maxAttachmentBytes {
			return nil, fmt.Errorf("附件 %s 超过 5 MiB", name)
		}
		total += len(data)
		if total > maxAttachmentTotalBytes {
			return nil, errors.New("本条消息的附件总大小超过 10 MiB")
		}
		mimeType, kind, err := classifyAttachment(name, item.MIMEType, data)
		if err != nil {
			return nil, err
		}
		result = append(result, store.Attachment{
			ID: newID(), Name: name, MIMEType: mimeType, Kind: kind, Size: int64(len(data)), Data: data,
		})
	}
	return result, nil
}

func classifyAttachment(name, declared string, data []byte) (string, string, error) {
	declared = strings.ToLower(strings.TrimSpace(strings.Split(declared, ";")[0]))
	extension := strings.ToLower(filepath.Ext(name))
	detected := strings.ToLower(strings.Split(http.DetectContentType(data), ";")[0])
	imageTypes := map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}
	if imageTypes[declared] && !imageTypes[detected] {
		return "", "", fmt.Errorf("附件 %s 不是有效的图片", name)
	}
	if imageTypes[detected] {
		return detected, "image", nil
	}
	if declared == "application/pdf" || detected == "application/pdf" || extension == ".pdf" {
		if !strings.HasPrefix(string(data), "%PDF-") {
			return "", "", fmt.Errorf("附件 %s 不是有效的 PDF", name)
		}
		return "application/pdf", "pdf", nil
	}
	textExtensions := map[string]bool{
		".txt": true, ".md": true, ".log": true, ".csv": true, ".json": true, ".xml": true,
		".yaml": true, ".yml": true, ".go": true, ".java": true, ".py": true, ".js": true,
		".ts": true, ".tsx": true, ".jsx": true, ".css": true, ".html": true, ".sh": true,
		".sql": true, ".properties": true, ".toml": true, ".ini": true, ".conf": true,
	}
	textMIMEs := map[string]bool{
		"application/json": true, "application/xml": true, "application/javascript": true,
		"application/x-javascript": true, "application/yaml": true, "application/x-yaml": true,
	}
	if strings.HasPrefix(declared, "text/") || textMIMEs[declared] || textExtensions[extension] || detected == "text/plain" {
		if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
			return "", "", fmt.Errorf("附件 %s 不是有效的 UTF-8 文本", name)
		}
		mimeType := declared
		if mimeType == "" || mimeType == "application/octet-stream" {
			mimeType = mime.TypeByExtension(extension)
		}
		if mimeType == "" {
			mimeType = "text/plain"
		}
		return strings.Split(mimeType, ";")[0], "text", nil
	}
	return "", "", fmt.Errorf("附件 %s 的格式暂不支持；当前支持图片、UTF-8 文本/代码和 PDF", name)
}

func attachmentTitle(message string, attachments []store.Attachment) string {
	if strings.TrimSpace(message) != "" {
		return makeTitle(message)
	}
	if len(attachments) == 1 {
		return makeTitle("分析附件 " + attachments[0].Name)
	}
	return fmt.Sprintf("分析 %d 个附件", len(attachments))
}

func (server *Server) getAttachment(response http.ResponseWriter, request *http.Request) {
	value, err := server.store.Attachment(request.PathValue("id"))
	if err != nil {
		writeError(response, http.StatusNotFound, "附件不存在")
		return
	}
	response.Header().Set("Content-Type", value.MIMEType)
	response.Header().Set("Content-Length", fmt.Sprint(len(value.Data)))
	response.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": value.Name}))
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(value.Data)
}
