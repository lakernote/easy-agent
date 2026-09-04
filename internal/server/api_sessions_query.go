package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/store"
)

const (
	apiMessageWindow = 200
	apiEventWindow   = 300
)

type sessionHistoryPage struct {
	Messages        []store.Message `json:"messages,omitempty"`
	Events          []store.Event   `json:"events,omitempty"`
	MessageCount    int             `json:"messageCount,omitempty"`
	EventCount      int             `json:"eventCount,omitempty"`
	MessagesHasMore bool            `json:"messagesHasMore,omitempty"`
	EventsHasMore   bool            `json:"eventsHasMore,omitempty"`
}

type sessionListPage struct {
	Sessions []sessionView `json:"sessions"`
	HasMore  bool          `json:"hasMore"`
}

func (server *Server) getOlderSessions(response http.ResponseWriter, request *http.Request) {
	beforeUpdatedAt := strings.TrimSpace(request.URL.Query().Get("beforeUpdatedAt"))
	beforeID := strings.TrimSpace(request.URL.Query().Get("beforeID"))
	if beforeUpdatedAt == "" || beforeID == "" {
		writeError(response, http.StatusBadRequest, "会话游标不完整")
		return
	}
	if _, err := time.Parse(time.RFC3339Nano, beforeUpdatedAt); err != nil {
		writeError(response, http.StatusBadRequest, "会话时间游标无效")
		return
	}
	sessions, hasMore, err := server.store.ListSessionsBefore(100, beforeUpdatedAt, beforeID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, sessionListPage{Sessions: server.sessionViews(sessions), HasMore: hasMore})
}

func modelRules() modelRulesPayload {
	return modelRulesPayload{
		DefaultMaxOutputTokens:             store.DefaultMaxOutputTokens,
		DefaultRequestTimeoutSeconds:       store.DefaultRequestTimeoutSeconds,
		MinRequestTimeoutSeconds:           store.MinRequestTimeoutSeconds,
		MaxRequestTimeoutSeconds:           store.MaxRequestTimeoutSeconds,
		DefaultCodexTurnTimeoutSeconds:     store.DefaultCodexTurnTimeoutSeconds,
		MinCodexTurnTimeoutSeconds:         store.MinCodexTurnTimeoutSeconds,
		MaxCodexTurnTimeoutSeconds:         store.MaxCodexTurnTimeoutSeconds,
		DefaultCompressionThresholdPercent: store.DefaultCompressionThresholdPercent,
		MinCompressionThresholdPercent:     store.MinCompressionThresholdPercent,
		MaxCompressionThresholdPercent:     store.MaxCompressionThresholdPercent,
	}
}

func (server *Server) getSession(response http.ResponseWriter, request *http.Request) {
	value, err := server.loadLiveSession(request.Context(), request.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(response, http.StatusNotFound, "会话不存在")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, server.sessionView(value))
}

func (server *Server) loadLiveSession(ctx context.Context, id string) (store.Session, error) {
	value, err := server.store.LoadSessionWindow(id, apiMessageWindow, apiEventWindow)
	if err != nil {
		return store.Session{}, err
	}
	settings, _ := server.store.GetModelSettingsByProfileID(value.ProfileID)
	if settings.Runtime == "" {
		settings, _ = server.store.GetModelSettings()
	}
	settings = enrichOllamaContextWindow(ctx, settings)
	decorateContext(&value, settings)
	value.PartialOutput = server.tasks.partial(value.ID)
	value.RunProgress = server.tasks.progress(value.ID)
	if live := server.tasks.usage(value.ID); live.ModelCalls > 0 || live.InputTokens > 0 || live.OutputTokens > 0 || live.TotalTokens > 0 {
		value.Usage.InputTokens += live.InputTokens
		value.Usage.OutputTokens += live.OutputTokens
		value.Usage.CachedTokens += live.CachedTokens
		value.Usage.CacheWriteTokens += live.CacheWriteTokens
		value.Usage.TotalTokens += live.TotalTokens
		value.Usage.ModelCalls += live.ModelCalls
		value.Usage.CacheReported = value.Usage.CacheReported || live.CacheReported
		value.Usage.CacheInputTokens += live.InputTokens
		if live.ContextWindowTokens > 0 {
			value.Usage.ContextWindowTokens = live.ContextWindowTokens
			value.Context.ContextWindowTokens = live.ContextWindowTokens
		}
	}
	return value, nil
}

// getSessionHistory 是页面的 keyset pagination 接口。kind 决定只读取消息或
// Trace，避免为了滚动其中一类历史而重新查询、传输另一类历史。
func (server *Server) getSessionHistory(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if _, err := server.store.LoadSessionWindow(id, 1, 1); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, "会话不存在")
		} else {
			writeError(response, http.StatusInternalServerError, err.Error())
		}
		return
	}
	kind := request.URL.Query().Get("kind")
	before, err := strconv.ParseInt(strings.TrimSpace(request.URL.Query().Get("before")), 10, 64)
	if err != nil || before <= 0 {
		writeError(response, http.StatusBadRequest, "历史游标必须是正整数")
		return
	}
	page := sessionHistoryPage{}
	switch kind {
	case "messages":
		page.Messages, page.MessageCount, page.MessagesHasMore, err = server.store.ListMessagesBefore(id, before, apiMessageWindow)
	case "events":
		page.Events, page.EventCount, page.EventsHasMore, err = server.store.ListEventsBefore(id, before, apiEventWindow)
	default:
		writeError(response, http.StatusBadRequest, "历史类型必须是 messages 或 events")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, page)
}
