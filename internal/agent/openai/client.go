// Package openai 提供 OpenAI Chat Completions 与 Responses 两种协议适配器。
// 兼容服务只要实现其中一种 HTTP 协议，就能接入 EasyAgent 的同一个运行时。
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	core "github.com/lakernote/easy-agent/internal/agent"
)

type Protocol string

const (
	ChatCompletions Protocol = "chat_completions"
	Responses       Protocol = "responses"
)

// Config 只保存模型传输相关配置。业务 Prompt、Skill 和工具不属于模型客户端。
type Config struct {
	BaseURL         string
	APIKey          string
	Protocol        Protocol
	HTTPClient      *http.Client
	Timeout         time.Duration
	DisableThinking bool
}

type Client struct {
	baseURL         string
	apiKey          string
	protocol        Protocol
	httpClient      *http.Client
	disableThinking bool
}

func New(config Config) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("模型地址必须是有效的 http(s) URL")
	}
	protocol := config.Protocol
	if protocol == "" {
		protocol = ChatCompletions
	}
	if protocol != ChatCompletions && protocol != Responses {
		return nil, fmt.Errorf("不支持的模型协议 %q", protocol)
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		timeout := config.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Minute
		}
		httpClient = &http.Client{Timeout: timeout}
	}
	return &Client{baseURL: baseURL, apiKey: strings.TrimSpace(config.APIKey), protocol: protocol, httpClient: httpClient, disableThinking: config.DisableThinking}, nil
}

func (client *Client) Generate(ctx context.Context, request core.Request) (core.Response, error) {
	if strings.TrimSpace(request.Model) == "" {
		return core.Response{}, errors.New("请先配置模型名称")
	}
	if client.protocol == Responses {
		return client.generateResponse(ctx, request)
	}
	return client.generateChat(ctx, request)
}

func (client *Client) post(ctx context.Context, endpoint string, payload any, protocol string, decode func([]byte) (core.Response, error)) (core.Response, error) {
	startedAt := time.Now()
	body, err := json.Marshal(payload)
	if err != nil {
		return core.Response{}, err
	}
	exchange := core.Exchange{Model: requestModel(payload), Protocol: protocol, Request: string(body)}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return core.Response{Exchange: exchange}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if client.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+client.apiKey)
	}
	httpResponse, err := client.httpClient.Do(httpRequest)
	if err != nil {
		exchange.Duration = time.Since(startedAt)
		return core.Response{Exchange: exchange}, err
	}
	defer httpResponse.Body.Close()
	limit := int64(8 * 1024 * 1024)
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		limit = 16 * 1024
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(httpResponse.Body, limit))
	exchange.Response = string(responseBody)
	exchange.StatusCode = httpResponse.StatusCode
	exchange.Duration = time.Since(startedAt)
	if readErr != nil {
		return core.Response{Exchange: exchange}, readErr
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return core.Response{Exchange: exchange}, modelHTTPError(httpResponse, responseBody)
	}
	response, err := decode(responseBody)
	response.Exchange = exchange
	response.Exchange.Model = exchange.Model
	response.Exchange.Usage = response.Usage
	return response, err
}

func modelHTTPError(response *http.Response, body []byte) error {
	message := fmt.Sprintf("模型返回 %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	return &core.ModelError{StatusCode: response.StatusCode, Message: message, RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"))}
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := time.ParseDuration(value + "s"); err == nil && seconds > 0 {
		return seconds
	}
	if when, err := http.ParseTime(value); err == nil {
		if wait := time.Until(when); wait > 0 {
			return wait
		}
	}
	return 0
}

// requestModel 只用于 Trace，避免 post 再依赖两种协议的私有请求类型。
func requestModel(payload any) string {
	data, _ := json.Marshal(payload)
	var value struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(data, &value)
	return value.Model
}

func toolSpecs(tools []core.ToolSpec) []functionTool {
	result := make([]functionTool, 0, len(tools))
	for _, tool := range tools {
		result = append(result, functionTool{Type: "function", Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters})
	}
	return result
}

type functionTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type tokenDetails struct {
	CachedTokens             *int `json:"cached_tokens"`
	CacheWriteTokens         *int `json:"cache_write_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
}

func optionalMax(values ...*int) int {
	result := 0
	for _, value := range values {
		if value != nil && *value > result {
			result = *value
		}
	}
	return result
}

func anyReported(values ...*int) bool {
	for _, value := range values {
		if value != nil {
			return true
		}
	}
	return false
}
