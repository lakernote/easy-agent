package codexruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveProviderConfigKeepsAPIKeyOutOfToml(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	config, err := SaveProviderConfig(ProviderConfigInput{
		Provider:        "groq",
		ProviderName:    "Groq",
		BaseURL:         "https://api.groq.com/openai/v1",
		Model:           "openai/gpt-oss-20b",
		ReasoningEffort: "medium",
		EnvKey:          "GROQ_API_KEY",
		APIKey:          "gsk-test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !config.Configured || !config.APIKeyConfigured || config.Model != "openai/gpt-oss-20b" {
		t.Fatalf("unexpected config: %+v", config)
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "gsk-test-secret") || !strings.Contains(string(data), `env_key = "GROQ_API_KEY"`) {
		t.Fatalf("config.toml should contain env name but not secret: %s", data)
	}
	secrets, err := LoadManagedEnvironment()
	if err != nil || secrets["GROQ_API_KEY"] != "gsk-test-secret" {
		t.Fatalf("managed secret not saved: values=%v err=%v", secrets, err)
	}
	secretInfo, err := os.Stat(filepath.Join(home, ".codex", secretsFile))
	if err != nil || secretInfo.Mode().Perm() != 0o600 {
		t.Fatalf("managed secret file should be 0600: info=%v err=%v", secretInfo, err)
	}
}

func TestProviderConfigRejectsSecretAsEnvironmentKey(t *testing.T) {
	_, err := normalizeProviderInput(ProviderConfigInput{
		Provider: "groq", BaseURL: "https://api.groq.com/openai/v1", Model: "openai/gpt-oss-20b", EnvKey: "gsk_test_secret",
	})
	if err == nil || !strings.Contains(err.Error(), "不能填写 API Key") {
		t.Fatalf("expected actionable env_key validation, got %v", err)
	}
}

func TestLoadProviderConfigDoesNotEchoMisplacedSecret(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDirectory := filepath.Join(home, ".codex")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	config := "model = \"openai/gpt-oss-20b\"\nmodel_provider = \"groq\"\n\n[model_providers.groq]\nbase_url = \"https://api.groq.com/openai/v1\"\nenv_key = \"gsk_secret_value\"\napi_key = \"gsk_secret_value\"\n"
	if err := os.WriteFile(filepath.Join(configDirectory, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := LoadProviderConfig()
	if err != nil {
		t.Fatal(err)
	}
	if value.EnvKey != defaultEnvKey || strings.Contains(value.EnvKey, "gsk_") || !strings.Contains(value.Warning, "API Key") {
		t.Fatalf("misplaced secret should be hidden and explained: %+v", value)
	}
}

func TestSyncMCPServersDocumentPreservesUnmanagedAndKeepsSecretsInEnvironment(t *testing.T) {
	document := configDocument{"mcp_servers": map[string]any{
		"personal":        map[string]any{"url": "https://personal.example/mcp"},
		"easyagent_stale": map[string]any{"url": "https://stale.example/mcp"},
	}}
	environment := syncMCPServersDocument(document, []MCPServerConfig{
		{ID: "docs", Transport: "http", Endpoint: "https://docs.example/mcp", AuthType: "bearer", Token: "secret-token", Headers: map[string]string{"X-Team": "alpha"}},
		{ID: "local", Transport: "stdio", Command: "/usr/bin/local-mcp", Args: []string{"serve"}, Environment: map[string]string{"LOCAL_TOKEN": "local-secret", "bad-key": "ignored"}},
	})
	servers := providerDocumentMap(document, "mcp_servers")
	if _, ok := servers["personal"]; !ok {
		t.Fatal("不属于 EasyAgent 的 MCP 配置不应被删除")
	}
	if _, ok := servers["easyagent_stale"]; ok {
		t.Fatal("已失效的 EasyAgent MCP 配置应被清理")
	}
	encoded, _ := json.Marshal(document)
	if strings.Contains(string(encoded), "secret-token") || strings.Contains(string(encoded), "local-secret") {
		t.Fatalf("MCP 密钥不应写入 Codex TOML 文档: %s", encoded)
	}
	if environment["LOCAL_TOKEN"] != "local-secret" || environment["EASYAGENT_DOCS_TOKEN"] != "secret-token" {
		t.Fatalf("MCP 密钥应通过进程环境传递: %+v", environment)
	}
}
