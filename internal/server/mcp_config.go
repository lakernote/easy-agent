package server

import (
	"github.com/lakernote/easy-agent/internal/mcpclient"
	"github.com/lakernote/easy-agent/internal/store"
)

// mcpClientConfig 把持久化层配置转换成连接层配置，隔离 SQLite 数据模型和 MCP
// 协议适配器。新增数据库字段时，不会自动泄漏到远端连接层。
func mcpClientConfig(value store.MCPConfig) mcpclient.Config {
	return mcpclient.Config{
		ID: value.ID, Name: value.Name, Description: value.Description, Enabled: value.Enabled,
		Transport: value.Transport, Command: value.Command, Args: append([]string(nil), value.Args...),
		Endpoint: value.Endpoint, AuthType: value.AuthType, Token: value.Token,
		Username: value.Username, Password: value.Password,
		Headers: cloneMap(value.Headers), Environment: cloneMap(value.Environment),
	}
}

func mcpClientConfigs(values []store.MCPConfig) []mcpclient.Config {
	result := make([]mcpclient.Config, 0, len(values))
	for _, value := range values {
		result = append(result, mcpClientConfig(value))
	}
	return result
}
