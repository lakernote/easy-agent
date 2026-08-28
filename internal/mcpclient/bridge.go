// Package mcpclient 把 MCP Server 暴露的远端工具转换成 EasyAgent 的普通工具。
//
// Agent 核心并不知道 MCP 的存在：对核心来说，内置工具和 MCP 工具都是
// []agent.Tool。这一层只负责连接、协议转换和关闭连接。
package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxToolOutput = 24 * 1024

var invalidToolName = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// ToolInfo 是连接测试返回给页面的精简工具说明。
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Connection 保存一次 MCP 会话和已经转换好的工具。
// 一轮 Agent 任务结束时必须调用 Close，避免残留子进程或 HTTP 长连接。
type Connection struct {
	Tools []agent.Tool
	Info  []ToolInfo
	close func() error
}

func (connection *Connection) Close() error {
	if connection == nil || connection.close == nil {
		return nil
	}
	return connection.close()
}

// Connect 连接一个已启用的 MCP 配置并读取它的工具清单。
func Connect(ctx context.Context, config store.MCPConfig) (*Connection, error) {
	if !config.Enabled {
		return nil, errors.New("MCP 尚未启用")
	}
	transport, err := createTransport(config)
	if err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "easyagent", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("连接 MCP %s: %w", config.Name, err)
	}
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("读取 MCP %s 工具: %w", config.Name, err)
	}

	connection := &Connection{close: session.Close}
	prefix := "mcp__" + safeName(config.ID) + "__"
	for _, remote := range listed.Tools {
		remote := remote
		if remote == nil || strings.TrimSpace(remote.Name) == "" {
			continue
		}
		name := prefix + safeName(remote.Name)
		schema := normalizeSchema(remote.InputSchema)
		connection.Info = append(connection.Info, ToolInfo{Name: name, Description: remote.Description})
		connection.Tools = append(connection.Tools, agent.Tool{
			Spec: agent.ToolSpec{Name: name, Description: remote.Description, Parameters: schema},
			Run: func(callContext context.Context, raw json.RawMessage) (string, error) {
				var arguments any = map[string]any{}
				if len(raw) > 0 && string(raw) != "null" {
					if err := json.Unmarshal(raw, &arguments); err != nil {
						return "", fmt.Errorf("MCP 工具参数错误: %w", err)
					}
				}
				result, err := session.CallTool(callContext, &mcp.CallToolParams{Name: remote.Name, Arguments: arguments})
				if err != nil {
					return "", err
				}
				output := formatResult(result)
				if result.IsError {
					return output, errors.New(output)
				}
				return output, nil
			},
		})
	}
	if len(connection.Tools) == 0 {
		_ = connection.Close()
		return nil, fmt.Errorf("MCP %s 没有提供可调用的工具", config.Name)
	}
	return connection, nil
}

func createTransport(config store.MCPConfig) (mcp.Transport, error) {
	switch strings.ToLower(strings.TrimSpace(config.Transport)) {
	case "stdio":
		if strings.TrimSpace(config.Command) == "" {
			return nil, errors.New("stdio MCP 缺少启动命令")
		}
		command := exec.Command(config.Command, config.Args...)
		command.Env = append(os.Environ(), mapToEnvironment(config.Environment)...)
		return &mcp.CommandTransport{Command: command}, nil
	case "http", "streamable_http":
		if strings.TrimSpace(config.Endpoint) == "" {
			return nil, errors.New("HTTP MCP 缺少 Endpoint")
		}
		httpClient := &http.Client{Timeout: 90 * time.Second, Transport: authTransport{base: http.DefaultTransport, config: config}}
		return &mcp.StreamableClientTransport{Endpoint: config.Endpoint, HTTPClient: httpClient, DisableStandaloneSSE: true}, nil
	default:
		return nil, fmt.Errorf("不支持的 MCP Transport %q", config.Transport)
	}
}

type authTransport struct {
	base   http.RoundTripper
	config store.MCPConfig
}

func (transport authTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	for key, value := range transport.config.Headers {
		cloned.Header.Set(key, value)
	}
	switch strings.ToLower(transport.config.AuthType) {
	case "bearer", "token":
		if transport.config.Token != "" {
			cloned.Header.Set("Authorization", "Bearer "+transport.config.Token)
		}
	case "basic":
		cloned.SetBasicAuth(transport.config.Username, transport.config.Password)
	}
	return transport.base.RoundTrip(cloned)
}

func normalizeSchema(value any) map[string]any {
	if schema, ok := value.(map[string]any); ok && schema != nil {
		return schema
	}
	data, err := json.Marshal(value)
	if err == nil {
		var schema map[string]any
		if json.Unmarshal(data, &schema) == nil && schema != nil {
			return schema
		}
	}
	return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": true}
}

func formatResult(result *mcp.CallToolResult) string {
	if result == nil {
		return "{}"
	}
	if result.StructuredContent != nil {
		if data, err := json.Marshal(result.StructuredContent); err == nil {
			return truncate(string(data))
		}
	}
	parts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
			continue
		}
		if data, err := content.MarshalJSON(); err == nil {
			parts = append(parts, string(data))
		}
	}
	return truncate(strings.Join(parts, "\n"))
}

func truncate(value string) string {
	if len(value) <= maxToolOutput {
		return value
	}
	half := maxToolOutput / 2
	return value[:half] + fmt.Sprintf("\n… MCP 输出已截断 %d 字节 …\n", len(value)-maxToolOutput) + value[len(value)-half:]
}

func safeName(value string) string {
	value = invalidToolName.ReplaceAllString(strings.TrimSpace(value), "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "tool"
	}
	return value
}

func mapToEnvironment(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key, value := range values {
		if strings.TrimSpace(key) != "" {
			result = append(result, key+"="+value)
		}
	}
	return result
}
