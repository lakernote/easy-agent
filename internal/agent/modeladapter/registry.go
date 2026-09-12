// Package modeladapter is the transport boundary for EasyAgent models. The
// server selects a protocol by name; the core runner never imports a provider.
package modeladapter

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/agent/anthropic"
	"github.com/lakernote/easy-agent/internal/agent/ollama"
	"github.com/lakernote/easy-agent/internal/agent/openai"
)

const (
	ProtocolChatCompletions = "chat_completions"
	ProtocolResponses       = "responses"
	ProtocolOllamaChat      = ollama.Protocol
	ProtocolAnthropic       = anthropic.Protocol
)

type Config struct {
	Provider            string
	Protocol            string
	BaseURL             string
	APIKey              string
	HTTPClient          *http.Client
	Timeout             time.Duration
	DisableThinking     bool
	ContextWindowTokens int
}

type Factory func(Config) (agent.Model, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

func init() {
	MustRegister(ProtocolChatCompletions, func(config Config) (agent.Model, error) {
		isOllama := strings.EqualFold(strings.TrimSpace(config.Provider), "ollama")
		return openai.New(openai.Config{
			BaseURL: config.BaseURL, APIKey: config.APIKey, Protocol: openai.ChatCompletions,
			HTTPClient: config.HTTPClient, Timeout: config.Timeout, DisableThinking: config.DisableThinking,
			KeepThinkingForTools: isOllama, DisableContinuation: isOllama,
		})
	})
	MustRegister(ProtocolResponses, func(config Config) (agent.Model, error) {
		isOllama := strings.EqualFold(strings.TrimSpace(config.Provider), "ollama")
		return openai.New(openai.Config{
			BaseURL: config.BaseURL, APIKey: config.APIKey, Protocol: openai.Responses,
			HTTPClient: config.HTTPClient, Timeout: config.Timeout, DisableThinking: config.DisableThinking,
			KeepThinkingForTools: isOllama, DisableContinuation: isOllama,
		})
	})
	MustRegister(ProtocolOllamaChat, func(config Config) (agent.Model, error) {
		return ollama.New(ollama.Config{
			BaseURL: config.BaseURL, APIKey: config.APIKey, HTTPClient: config.HTTPClient, Timeout: config.Timeout,
			DisableThinking: config.DisableThinking, ContextWindow: config.ContextWindowTokens,
		})
	})
	MustRegister(ProtocolAnthropic, func(config Config) (agent.Model, error) {
		return anthropic.New(anthropic.Config{BaseURL: config.BaseURL, APIKey: config.APIKey, HTTPClient: config.HTTPClient, Timeout: config.Timeout})
	})
}

func Register(protocol string, factory Factory) error {
	protocol = strings.TrimSpace(protocol)
	if protocol == "" || factory == nil {
		return fmt.Errorf("模型 Adapter 协议名和 Factory 不能为空")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[protocol]; exists {
		return fmt.Errorf("模型 Adapter %q 已注册", protocol)
	}
	registry[protocol] = factory
	return nil
}

func MustRegister(protocol string, factory Factory) {
	if err := Register(protocol, factory); err != nil {
		panic(err)
	}
}

func New(config Config) (agent.Model, error) {
	protocol := strings.TrimSpace(config.Protocol)
	if protocol == "" {
		protocol = ProtocolChatCompletions
	}
	registryMu.RLock()
	factory := registry[protocol]
	registryMu.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("不支持的模型协议 %q（可用：%s）", protocol, strings.Join(Protocols(), "、"))
	}
	return factory(config)
}

func Supports(protocol string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := registry[strings.TrimSpace(protocol)]
	return ok
}

func Protocols() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	result := make([]string, 0, len(registry))
	for protocol := range registry {
		result = append(result, protocol)
	}
	sort.Strings(result)
	return result
}
