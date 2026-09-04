package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultModelDoesNotGuessInstalledModel(t *testing.T) {
	value := DefaultModelSettings()
	if value.Model != "" {
		t.Fatalf("默认配置不应猜测本机已安装模型: %q", value.Model)
	}
	if value.BaseURL != DefaultOllamaBaseURL || value.RequestTimeoutSeconds != DefaultRequestTimeoutSeconds || value.TurnTimeoutSeconds != 0 || value.CompressionThresholdPercent != DefaultCompressionThresholdPercent {
		t.Fatalf("默认配置没有使用集中常量: %+v", value)
	}
}

func TestCodexModelGetsIndependentTurnTimeoutDefault(t *testing.T) {
	value := (ModelSettings{Runtime: RuntimeCodex}).WithDefaults()
	if value.TurnTimeoutSeconds != DefaultCodexTurnTimeoutSeconds {
		t.Fatalf("Codex 整轮任务超时默认值不正确: got %d, want %d", value.TurnTimeoutSeconds, DefaultCodexTurnTimeoutSeconds)
	}
	if value.RequestTimeoutSeconds != DefaultRequestTimeoutSeconds {
		t.Fatalf("Codex 不应丢失兼容的请求超时默认值: got %d, want %d", value.RequestTimeoutSeconds, DefaultRequestTimeoutSeconds)
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

func TestModelProfilesMigrateLegacyAndKeepActiveSelection(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	legacy, err := database.GetModelSettings()
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ProfileID != "default" {
		t.Fatalf("legacy profile id = %q", legacy.ProfileID)
	}
	legacy.ProfileName = "本地 Ollama"
	legacy.Model = "qwen2.5"
	if err := database.SaveModelSettings(legacy); err != nil {
		t.Fatal(err)
	}
	second := legacy
	second.ProfileID = "openai-main"
	second.ProfileName = "OpenAI 主模型"
	second.Provider = "openai"
	second.BaseURL = "https://api.openai.com/v1"
	second.Model = "gpt-5"
	if err := database.SaveModelSettings(second); err != nil {
		t.Fatal(err)
	}

	profiles, activeID, err := database.ListModelProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 || activeID != "openai-main" {
		t.Fatalf("profiles = %+v, active = %q", profiles, activeID)
	}
	active, err := database.GetModelSettings()
	if err != nil {
		t.Fatal(err)
	}
	if active.ProfileID != "openai-main" || active.Model != "gpt-5" {
		t.Fatalf("active = %+v", active)
	}
	if err := database.DeleteModelProfile("openai-main"); err != nil {
		t.Fatal(err)
	}
	active, err = database.GetModelSettings()
	if err != nil {
		t.Fatal(err)
	}
	if active.ProfileID != "default" || active.Model != "qwen2.5" {
		t.Fatalf("fallback active = %+v", active)
	}
}

func TestModelProfileCannotBeDeletedWhileSessionUsesIt(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	model := DefaultModelSettings()
	model.ProfileID = "kept-for-session"
	model.ProfileName = "被会话使用"
	model.Model = "fixture"
	if err := database.SaveModelSettings(model); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateSessionWithProfile("session-1", "fixture", RuntimeEasyAgent, model.ProfileID, model.Model, t.TempDir(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteModelProfile(model.ProfileID); err == nil {
		t.Fatal("仍被会话使用的 profile 不应删除")
	}
}
