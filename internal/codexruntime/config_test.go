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

func TestSaveProviderConfigPreservesOtherProviders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDirectory := filepath.Join(home, ".codex")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	initial := `model = "custom-model"
model_provider = "custom-responses"

[model_providers.custom-responses]
name = "Responses"
base_url = "https://responses.example.com/v1"
env_key = "RESPONSES_API_KEY"
wire_api = "responses"

[model_providers.groq]
name = "Groq"
base_url = "https://api.groq.com/openai/v1"
env_key = "GROQ_API_KEY"
wire_api = "responses"
`
	if err := os.WriteFile(filepath.Join(configDirectory, "config.toml"), []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := SaveProviderConfig(ProviderConfigInput{
		Provider: "groq", ProviderName: "Groq Updated", BaseURL: "https://api.groq.com/openai/v1",
		Model: "openai/gpt-oss-20b", EnvKey: "GROQ_API_KEY", APIKey: "gsk-test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Providers) != 2 {
		t.Fatalf("saving one Provider should preserve the directory: %+v", config.Providers)
	}

	data, err := os.ReadFile(filepath.Join(configDirectory, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	contents := string(data)
	if !strings.Contains(contents, "model_providers.custom-responses") || !strings.Contains(contents, "model_providers.groq") {
		t.Fatalf("config.toml should retain both Provider entries: %s", contents)
	}
	if !strings.Contains(contents, `model_provider = "groq"`) || !strings.Contains(contents, `model = "openai/gpt-oss-20b"`) {
		t.Fatalf("selected Provider should become the Codex default: %s", contents)
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
	if len(value.Providers) != 1 || value.Providers[0].ID != "groq" {
		t.Fatalf("nested TOML provider should be listed without exposing the secret: %+v", value.Providers)
	}
}

func TestLoadProviderConfigTreatsOpenAIAsOfficialAndExcludesStaleEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDirectory := filepath.Join(home, ".codex")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	config := `model = "gpt-5.6-sol"
model_provider = "openai"

[model_providers.openai]
name = "Groq"
base_url = "https://api.groq.com/openai/v1"
env_key = "GROQ_API_KEY"

[model_providers.custom-responses]
name = "Responses"
base_url = "https://responses.example.com/v1"
env_key = "RESPONSES_API_KEY"
`
	if err := os.WriteFile(filepath.Join(configDirectory, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	value, err := LoadProviderConfig()
	if err != nil {
		t.Fatal(err)
	}
	if value.Provider != "" || value.ProviderName != "" || value.BaseURL != "" {
		t.Fatalf("built-in openai should be represented as official login: %+v", value)
	}
	if len(value.Providers) != 1 || value.Providers[0].ID != "custom-responses" || value.Providers[0].Name != "Responses" || value.Providers[0].BaseURL != "https://responses.example.com/v1" {
		t.Fatalf("stale built-in provider should not appear in directory: %+v", value.Providers)
	}
}

func TestNormalizeProviderInputRejectsBuiltInOpenAI(t *testing.T) {
	_, err := normalizeProviderInput(ProviderConfigInput{Provider: "openai"})
	if err == nil || !strings.Contains(err.Error(), "官方内置") {
		t.Fatalf("expected built-in provider validation, got %v", err)
	}
}

func TestDeleteProviderConfigRemovesOnlyTargetAndFallsBackToOfficial(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDirectory := filepath.Join(home, ".codex")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	config := `model = "openai/gpt-oss-20b"
model_provider = "groq"

[model_providers.groq]
name = "Groq"
base_url = "https://api.groq.com/openai/v1"
env_key = "GROQ_API_KEY"

[model_providers.other]
name = "Other"
base_url = "https://other.example/v1"
env_key = "OTHER_API_KEY"
`
	if err := os.WriteFile(filepath.Join(configDirectory, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeSecrets(filepath.Join(configDirectory, secretsFile), map[string]string{"GROQ_API_KEY": "groq-secret", "OTHER_API_KEY": "other-secret"}); err != nil {
		t.Fatal(err)
	}

	value, err := DeleteProviderConfig("groq")
	if err != nil {
		t.Fatal(err)
	}
	if value.Provider != "" || len(value.Providers) != 1 || value.Providers[0].ID != "other" {
		t.Fatalf("delete should preserve other providers and select official login: %+v", value)
	}
	data, err := os.ReadFile(filepath.Join(configDirectory, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "model_providers.groq") || !strings.Contains(string(data), `model_provider = "openai"`) || !strings.Contains(string(data), "model_providers.other") {
		t.Fatalf("unexpected config after deletion: %s", data)
	}
	secrets, err := readSecrets(filepath.Join(configDirectory, secretsFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := secrets["GROQ_API_KEY"]; exists || secrets["OTHER_API_KEY"] != "other-secret" {
		t.Fatalf("only the unused managed secret should be removed: %+v", secrets)
	}
}

func TestDeleteProviderConfigRejectsOfficialLogin(t *testing.T) {
	if _, err := DeleteProviderConfig("openai"); err == nil || !strings.Contains(err.Error(), "不能删除") {
		t.Fatalf("expected official login protection, got %v", err)
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

func TestSyncMCPServersDocumentTreatsTokenAsBearerAuthentication(t *testing.T) {
	document := configDocument{}
	environment := syncMCPServersDocument(document, []MCPServerConfig{{
		ID: "token-mcp", Transport: "http", Endpoint: "https://mcp.example/mcp", AuthType: "token", Token: "secret-token",
	}})
	servers := providerDocumentMap(document, "mcp_servers")
	entry, ok := servers["easyagent_token-mcp"].(map[string]any)
	if !ok || entry["bearer_token_env_var"] != "EASYAGENT_TOKEN_MCP_TOKEN" {
		t.Fatalf("token authentication should use Codex bearer token env var: %+v", entry)
	}
	if environment["EASYAGENT_TOKEN_MCP_TOKEN"] != "secret-token" {
		t.Fatalf("token authentication should be passed through the process environment: %+v", environment)
	}
}
