package server

import (
	"fmt"
	"time"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/agent/modeladapter"
	"github.com/lakernote/easy-agent/internal/store"
)

// newModelAdapter is the only server-level construction point for EasyAgent
// models. Runtime orchestration depends on agent.Model and model capabilities,
// never on a provider client concrete type.
func newModelAdapter(settings store.ModelSettings) (agent.Model, error) {
	model, err := modeladapter.New(modeladapter.Config{
		Provider: settings.Provider, Protocol: settings.Protocol, BaseURL: settings.BaseURL,
		APIKey: settings.APIKey, Timeout: time.Duration(settings.RequestTimeoutSeconds) * time.Second,
		DisableThinking: settings.Thinking == "disabled", ContextWindowTokens: settings.ContextWindowTokens,
	})
	if err != nil {
		return nil, err
	}
	capabilities := agent.CapabilitiesOf(model)
	if !capabilities.NativeTools {
		return nil, fmt.Errorf("模型协议 %s 不支持原生 Function Calling，EasyAgent 不会解析文本伪工具调用", settings.Protocol)
	}
	if !capabilities.Streaming {
		return nil, fmt.Errorf("模型协议 %s 没有实现流式 API，不能用于 EasyAgent", settings.Protocol)
	}
	return model, nil
}
