package presets

import (
	"strings"
	"testing"
)

func TestPlaywrightPresetCanBeInstalledAutomatically(t *testing.T) {
	preset, found := Find("playwright")
	if !found {
		t.Fatal("缺少 Playwright 预设")
	}
	if preset.Action != "install" || preset.NPMPackage != "@playwright/mcp@0.0.79" || preset.NPMExecutable != "playwright-mcp" || preset.MinimumNodeMajor != 20 || strings.Contains(preset.NPMPackage, "@latest") || !strings.HasPrefix(preset.Command, "@runtime/") {
		t.Fatalf("Playwright 安装信息不完整: %+v", preset)
	}
}

func TestFilesystemPresetIsRemoved(t *testing.T) {
	if _, found := Find("filesystem"); found {
		t.Fatal("Filesystem 不应再作为内置 MCP 预设提供")
	}
}

func TestGitHubPresetLimitsToolsets(t *testing.T) {
	preset, found := Find("github")
	if !found || preset.Endpoint != "https://api.githubcopilot.com/mcp/" || preset.Headers["X-MCP-Toolsets"] == "" {
		t.Fatalf("GitHub 预设应使用官方远端地址并限制常用 Toolsets: %+v", preset)
	}
}

func TestContext7PresetUsesOfficialRemoteEndpoint(t *testing.T) {
	preset, found := Find("context7")
	if !found || preset.Action != "configure" || preset.Transport != "http" || preset.Endpoint != "https://mcp.context7.com/mcp" {
		t.Fatalf("Context7 预设应使用官方远端 MCP: %+v", preset)
	}
}

func TestConfigurablePresetsAreNotAutoInstalled(t *testing.T) {
	for _, id := range []string{"context7", "github"} {
		preset, found := Find(id)
		if !found || preset.Action != "configure" {
			t.Fatalf("%s 应要求用户先配置: %+v", id, preset)
		}
	}
}
