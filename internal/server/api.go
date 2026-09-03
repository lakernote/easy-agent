package server

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/builtin/prompt"
	builtintools "github.com/lakernote/easy-agent/internal/builtin/tools"
	mcppresets "github.com/lakernote/easy-agent/internal/mcp/presets"
	"github.com/lakernote/easy-agent/internal/store"
)

type bootstrapPayload struct {
	Sessions             []sessionView         `json:"sessions"`
	SessionsHasMore      bool                  `json:"sessionsHasMore,omitempty"`
	Model                store.ModelSettings   `json:"model"`
	ModelProfiles        []store.ModelProfile  `json:"modelProfiles"`
	ActiveModelProfileID string                `json:"activeModelProfileId"`
	Skills               []store.SkillOverride `json:"skills"`
	BuiltinTools         []builtintools.Info   `json:"builtinTools"`
	MCPPresets           []mcppresets.Preset   `json:"mcpPresets"`
	ModelRules           modelRulesPayload     `json:"modelRules"`
	MCPs                 []store.MCPConfig     `json:"mcps"`
	SystemPrompt         string                `json:"systemPrompt"`
	Ollama               ollamaStatus          `json:"ollama"`
	Codex                codexRuntimeStatus    `json:"codex"`
	Runtime              runtimeInfoPayload    `json:"runtime"`
}

type runtimeInfoPayload struct {
	Home      string `json:"home"`
	Workspace string `json:"workspace"`
	Runtime   string `json:"runtime"`
}

type modelRulesPayload struct {
	DefaultMaxOutputTokens             int `json:"defaultMaxOutputTokens"`
	DefaultRequestTimeoutSeconds       int `json:"defaultRequestTimeoutSeconds"`
	MinRequestTimeoutSeconds           int `json:"minRequestTimeoutSeconds"`
	MaxRequestTimeoutSeconds           int `json:"maxRequestTimeoutSeconds"`
	DefaultCompressionThresholdPercent int `json:"defaultCompressionThresholdPercent"`
	MinCompressionThresholdPercent     int `json:"minCompressionThresholdPercent"`
	MaxCompressionThresholdPercent     int `json:"maxCompressionThresholdPercent"`
}

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

func (server *Server) bootstrap(response http.ResponseWriter, request *http.Request) {
	sessions, sessionsHasMore, err := server.store.ListSessionsBefore(100, "", "")
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	model, err := server.store.GetModelSettings()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	profiles, activeProfileID, err := server.store.ListModelProfiles()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	publicProfiles := make([]store.ModelProfile, 0, len(profiles))
	for _, profile := range profiles {
		profile.Settings = publicModel(profile.Settings)
		publicProfiles = append(publicProfiles, profile)
	}
	catalog, err := loadSkillCatalog(server.store)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	mcps, err := server.store.ListMCPConfigs()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	toolInfo := builtintools.InfoList(server.env, catalog)
	for _, config := range mcps {
		if config.Enabled {
			toolInfo = append(toolInfo, builtintools.Info{Name: "search_mcp_tools", Description: "按任务语义搜索已启用的 MCP Server，一次只加载少量相关工具。", Source: "运行时"})
			break
		}
	}
	detectedModel := enrichOllamaContextWindow(request.Context(), model)
	model = detectedModel
	writeJSON(response, http.StatusOK, bootstrapPayload{
		Sessions: publicSessions(sessions), SessionsHasMore: sessionsHasMore, Model: publicModel(model), ModelProfiles: publicProfiles, ActiveModelProfileID: activeProfileID, Skills: catalog.All(),
		BuiltinTools: toolInfo, MCPPresets: mcppresets.Catalog(), ModelRules: modelRules(),
		MCPs: publicMCPs(mcps), SystemPrompt: prompt.Template(), Ollama: server.detectOllama(request.Context()),
		Runtime: runtimeInfoPayload{Home: server.env.Home(), Workspace: server.env.Workspace(), Runtime: server.env.Runtime()},
		Codex:   server.detectCodex(request.Context()),
	})
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
	writeJSON(response, http.StatusOK, sessionListPage{Sessions: publicSessions(sessions), HasMore: hasMore})
}

func modelRules() modelRulesPayload {
	return modelRulesPayload{
		DefaultMaxOutputTokens:             store.DefaultMaxOutputTokens,
		DefaultRequestTimeoutSeconds:       store.DefaultRequestTimeoutSeconds,
		MinRequestTimeoutSeconds:           store.MinRequestTimeoutSeconds,
		MaxRequestTimeoutSeconds:           store.MaxRequestTimeoutSeconds,
		DefaultCompressionThresholdPercent: store.DefaultCompressionThresholdPercent,
		MinCompressionThresholdPercent:     store.MinCompressionThresholdPercent,
		MaxCompressionThresholdPercent:     store.MaxCompressionThresholdPercent,
	}
}

