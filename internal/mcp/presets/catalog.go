// Package mcp 管理 EasyAgent 提供的一键安装 MCP 预设。
//
// 预设本身不是另一套工具协议，也不会进入 Agent 核心；用户启用后，MCP 客户端
// 才连接服务器并把远端能力转换为普通 agent.Tool。
package presets

type Preset struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	Transport        string            `json:"transport"`
	Command          string            `json:"command,omitempty"`
	Args             []string          `json:"args,omitempty"`
	Endpoint         string            `json:"endpoint,omitempty"`
	AuthType         string            `json:"authType,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	Action           string            `json:"action"`
	Requirement      string            `json:"requirement"`
	RequiredCommands []string          `json:"requiredCommands,omitempty"`
	MinimumNodeMajor int               `json:"minimumNodeMajor,omitempty"`
	NPMPackage       string            `json:"npmPackage,omitempty"`
	NPMExecutable    string            `json:"npmExecutable,omitempty"`
}

func Catalog() []Preset {
	return []Preset{
		{
			ID: "openai-docs", Name: "OpenAI Docs", Description: "搜索并读取 OpenAI、Codex 和 API 官方文档",
			Transport: "http", Endpoint: "https://developers.openai.com/mcp",
			Action: "configure", Requirement: "OpenAI 官方远端 MCP · 无需本地安装",
		},
		{
			ID: "context7", Name: "Context7", Description: "查询最新的开源库与框架文档和代码示例",
			Transport: "http", Endpoint: "https://mcp.context7.com/mcp",
			Action: "configure", Requirement: "Context7 官方远端 MCP · API Key 可选，配置后可提高额度",
		},
		{
			ID: "playwright", Name: "Playwright", Description: "复现网页问题，操作页面并检查结构与请求",
			Transport: "stdio", Command: "@runtime/mcp/playwright/node_modules/.bin/playwright-mcp", Args: []string{"--headless", "--isolated"},
			Action: "install", Requirement: "宿主机 Node.js 20+ / npm · MCP 包私有安装 · 固定版本 0.0.79", RequiredCommands: []string{"node", "npm"}, MinimumNodeMajor: 20,
			NPMPackage: "@playwright/mcp@0.0.79", NPMExecutable: "playwright-mcp",
		},
		{
			ID: "github", Name: "GitHub", Description: "读取仓库、Issue、PR 和 Actions 上下文",
			Transport: "http", Endpoint: "https://api.githubcopilot.com/mcp/", AuthType: "bearer",
			Headers: map[string]string{"X-MCP-Toolsets": "repos,issues,pull_requests,actions"},
			Action:  "configure", Requirement: "GitHub 官方远端 MCP · 需要 PAT 或 App Token",
		},
	}
}

// Find 只允许通过内置清单安装预设，避免安装接口变成任意命令执行入口。
func Find(id string) (Preset, bool) {
	for _, preset := range Catalog() {
		if preset.ID == id {
			return preset, true
		}
	}
	return Preset{}, false
}
