package store

import "testing"

func TestDefaultModelDoesNotGuessInstalledModel(t *testing.T) {
	value := DefaultModelSettings()
	if value.Model != "" {
		t.Fatalf("默认配置不应猜测本机已安装模型: %q", value.Model)
	}
	if value.BaseURL != DefaultOllamaBaseURL || value.RequestTimeoutSeconds != DefaultRequestTimeoutSeconds || value.CompressionThresholdPercent != DefaultCompressionThresholdPercent {
		t.Fatalf("默认配置没有使用集中常量: %+v", value)
	}
}

func TestModelCapabilityDetectionUsesEndpoint(t *testing.T) {
	if !((ModelSettings{BaseURL: "http://localhost:11434/v1"}).IsOllama()) {
		t.Fatal("标准 Ollama 端点应被识别")
	}
	if !((ModelSettings{Provider: "ollama", BaseURL: "https://models.example.com/v1"}).IsOllama()) {
		t.Fatal("反向代理 Ollama 应允许用 Provider 显式声明")
	}
	if !((ModelSettings{Provider: "custom", BaseURL: "https://api.openai.com/v1"}).IsOfficialOpenAI()) {
		t.Fatal("官方 OpenAI 端点不应依赖可编辑 Provider 名称")
	}
	if (ModelSettings{Provider: "openai", BaseURL: "https://example.com/v1"}).IsOfficialOpenAI() {
		t.Fatal("兼容服务不能因为 Provider 名称收到 OpenAI 厂商扩展字段")
	}
}
