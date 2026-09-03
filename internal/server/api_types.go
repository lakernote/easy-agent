package server

import (
	"time"

	"github.com/lakernote/easy-agent/internal/store"
)

// sessionView 是 HTTP 层的会话 DTO。store.Session 还包含 Compactions、ResponseID
// 和 ProviderKey 等 Runtime/持久化字段，不能让这些内部字段成为 API 契约的一部分。
type sessionView struct {
	ID                string            `json:"id"`
	Title             string            `json:"title"`
	Status            string            `json:"status"`
	Error             string            `json:"error,omitempty"`
	Runtime           string            `json:"runtime"`
	ProfileID         string            `json:"profileId,omitempty"`
	Model             string            `json:"model,omitempty"`
	Workspace         string            `json:"workspace"`
	CreatedAt         time.Time         `json:"createdAt"`
	UpdatedAt         time.Time         `json:"updatedAt"`
	Messages          []store.Message   `json:"messages"`
	Events            []store.Event     `json:"events"`
	MessageCount      int               `json:"messageCount,omitempty"`
	EventCount        int               `json:"eventCount,omitempty"`
	UserTurnCount     int               `json:"userTurnCount,omitempty"`
	MessagesTruncated bool              `json:"messagesTruncated,omitempty"`
	EventsTruncated   bool              `json:"eventsTruncated,omitempty"`
	MessagesHasMore   bool              `json:"messagesHasMore,omitempty"`
	EventsHasMore     bool              `json:"eventsHasMore,omitempty"`
	Usage             store.Usage       `json:"usage"`
	Context           store.ContextInfo `json:"context"`
	PartialOutput     string            `json:"partialOutput,omitempty"`
	RunProgress       string            `json:"runProgress,omitempty"`
}

func publicSession(value store.Session) sessionView {
	return sessionView{
		ID: value.ID, Title: value.Title, Status: value.Status, Error: value.Error,
		Runtime: value.Runtime, Model: value.Model, Workspace: value.Workspace, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		ProfileID: value.ProfileID,
		Messages:  value.Messages, Events: value.Events, MessageCount: value.MessageCount, EventCount: value.EventCount,
		UserTurnCount: value.UserTurnCount, MessagesTruncated: value.MessagesTruncated, EventsTruncated: value.EventsTruncated,
		MessagesHasMore: value.MessagesHasMore, EventsHasMore: value.EventsHasMore, Usage: value.Usage,
		Context: value.Context, PartialOutput: value.PartialOutput, RunProgress: value.RunProgress,
	}
}

func publicSessions(values []store.Session) []sessionView {
	result := make([]sessionView, 0, len(values))
	for _, value := range values {
		result = append(result, publicSession(value))
	}
	return result
}
