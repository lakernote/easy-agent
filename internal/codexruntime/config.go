package codexruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	defaultProvider = "groq"
	defaultEnvKey   = "GROQ_API_KEY"
	secretsFile     = "easyagent-secrets.json"
)

var (
	providerIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	envKeyPattern     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// ProviderConfig 是 EasyAgent 能够管理的 Codex provider 配置。API Key 永远
// 不放在这个结构体里返回；它只在 SaveProviderConfig 的入参中短暂出现。
type ProviderConfig struct {
	ConfigPath       string `json:"configPath"`
	Provider         string `json:"provider"`
	ProviderName     string `json:"providerName"`
	BaseURL          string `json:"baseUrl"`
	Model            string `json:"model"`
	ReasoningEffort  string `json:"reasoningEffort"`
	EnvKey           string `json:"envKey"`
	APIKeyConfigured bool   `json:"apiKeyConfigured"`
	Configured       bool   `json:"configured"`
	Warning          string `json:"warning,omitempty"`
}

// ProviderConfigInput 是浏览器提交的配置。APIKey 为空表示保留已有密钥，
// ClearAPIKey=true 才会删除已保存的密钥。
type ProviderConfigInput struct {
	Provider        string `json:"provider"`
	ProviderName    string `json:"providerName"`
	BaseURL         string `json:"baseUrl"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort"`
	EnvKey          string `json:"envKey"`
	APIKey          string `json:"apiKey"`
	ClearAPIKey     bool   `json:"clearApiKey"`
}

type configDocument map[string]any

func codexHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("读取 Codex 用户目录: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

func configPaths() (string, string, error) {
	home, err := codexHome()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(home, "config.toml"), filepath.Join(home, secretsFile), nil
}

// LoadProviderConfig 读取用户当前 Codex 配置，只返回非敏感字段和密钥存在性。
// 配置文件不存在时返回可直接填写的默认模板，而不是把“未配置”当成服务错误。
func LoadProviderConfig() (ProviderConfig, error) {
	configPath, secretsPath, err := configPaths()
	if err != nil {
		return ProviderConfig{}, err
	}
	document, err := readDocument(configPath)
	if err != nil {
		return ProviderConfig{}, err
	}
	provider := stringValue(document, "model_provider")
	if provider == "" {
		provider = defaultProvider
	}
	providerValues := providerDocument(document, provider)
	envKey := stringValue(providerValues, "env_key")
	warning := ""
	if _, exists := providerValues["api_key"]; exists {
		warning = "检测到 config.toml 里直接保存了 API Key；请使用下方 API Key 输入框迁移到受保护的密钥文件。"
	}
	if looksLikeSecret(envKey) {
		envKey = defaultEnvKey
		warning = "检测到旧配置把 API Key 填进了 env_key；请在下方 API Key 输入框重新保存。"
	}
	if envKey == "" && provider == defaultProvider {
		envKey = defaultEnvKey
	}
	secrets, _ := readSecrets(secretsPath)
	configured := envKey != "" && (strings.TrimSpace(os.Getenv(envKey)) != "" || strings.TrimSpace(secrets[envKey]) != "")
	return ProviderConfig{
		ConfigPath:       configPath,
		Provider:         provider,
		ProviderName:     stringValue(providerValues, "name"),
		BaseURL:          stringValue(providerValues, "base_url"),
		Model:            stringValue(document, "model"),
		ReasoningEffort:  stringValue(document, "model_reasoning_effort"),
		EnvKey:           envKey,
		APIKeyConfigured: configured,
		Configured:       strings.TrimSpace(stringValue(document, "model")) != "" && strings.TrimSpace(stringValue(providerValues, "base_url")) != "" && configured,
		Warning:          warning,
	}, nil
}

// LoadManagedEnvironment 读取 EasyAgent 自己保存的 Codex 密钥。文件只允许当前
// 用户访问，且内容按 JSON 解析，不执行 shell，避免把配置内容当成命令运行。
func LoadManagedEnvironment() (map[string]string, error) {
	_, secretsPath, err := configPaths()
	if err != nil {
		return nil, err
	}
	return readSecrets(secretsPath)
}

// SaveProviderConfig 更新 Codex config.toml 中的模型/provider，并把 API Key
// 保存到独立的 0600 文件。这样 config.toml 不会泄漏密钥，app-server 仍可通过
// env_key 读取对应环境变量。
func SaveProviderConfig(input ProviderConfigInput) (ProviderConfig, error) {
	input, err := normalizeProviderInput(input)
	if err != nil {
		return ProviderConfig{}, err
	}
	configPath, secretsPath, err := configPaths()
	if err != nil {
		return ProviderConfig{}, err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return ProviderConfig{}, fmt.Errorf("创建 Codex 配置目录: %w", err)
	}

	secrets, err := readSecrets(secretsPath)
	if err != nil {
		return ProviderConfig{}, err
	}
	if strings.TrimSpace(input.APIKey) != "" {
		secrets[input.EnvKey] = strings.TrimSpace(input.APIKey)
	} else if input.ClearAPIKey {
		delete(secrets, input.EnvKey)
	}

	document, err := readDocument(configPath)
	if err != nil {
		return ProviderConfig{}, err
	}
	document["model"] = input.Model
	document["model_provider"] = input.Provider
	if input.ReasoningEffort == "" {
		delete(document, "model_reasoning_effort")
	} else {
		document["model_reasoning_effort"] = input.ReasoningEffort
	}
	providers := providerDocuments(document)
	provider := providerDocument(providers, input.Provider)
	provider["name"] = input.ProviderName
	provider["base_url"] = input.BaseURL
	provider["env_key"] = input.EnvKey
	provider["wire_api"] = "responses"
	provider["requires_openai_auth"] = false
	delete(provider, "api_key")
	delete(provider, "apiKey")
	delete(provider, "bearer_token")
	delete(provider, "experimental_bearer_token")
	providers[input.Provider] = provider
	document["model_providers"] = providers

	if err := writeSecrets(secretsPath, secrets); err != nil {
		return ProviderConfig{}, err
	}
	if err := writeDocument(configPath, document); err != nil {
		return ProviderConfig{}, err
	}
	return LoadProviderConfig()
}

func normalizeProviderInput(input ProviderConfigInput) (ProviderConfigInput, error) {
	input.Provider = strings.TrimSpace(input.Provider)
	if input.Provider == "" {
		input.Provider = defaultProvider
	}
	if !providerIDPattern.MatchString(input.Provider) {
		return ProviderConfigInput{}, errors.New("Provider ID 只能包含字母、数字、下划线或短横线")
	}
	input.ProviderName = strings.TrimSpace(input.ProviderName)
	if input.ProviderName == "" {
		input.ProviderName = input.Provider
	}
	input.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	parsed, err := url.Parse(input.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ProviderConfigInput{}, errors.New("Base URL 必须是带 http/https 的完整地址")
	}
	input.Model = strings.TrimSpace(input.Model)
	if input.Model == "" {
		return ProviderConfigInput{}, errors.New("Codex 默认模型不能为空")
	}
	input.EnvKey = strings.TrimSpace(input.EnvKey)
	if input.EnvKey == "" {
		input.EnvKey = defaultEnvKey
	}
	// 这是最常见、也最隐蔽的手工配置错误：把 gsk_... 密钥误填到了 env_key。
	if looksLikeSecret(input.EnvKey) {
		return ProviderConfigInput{}, errors.New("env_key 必须填写环境变量名（例如 GROQ_API_KEY），不能填写 API Key 本身")
	}
	if !envKeyPattern.MatchString(input.EnvKey) {
		return ProviderConfigInput{}, errors.New("API Key 环境变量名无效，例如 GROQ_API_KEY")
	}
	input.ReasoningEffort = strings.TrimSpace(input.ReasoningEffort)
	if input.ReasoningEffort != "" && input.ReasoningEffort != "low" && input.ReasoningEffort != "medium" && input.ReasoningEffort != "high" && input.ReasoningEffort != "xhigh" {
		return ProviderConfigInput{}, errors.New("推理强度只能是 low、medium、high 或 xhigh")
	}
	return input, nil
}

func looksLikeSecret(value string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "gsk_") || len(strings.TrimSpace(value)) > 128
}

func readDocument(path string) (configDocument, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return configDocument{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 Codex 配置: %w", err)
	}
	document := configDocument{}
	if _, err := toml.Decode(string(data), &document); err != nil {
		return nil, fmt.Errorf("解析 Codex config.toml: %w", err)
	}
	return document, nil
}

func writeDocument(path string, document configDocument) error {
	var buffer bytes.Buffer
	if err := toml.NewEncoder(&buffer).Encode(document); err != nil {
		return fmt.Errorf("生成 Codex config.toml: %w", err)
	}
	return atomicWrite(path, buffer.Bytes(), 0o600)
}

func providerDocuments(document configDocument) map[string]any {
	value, ok := document["model_providers"].(map[string]any)
	if ok {
		return value
	}
	return map[string]any{}
}

func providerDocument(document configDocument, provider string) map[string]any {
	providers := providerDocuments(document)
	value, ok := providers[provider].(map[string]any)
	if ok {
		return value
	}
	return map[string]any{}
}

func stringValue(document map[string]any, key string) string {
	value, ok := document[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func readSecrets(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 EasyAgent Codex 密钥: %w", err)
	}
	values := map[string]string{}
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("解析 EasyAgent Codex 密钥: %w", err)
	}
	return values, nil
}

func writeSecrets(path string, values map[string]string) error {
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return fmt.Errorf("编码 EasyAgent Codex 密钥: %w", err)
	}
	return atomicWrite(path, append(data, '\n'), 0o600)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".easyagent-codex-*")
	if err != nil {
		return fmt.Errorf("创建临时配置文件: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("保护临时配置文件: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("写入配置文件: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("同步配置文件: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭临时配置文件: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("替换配置文件: %w", err)
	}
	return os.Chmod(path, mode)
}
