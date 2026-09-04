package server

import (
	"net/http"

	"github.com/lakernote/easy-agent/internal/builtin/prompt"
	builtintools "github.com/lakernote/easy-agent/internal/builtin/tools"
	"github.com/lakernote/easy-agent/internal/codexruntime"
	mcppresets "github.com/lakernote/easy-agent/internal/mcp/presets"
	"github.com/lakernote/easy-agent/internal/store"
)

type bootstrapPayload struct {
	Sessions             []sessionView               `json:"sessions"`
	SessionsHasMore      bool                        `json:"sessionsHasMore,omitempty"`
	Model                store.ModelSettings         `json:"model"`
	ModelProfiles        []store.ModelProfile        `json:"modelProfiles"`
	ActiveModelProfileID string                      `json:"activeModelProfileId"`
	Skills               []store.SkillOverride       `json:"skills"`
	BuiltinTools         []builtintools.Info         `json:"builtinTools"`
	MCPPresets           []mcppresets.Preset         `json:"mcpPresets"`
	ModelRules           modelRulesPayload           `json:"modelRules"`
	MCPs                 []store.MCPConfig           `json:"mcps"`
	SystemPrompt         string                      `json:"systemPrompt"`
	Ollama               ollamaStatus                `json:"ollama"`
	Codex                codexRuntimeStatus          `json:"codex"`
	CodexConfig          codexruntime.ProviderConfig `json:"codexConfig"`
	Runtime              runtimeInfoPayload          `json:"runtime"`
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
	DefaultCodexTurnTimeoutSeconds     int `json:"defaultCodexTurnTimeoutSeconds"`
	MinCodexTurnTimeoutSeconds         int `json:"minCodexTurnTimeoutSeconds"`
	MaxCodexTurnTimeoutSeconds         int `json:"maxCodexTurnTimeoutSeconds"`
	DefaultCompressionThresholdPercent int `json:"defaultCompressionThresholdPercent"`
	MinCompressionThresholdPercent     int `json:"minCompressionThresholdPercent"`
	MaxCompressionThresholdPercent     int `json:"maxCompressionThresholdPercent"`
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
		Sessions: server.sessionViews(sessions), SessionsHasMore: sessionsHasMore, Model: publicModel(model), ModelProfiles: publicProfiles, ActiveModelProfileID: activeProfileID, Skills: catalog.All(),
		BuiltinTools: toolInfo, MCPPresets: mcppresets.Catalog(), ModelRules: modelRules(),
		MCPs: publicMCPs(mcps), SystemPrompt: prompt.Template(), Ollama: server.detectOllama(request.Context()),
		Runtime:     runtimeInfoPayload{Home: server.env.Home(), Workspace: server.env.Workspace(), Runtime: server.env.Runtime()},
		Codex:       server.detectCodex(request.Context()),
		CodexConfig: server.loadCodexConfig(),
	})
}

func (server *Server) loadCodexConfig() codexruntime.ProviderConfig {
	config, err := codexruntime.LoadProviderConfig()
	if err != nil {
		return codexruntime.ProviderConfig{}
	}
	return config
}
