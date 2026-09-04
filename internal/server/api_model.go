package server

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/lakernote/easy-agent/internal/store"
)

func (server *Server) saveModel(response http.ResponseWriter, request *http.Request) {
	var input store.ModelSettings
	if !decodeJSON(response, request, &input) {
		return
	}
	current, _ := server.store.GetModelSettings()
	input, err := prepareModelInput(input, current)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	input.SecretConfigured = false
	if err := server.store.SaveModelSettings(input); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, publicModel(enrichOllamaContextWindow(request.Context(), input)))
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

func publicModel(value store.ModelSettings) store.ModelSettings {
	value.SecretConfigured = value.APIKey != "" || (value.APIKeyEnv != "" && os.Getenv(value.APIKeyEnv) != "")
	value.APIKey = ""
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
	if value.Protocol != "chat_completions" && value.Protocol != "responses" {
		return errors.New("协议只能是 chat_completions 或 responses")
	}
	if value.MaxOutputTokens <= 0 {
		return errors.New("最大输出 Token 必须大于 0")
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
