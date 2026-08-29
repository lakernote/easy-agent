package models

import "testing"

func TestFreePresetsAreOpenAICompatibleAndNeedEnvironmentKey(t *testing.T) {
	values := Catalog()
	if len(values) < 4 {
		t.Fatalf("免费模型模板过少: %d", len(values))
	}
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value.ID] || value.ID == "" || value.BaseURL == "" || value.Model == "" || value.APIKeyEnv == "" {
			t.Fatalf("模型模板不完整或重复: %+v", value)
		}
		if value.Protocol != "chat_completions" || !value.Free {
			t.Fatalf("免费模板必须走当前通用协议: %+v", value)
		}
		seen[value.ID] = true
	}
	if value, found := func() (Preset, bool) {
		for _, item := range values {
			if item.ID == "cerebras-gpt-oss" {
				return item, true
			}
		}
		return Preset{}, false
	}(); !found || value.Model != "gpt-oss-120b" {
		t.Fatalf("缺少 Cerebras 免费模板: %+v", value)
	}
}
