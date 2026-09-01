package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/appenv"
	"github.com/lakernote/easy-agent/internal/store"
)

// ServerInfo 是写入 System Prompt 的 MCP 元数据。这里只告诉模型“有什么服务”，
// 不提前连接服务，也不把几十个远端工具定义塞进每一轮请求。
type ServerInfo struct {
	ID          string
	Name        string
	Description string
}

// Loader 管理一轮 Agent 任务中的 MCP 连接。每轮创建、每轮关闭，不保存跨任务状态。
// 模型先调用 load_mcp，Loader 才建立连接并把真实工具动态注册给 Runner。
type Loader struct {
	configs     map[string]store.MCPConfig
	connections map[string]*Connection
	environment *appenv.Environment
	register    func([]agent.Tool) error
}

func NewLoader(environment *appenv.Environment, configs []store.MCPConfig) *Loader {
	loader := &Loader{
		configs:     make(map[string]store.MCPConfig),
		connections: make(map[string]*Connection),
		environment: environment,
	}
	for _, config := range configs {
		if config.Enabled {
			loader.configs[config.ID] = config
		}
	}
	return loader
}

// SetRegister 在 Runner 创建后注入动态注册函数，避免 Loader 依赖具体运行时实现。
func (loader *Loader) SetRegister(register func([]agent.Tool) error) {
	loader.register = register
}

func (loader *Loader) Empty() bool { return loader == nil || len(loader.configs) == 0 }

func (loader *Loader) Servers() []ServerInfo {
	if loader == nil {
		return nil
	}
	result := make([]ServerInfo, 0, len(loader.configs))
	for _, config := range loader.configs {
		description := strings.TrimSpace(config.Description)
		if description == "" {
			description = config.Name + " 提供的外部工具"
		}
		result = append(result, ServerInfo{ID: config.ID, Name: config.Name, Description: description})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// Tool 返回唯一常驻的 MCP 入口。远端工具只会在 load_mcp 成功后的下一轮出现。
func (loader *Loader) Tool() agent.Tool {
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name:        "load_mcp",
			Description: "按 ID 连接一个与当前任务相关的 MCP Server，并加载它的工具；不要无目的加载全部 MCP。",
			Loader:      true,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "已启用的 MCP Server ID"},
				},
				"required":             []string{"id"},
				"additionalProperties": false,
			},
		},
		Run: loader.load,
	}
}

func (loader *Loader) load(ctx context.Context, raw json.RawMessage) (string, error) {
	if loader == nil || loader.register == nil {
		return "", errors.New("MCP Loader 尚未初始化")
	}
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("MCP 参数错误: %w", err)
	}
	id := strings.TrimSpace(input.ID)
	config, ok := loader.configs[id]
	if !ok {
		ids := make([]string, 0, len(loader.configs))
		for key := range loader.configs {
			ids = append(ids, key)
		}
		sort.Strings(ids)
		return "", fmt.Errorf("MCP %q 不存在或未启用，可用 MCP: %v", id, ids)
	}
	if connection, loaded := loader.connections[id]; loaded {
		return loadedResult(config, connection.Info, true), nil
	}

	connection, err := Connect(ctx, loader.environment, config)
	if err != nil {
		return "", err
	}
	if err := loader.register(connection.Tools); err != nil {
		_ = connection.Close()
		return "", err
	}
	loader.connections[id] = connection
	return loadedResult(config, connection.Info, false), nil
}

func loadedResult(config store.MCPConfig, tools []ToolInfo, alreadyLoaded bool) string {
	result := struct {
		ID            string     `json:"id"`
		Name          string     `json:"name"`
		AlreadyLoaded bool       `json:"alreadyLoaded"`
		Tools         []ToolInfo `json:"tools"`
	}{ID: config.ID, Name: config.Name, AlreadyLoaded: alreadyLoaded, Tools: tools}
	data, _ := json.Marshal(result)
	return string(data)
}

// Close 关闭这一轮按需建立的全部连接，包括 stdio 子进程。
func (loader *Loader) Close() error {
	if loader == nil {
		return nil
	}
	var result error
	for id, connection := range loader.connections {
		if err := connection.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("关闭 MCP %s: %w", id, err))
		}
	}
	return result
}
