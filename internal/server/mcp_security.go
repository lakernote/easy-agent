package server

import (
	"errors"
	"strings"

	"github.com/lakernote/easy-agent/internal/store"
)

func publicMCPs(values []store.MCPConfig) []store.MCPConfig {
	result := make([]store.MCPConfig, 0, len(values))
	for _, value := range values {
		result = append(result, publicMCP(value))
	}
	return result
}

func publicMCP(value store.MCPConfig) store.MCPConfig {
	value.SecretConfigured = value.Token != "" || value.Password != "" || hasRedactedMCPValue(value.Args, value.Headers, value.Environment)
	value.Token, value.Password = "", ""
	// Args、Header 和环境变量都可能承载凭证；只遮蔽敏感值并保留非敏感配置，
	// 保存时由服务端把占位恢复成原值，避免 bootstrap 把秘密发到浏览器。
	value.Args = redactMCPArgs(value.Args)
	value.Headers = redactMCPMap(value.Headers)
	value.Environment = redactMCPMap(value.Environment)
	return value
}

const redactedMCPValue = "__EASYAGENT_REDACTED__"

func isSensitiveMCPKey(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, marker := range []string{"authorization", "api-key", "apikey", "token", "secret", "password", "passwd", "cookie", "credential", "private-key", "access-key"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func redactMCPMap(values map[string]string) map[string]string {
	result := cloneMap(values)
	for key, value := range result {
		if isSensitiveMCPKey(key) || isSensitiveMCPKey(value) {
			result[key] = redactedMCPValue
		}
	}
	return result
}

func restoreRedactedMap(values, original map[string]string) map[string]string {
	result := cloneMap(values)
	for key, value := range result {
		if value == redactedMCPValue && original != nil {
			if old, ok := original[key]; ok {
				result[key] = old
			}
		}
	}
	return result
}

func redactMCPArgs(values []string) []string {
	result := append([]string(nil), values...)
	redactNext := false
	for index, value := range result {
		if redactNext {
			result[index] = redactedMCPValue
			redactNext = false
			continue
		}
		if key, _, found := strings.Cut(value, "="); found && isSensitiveMCPKey(key) {
			result[index] = key + "=" + redactedMCPValue
			continue
		}
		if isSensitiveMCPKey(value) {
			redactNext = true
		}
	}
	return result
}

func restoreRedactedArgs(values, original []string) []string {
	result := append([]string(nil), values...)
	for index, value := range result {
		if value != redactedMCPValue || index >= len(original) {
			continue
		}
		result[index] = original[index]
	}
	return result
}

func hasRedactedMCPValue(args []string, headers, environment map[string]string) bool {
	for _, value := range args {
		if value == redactedMCPValue || isSensitiveMCPKey(value) {
			return true
		}
	}
	for key, value := range headers {
		if isSensitiveMCPKey(key) || isSensitiveMCPKey(value) {
			return true
		}
	}
	for key, value := range environment {
		if isSensitiveMCPKey(key) || isSensitiveMCPKey(value) {
			return true
		}
	}
	return false
}

func validateMCP(value store.MCPConfig) error {
	switch value.Transport {
	case "stdio":
		if strings.TrimSpace(value.Command) == "" {
			return errors.New("stdio MCP 缺少命令")
		}
	case "http", "streamable_http":
		if strings.TrimSpace(value.Endpoint) == "" {
			return errors.New("HTTP MCP 缺少 Endpoint")
		}
	default:
		return errors.New("MCP Transport 只能是 stdio、http 或 streamable_http")
	}
	if !value.Enabled {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(value.AuthType)) {
	case "", "none":
	case "bearer", "token":
		if strings.TrimSpace(value.Token) == "" {
			return errors.New("启用 Bearer 认证前必须填写 Token")
		}
	case "basic":
		if strings.TrimSpace(value.Username) == "" || value.Password == "" {
			return errors.New("启用 Basic 认证前必须填写用户名和密码")
		}
	default:
		return errors.New("MCP 认证方式只能是无认证、Bearer Token 或用户名密码")
	}
	return nil
}
