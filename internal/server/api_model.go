package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/lakernote/easy-agent/internal/agent/modeladapter"
	"github.com/lakernote/easy-agent/internal/store"
)

type modelSettingsInput struct {
	store.ModelSettings
	ClearAPIKey bool `json:"clearApiKey,omitempty"`
}

func (server *Server) saveModel(response http.ResponseWriter, request *http.Request) {
	var payload modelSettingsInput
	if !decodeJSON(response, request, &payload) {
		return
	}
	current := server.modelSettingsForInput(payload.ModelSettings)
	input, err := prepareModelInput(payload.ModelSettings, current, payload.ClearAPIKey)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	input.SecretConfigured = false
	if err := server.store.SaveModelProfile(input); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, publicModel(enrichOllamaContextWindow(request.Context(), input)))
}

func (server *Server) activateModelProfile(response http.ResponseWriter, request *http.Request) {
	id := strings.TrimSpace(request.PathValue("id"))
	settings, err := server.store.GetModelSettingsByProfileID(id)
	if err == nil {
		err = server.requireVerifiedEasyAgent(settings)
	}
	if err != nil {
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	value, err := server.store.SetActiveModelProfile(id)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, publicModel(enrichOllamaContextWindow(request.Context(), value)))
}

func (server *Server) deleteModelProfile(response http.ResponseWriter, request *http.Request) {
	if err := server.store.DeleteModelProfile(request.PathValue("id")); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func sameModelEndpoint(left, right store.ModelSettings) bool {
	return strings.EqualFold(strings.TrimSpace(left.Provider), strings.TrimSpace(right.Provider)) &&
		strings.EqualFold(strings.TrimRight(strings.TrimSpace(left.BaseURL), "/"), strings.TrimRight(strings.TrimSpace(right.BaseURL), "/"))
}

func (server *Server) modelSettingsForInput(input store.ModelSettings) store.ModelSettings {
	if strings.TrimSpace(input.ProfileID) != "" {
		value, err := server.store.GetModelSettingsByProfileID(input.ProfileID)
		if err == nil {
			return value
		}
		return store.ModelSettings{}
	}
	value, _ := server.store.GetModelSettings()
	return value
}

func publicModel(value store.ModelSettings) store.ModelSettings {
	// EasyAgent 只公开 DB 密钥是否已配置，不再把环境变量入口带回浏览器。
	value.SecretConfigured = value.APIKey != ""
	value.APIKey = ""
	value.APIKeyEnv = ""
	return value
}

func validateModel(value store.ModelSettings) error {
	if value.Runtime == store.RuntimeCodex {
		if value.MaxOutputTokens <= 0 {
			return errors.New("最大输出 Token 必须大于 0")
		}
		if value.TurnTimeoutSeconds < store.MinCodexTurnTimeoutSeconds || value.TurnTimeoutSeconds > store.MaxCodexTurnTimeoutSeconds {
			return errors.New("Codex 整轮任务上限必须在 " + strconv.Itoa(store.MinCodexTurnTimeoutSeconds) + " 到 " + strconv.Itoa(store.MaxCodexTurnTimeoutSeconds) + " 秒之间")
		}
		return nil
	}
	if strings.TrimSpace(value.BaseURL) == "" || strings.TrimSpace(value.Model) == "" {
		return errors.New("模型地址和名称不能为空")
	}
	if !modeladapter.Supports(value.Protocol) {
		return errors.New("不支持的模型协议；可用：" + strings.Join(modeladapter.Protocols(), "、"))
	}
	if value.MaxOutputTokens <= 0 {
		return errors.New("最大输出 Token 必须大于 0")
	}
	if value.MaxSteps < store.MinMaxSteps || value.MaxSteps > store.MaxMaxSteps {
		return errors.New("Agent 最大步骤数必须在 " + strconv.Itoa(store.MinMaxSteps) + " 到 " + strconv.Itoa(store.MaxMaxSteps) + " 之间")
	}
	if value.RequestTimeoutSeconds < store.MinRequestTimeoutSeconds || value.RequestTimeoutSeconds > store.MaxRequestTimeoutSeconds {
		return errors.New("模型超时必须在 " + strconv.Itoa(store.MinRequestTimeoutSeconds) + " 到 " + strconv.Itoa(store.MaxRequestTimeoutSeconds) + " 秒之间")
	}
	if value.ContextWindowTokens < 0 {
		return errors.New("上下文窗口 Token 不能小于 0")
	}
	if value.ContextWindowTokens > 0 && value.ContextWindowTokens <= value.MaxOutputTokens {
		return errors.New("上下文窗口必须大于最大输出 Token")
	}
	if value.CompressionThresholdPercent < store.MinCompressionThresholdPercent || value.CompressionThresholdPercent > store.MaxCompressionThresholdPercent {
		return errors.New("自动压缩阈值必须在 " + strconv.Itoa(store.MinCompressionThresholdPercent) + "% 到 " + strconv.Itoa(store.MaxCompressionThresholdPercent) + "% 之间")
	}
	return nil
}