func (server *Server) getSession(response http.ResponseWriter, request *http.Request) {
	value, err := server.store.LoadSessionWindow(request.PathValue("id"), apiMessageWindow, apiEventWindow)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(response, http.StatusNotFound, "会话不存在")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	settings, _ := server.store.GetModelSettingsByProfileID(value.ProfileID)
	if settings.Runtime == "" {
		settings, _ = server.store.GetModelSettings()
	}
	settings = enrichOllamaContextWindow(request.Context(), settings)
	decorateContext(&value, settings)
	value.PartialOutput = server.taskPartial(value.ID)
	value.RunProgress = server.taskProgress(value.ID)
	if live := server.taskUsage(value.ID); live.ModelCalls > 0 || live.InputTokens > 0 || live.OutputTokens > 0 || live.TotalTokens > 0 {
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
	writeJSON(response, http.StatusOK, publicSession(value))
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

type messageRequest struct {
	Message     string              `json:"message"`
	Attachments []attachmentRequest `json:"attachments"`
	ProfileID   string              `json:"profileId,omitempty"`
	// Workspace 只在创建会话时使用；后续多轮对话始终沿用会话保存的工作区。
	Workspace string `json:"workspace,omitempty"`
}

func (server *Server) createSession(response http.ResponseWriter, request *http.Request) {
	var input messageRequest
	if !decodeJSON(response, request, &input) {
		return
	}
	input.Message = strings.TrimSpace(input.Message)
	attachments, err := validateAttachments(input.Attachments)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if input.Message == "" && len(attachments) == 0 {
		writeError(response, http.StatusBadRequest, "请输入消息或添加附件")
		return
	}
	runEnvironment, err := server.env.WithWorkspace(input.Workspace)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	model, err := server.store.GetModelSettingsByProfileID(input.ProfileID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	id := newID()
	if _, err := server.store.CreateSessionWithProfile(id, attachmentTitle(input.Message, attachments), model.Runtime, model.ProfileID, model.Model, runEnvironment.Workspace(), time.Now()); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := server.enqueueTurn(id, input.Message, attachments, model); err != nil {
		_ = server.store.DeleteSession(id)
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	value, _ := server.store.LoadSessionWindow(id, apiMessageWindow, apiEventWindow)
	value.RunProgress = server.taskProgress(id)
	model = enrichOllamaContextWindow(request.Context(), model)
	decorateContext(&value, model)
	writeJSON(response, http.StatusAccepted, publicSession(value))
}

func (server *Server) continueSession(response http.ResponseWriter, request *http.Request) {
	var input messageRequest
	if !decodeJSON(response, request, &input) {
		return
	}
	input.Message = strings.TrimSpace(input.Message)
	attachments, err := validateAttachments(input.Attachments)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if input.Message == "" && len(attachments) == 0 {
		writeError(response, http.StatusBadRequest, "请输入消息或添加附件")
		return
	}
	if strings.TrimSpace(input.Workspace) != "" {
		writeError(response, http.StatusBadRequest, "工作区在创建会话时确定；请新建会话后选择其他工作区")
		return
	}
	id := request.PathValue("id")
	// 这里只验证会话存在，避免继续对话前把完整历史加载两次。
	loaded, err := server.store.LoadSessionWindow(id, 1, 1)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, "会话不存在")
		} else {
			writeError(response, http.StatusInternalServerError, err.Error())
		}
		return
	}
	model, err := server.store.GetModelSettingsByProfileID(loaded.ProfileID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := server.enqueueTurn(id, input.Message, attachments, model); err != nil {
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	value, _ := server.store.LoadSessionWindow(id, apiMessageWindow, apiEventWindow)
	value.RunProgress = server.taskProgress(id)
	model = enrichOllamaContextWindow(request.Context(), model)
	decorateContext(&value, model)
	writeJSON(response, http.StatusAccepted, publicSession(value))
}

func (server *Server) deleteSession(response http.ResponseWriter, request *http.Request) {
	// 删除前只需要检查状态，不要为了一个状态字段把超长消息和 Trace 全量读入内存。
	value, err := server.store.LoadSessionWindow(request.PathValue("id"), 1, 1)
	if err == nil && (value.Status == "queued" || value.Status == "running") {
		writeError(response, http.StatusConflict, "Agent 正在运行，暂时不能删除")
		return
	}
	if err := server.store.DeleteSession(request.PathValue("id")); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (server *Server) cancelSession(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	changed, err := server.store.CancelSession(id, time.Now())
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if !changed {
		writeError(response, http.StatusConflict, "任务当前不在排队或运行中")
		return
	}
	server.cancelTask(id)
	value, err := server.store.LoadSessionWindow(id, apiMessageWindow, apiEventWindow)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	settings, _ := server.store.GetModelSettingsByProfileID(value.ProfileID)
	if settings.Runtime == "" {
		settings, _ = server.store.GetModelSettings()
	}
	settings = enrichOllamaContextWindow(request.Context(), settings)
	decorateContext(&value, settings)
	value.RunProgress = server.taskProgress(id)
	writeJSON(response, http.StatusOK, publicSession(value))
}
