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
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/appenv"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
func Connect(ctx context.Context, environment *appenv.Environment, config Config) (*Connection, error) {
	if !config.Enabled {
		return nil, errors.New("MCP 尚未启用")
	}
	transport, err := createTransport(environment, config)
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
		execute := func(callContext context.Context, raw json.RawMessage) (agent.ToolResult, error) {
			var arguments any = map[string]any{}
			if len(raw) > 0 && string(raw) != "null" {
				if err := json.Unmarshal(raw, &arguments); err != nil {
					return agent.ToolResult{}, fmt.Errorf("MCP 工具参数错误: %w", err)
				}
			}
			result, err := session.CallTool(callContext, &mcp.CallToolParams{Name: remote.Name, Arguments: arguments})
			if err != nil {
				return agent.ToolResult{}, err
			}
			output := convertResult(result)
			if output.IsError {
				return output, &agent.ToolError{Code: "mcp_tool_error", Message: "MCP 工具 " + remote.Name + " 返回失败", Retryable: false}
			}
			return output, nil
		}
		connection.Tools = append(connection.Tools, agent.Tool{
			Spec:    agent.ToolSpec{Name: name, Description: remote.Description, Parameters: schema, ActivityKind: "mcp", ActivitySource: config.ID, DisplayName: remote.Name},
			Execute: execute,
			Run: func(callContext context.Context, raw json.RawMessage) (string, error) {
				result, err := execute(callContext, raw)
				return result.ModelText(), err
			},
		})
	}
	sort.Slice(connection.Tools, func(i, j int) bool { return connection.Tools[i].Spec.Name < connection.Tools[j].Spec.Name })
	sort.Slice(connection.Info, func(i, j int) bool { return connection.Info[i].Name < connection.Info[j].Name })
	if len(connection.Tools) == 0 {
		_ = connection.Close()
		return nil, fmt.Errorf("MCP %s 没有提供可调用的工具", config.Name)
	}
	return connection, nil
}

func createTransport(environment *appenv.Environment, config Config) (mcp.Transport, error) {
	switch strings.ToLower(strings.TrimSpace(config.Transport)) {
	case "stdio":
		if strings.TrimSpace(config.Command) == "" {
			return nil, errors.New("stdio MCP 缺少启动命令")
		}
		resolved, err := environment.ResolveCommand(config.Command)
		if err != nil {
			return nil, fmt.Errorf("找不到 MCP 启动命令 %q（EasyAgent PATH=%s）: %w", config.Command, environment.Path(), err)
		}
		command := exec.Command(resolved, config.Args...)
		command.Dir = environment.Workspace()
		command.Env = environment.Environ(config.Environment)
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
	config Config
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

func convertResult(result *mcp.CallToolResult) agent.ToolResult {
	if result == nil {
		return agent.NewToolResult("{}")
	}
	converted := agent.ToolResult{IsError: result.IsError}
	if result.StructuredContent != nil {
		if data, err := json.Marshal(result.StructuredContent); err == nil {
			converted.StructuredContent = data
		}
	}
	for _, content := range result.Content {
		switch value := content.(type) {
		case *mcp.TextContent:
			converted.Content = append(converted.Content, agent.ContentBlock{Type: "text", Text: value.Text})
		case *mcp.ImageContent:
			converted.Content = append(converted.Content, agent.ContentBlock{Type: "image", MIMEType: value.MIMEType, Data: append([]byte(nil), value.Data...)})
		case *mcp.AudioContent:
			converted.Content = append(converted.Content, agent.ContentBlock{Type: "audio", MIMEType: value.MIMEType, Data: append([]byte(nil), value.Data...)})
		case *mcp.ResourceLink:
			converted.Content = append(converted.Content, agent.ContentBlock{Type: "resource_link", Name: value.Name, MIMEType: value.MIMEType, URI: value.URI})
		default:
			if data, err := content.MarshalJSON(); err == nil {
				converted.Content = append(converted.Content, agent.ContentBlock{Type: "json", JSON: data})
			}
		}
	}
	if converted.Empty() {
		fallback := agent.NewToolResult("{}")
		fallback.IsError = result.IsError
		return fallback
	}
	return converted
}

func safeName(value string) string {
	value = invalidToolName.ReplaceAllString(strings.TrimSpace(value), "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "tool"
	}
	return value
}
