package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/store"
)

type ollamaModel struct {
	Name       string    `json:"name"`
	Model      string    `json:"model,omitempty"`
	Size       int64     `json:"size,omitempty"`
	ModifiedAt time.Time `json:"modifiedAt,omitempty"`
}

type ollamaStatus struct {
	Installed bool          `json:"installed"`
	Running   bool          `json:"running"`
	BaseURL   string        `json:"baseUrl"`
	Models    []ollamaModel `json:"models"`
	Message   string        `json:"message"`
}

func (server *Server) getOllama(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, server.detectOllama(request.Context()))
}

func (server *Server) detectOllama(parent context.Context) ollamaStatus {
	_, lookupErr := server.env.ResolveCommand("ollama")
	status := ollamaStatus{Installed: lookupErr == nil, BaseURL: ollamaServerURL(), Models: []ollamaModel{}}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, status.BaseURL+"/api/tags", nil)
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		status.Message = "Ollama 未启动；可执行 ollama serve"
		return status
	}
	defer response.Body.Close()
	var payload struct {
		Models []ollamaModel `json:"models"`
	}
	if response.StatusCode >= 300 || json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(&payload) != nil {
		status.Message = "Ollama 服务响应异常"
		return status
	}
	status.Running, status.Models = true, payload.Models
	if len(payload.Models) == 0 {
		status.Message = "Ollama 已运行，但尚未下载模型"
	} else {
		status.Message = "Ollama 已运行，可直接使用"
	}
	return status
}

// ollamaServerURL 允许服务器通过环境变量连接另一个 Ollama 实例。
// 默认仍是本机，不要求用户增加配置；路径统一在这里处理，避免 API 各处
// 写死 127.0.0.1。
func ollamaServerURL() string {
	value := strings.TrimSpace(os.Getenv("EASYAGENT_OLLAMA_URL"))
	if value == "" {
		value = strings.TrimSuffix(store.DefaultOllamaBaseURL, "/v1")
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	return strings.TrimRight(value, "/")
}

// enrichOllamaContextWindow 从 /api/ps 读取当前真正加载的上下文窗口。
// /api/show 的 context_length 是模型理论上限，不一定等于 Ollama 根据显存实际
// 选择的窗口；用理论值做压缩阈值会在小窗口机器上过晚压缩。
func enrichOllamaContextWindow(parent context.Context, value store.ModelSettings) store.ModelSettings {
	if !value.IsOllama() || strings.TrimSpace(value.Model) == "" {
		return value
	}
	baseURL := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(value.BaseURL), "/"), "/v1")
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/ps", nil)
	if err != nil {
		return value
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		return value
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return value
	}
	var payload struct {
		Models []struct {
			Name          string `json:"name"`
			Model         string `json:"model"`
			ContextLength int    `json:"context_length"`
		} `json:"models"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(&payload) != nil {
		return value
	}
	for _, running := range payload.Models {
		if (running.Name == value.Model || running.Model == value.Model) && running.ContextLength > 0 {
			value.ContextWindowTokens = running.ContextLength
			return value
		}
	}
	return value
}

func (server *Server) useOllama(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Model string `json:"model"`
	}
	if !decodeJSON(response, request, &input) {
		return
	}
	status := server.detectOllama(request.Context())
	if !status.Running {
		writeError(response, http.StatusBadGateway, status.Message)
		return
	}
	found := false
	for _, value := range status.Models {
		if input.Model == value.Name || input.Model == value.Model {
			found = true
		}
	}
	if !found {
		writeError(response, http.StatusBadRequest, "模型尚未下载")
		return
	}
	model := store.DefaultModelSettings()
	model.BaseURL = strings.TrimRight(status.BaseURL, "/") + "/v1"
	model.Model = input.Model
	if err := server.store.SaveModelSettings(model); err != nil {
		writeError(response, 500, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, publicModel(enrichOllamaContextWindow(request.Context(), model)))
}
