package mcp

import (
	"strings"
	"testing"
)

func TestPlaywrightPresetCanBeInstalledAutomatically(t *testing.T) {
	preset, found := Find("playwright")
	if !found {
		t.Fatal("缺少 Playwright 预设")
	}
	if preset.Action != "install" || preset.Command != "npx" || preset.MinimumNodeMajor != 20 || len(preset.Args) < 2 || strings.Contains(preset.Args[1], "@latest") {
		t.Fatalf("Playwright 安装信息不完整: %+v", preset)
	}
}

func TestGitHubPresetLimitsToolsets(t *testing.T) {
	preset, found := Find("github")
	if !found || preset.Endpoint != "https://api.githubcopilot.com/mcp/" || preset.Headers["X-MCP-Toolsets"] == "" {
		t.Fatalf("GitHub 预设应使用官方远端地址并限制常用 Toolsets: %+v", preset)
	}
}

func TestConfigurablePresetsAreNotAutoInstalled(t *testing.T) {
	for _, id := range []string{"filesystem", "github"} {
		preset, found := Find(id)
		if !found || preset.Action != "configure" {
			t.Fatalf("%s 应要求用户先配置: %+v", id, preset)
		}
	}
}
