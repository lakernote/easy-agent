package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/appenv"
	"github.com/lakernote/easy-agent/internal/store"
)

const (
	defaultMCPToolLimit = 5
	maxMCPToolLimit     = 5
	directMCPToolLimit  = 5
	directMCPSchemaSize = 12 * 1024
)

// ServerInfo 是写入 System Prompt 的 MCP 元数据。这里只告诉模型“有什么服务”，
// 不提前连接服务，也不把远端工具定义塞进每一轮请求。
type ServerInfo struct {
	ID          string
	Name        string
	Description string
}

// Loader 管理一轮 Agent 任务中的 MCP 连接。每轮创建、每轮关闭，不保存跨任务状态。
// 模型先按任务语义搜索一个 MCP，Loader 再只注册少量匹配工具。
type Loader struct {
	configs     map[string]store.MCPConfig
	connections map[string]*Connection
	loaded      map[string]map[string]bool
	environment *appenv.Environment
	register    func([]agent.Tool) error
}

func NewLoader(environment *appenv.Environment, configs []store.MCPConfig) *Loader {
	loader := &Loader{
		configs:     make(map[string]store.MCPConfig),
		connections: make(map[string]*Connection),
		loaded:      make(map[string]map[string]bool),
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

// Preload 只服务于用户明确选择的 @mcp:id。小型 MCP 可以直接进入首轮，
// 避免再消耗一次 search_mcp_tools；大型 MCP 继续走语义搜索，避免 Schema
// 把上下文窗口塞满。普通自然语言任务不会在这里提前连接外部服务。
func (loader *Loader) Preload(ctx context.Context, ids []string) ([]agent.Tool, error) {
	if loader == nil {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(ids))
	result := make([]agent.Tool, 0)
	for _, rawID := range ids {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		config, ok := loader.configs[id]
		if !ok {
			return nil, fmt.Errorf("MCP %q 不存在或未启用", id)
		}
		connection, err := loader.connection(ctx, id, config)
		if err != nil {
			return nil, err
		}
		if len(connection.Tools) > directMCPToolLimit || toolSchemaBytes(connection.Tools) > directMCPSchemaSize {
			continue
		}
		if loader.loaded[id] == nil {
			loader.loaded[id] = make(map[string]bool)
		}
		for _, tool := range connection.Tools {
			if loader.loaded[id][tool.Spec.Name] {
				continue
			}
			loader.loaded[id][tool.Spec.Name] = true
			result = append(result, tool)
		}
	}
	return result, nil
}

// Tool 返回唯一常驻的 MCP 入口。它只加载本次任务最相关的少量工具，避免一个
// 大型 MCP 的全部 JSON Schema 占满小模型上下文。
func (loader *Loader) Tool() agent.Tool {
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name:        "search_mcp_tools",
			Description: "在一个已启用的 MCP Server 中按任务语义搜索工具，并让最相关的少量工具在下一步可用。query 应描述要执行的操作；如果服务使用英文工具名，优先使用简短英文操作词。不要无目的搜索全部 MCP。",
			Loader:      true,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":    map[string]any{"type": "string", "description": "已启用的 MCP Server ID"},
					"query": map[string]any{"type": "string", "description": "当前任务需要的操作，例如 navigate URL and inspect page"},
					"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": maxMCPToolLimit, "description": "最多加载几个工具，默认 5"},
				},
				"required":             []string{"id", "query"},
				"additionalProperties": false,
			},
		},
		Run: loader.search,
	}
}

func (loader *Loader) search(ctx context.Context, raw json.RawMessage) (string, error) {
	if loader == nil || loader.register == nil {
		return "", errors.New("MCP Loader 尚未初始化")
	}
	var input struct {
		ID    string `json:"id"`
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("MCP 搜索参数错误: %w", err)
	}
	id := strings.TrimSpace(input.ID)
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return "", errors.New("MCP 搜索缺少 query")
	}
	config, ok := loader.configs[id]
	if !ok {
		ids := make([]string, 0, len(loader.configs))
		for key := range loader.configs {
			ids = append(ids, key)
		}
		sort.Strings(ids)
		return "", fmt.Errorf("MCP %q 不存在或未启用，可用 MCP: %v", id, ids)
	}
	limit := input.Limit
	if limit <= 0 {
		limit = defaultMCPToolLimit
	}
	if limit > maxMCPToolLimit {
		limit = maxMCPToolLimit
	}

	connection, err := loader.connection(ctx, id, config)
	if err != nil {
		return "", err
	}
	matches := searchTools(connection.Tools, query, limit)
	if len(matches) == 0 {
		output := noMatchResult(config, query, connection.Info)
		return output, &agent.ToolError{
			Code: "no_mcp_tool_match", Message: "没有找到与 query 匹配的 MCP 工具",
			Hint: "根据返回的候选工具名改用更具体的操作词再次搜索", Retryable: false,
		}
	}

	if loader.loaded[id] == nil {
		loader.loaded[id] = make(map[string]bool)
	}
	newTools := make([]agent.Tool, 0, len(matches))
	for _, tool := range matches {
		if !loader.loaded[id][tool.Spec.Name] {
			newTools = append(newTools, tool)
		}
	}
	if len(newTools) > 0 {
		if err := loader.register(newTools); err != nil {
			return "", err
		}
		for _, tool := range newTools {
			loader.loaded[id][tool.Spec.Name] = true
		}
	}
	return searchResult(config, query, matches, len(newTools)), nil
}

func (loader *Loader) connection(ctx context.Context, id string, config store.MCPConfig) (*Connection, error) {
	if current := loader.connections[id]; current != nil {
		return current, nil
	}
	connection, err := Connect(ctx, loader.environment, config)
	if err != nil {
		return nil, err
	}
	loader.connections[id] = connection
	return connection, nil
}

func toolSchemaBytes(tools []agent.Tool) int {
	specs := make([]agent.ToolSpec, 0, len(tools))
	for _, tool := range tools {
		specs = append(specs, tool.Spec)
	}
	data, err := json.Marshal(specs)
	if err != nil {
		return directMCPSchemaSize + 1
	}
	return len(data)
}

type scoredTool struct {
	tool  agent.Tool
	score int
}

// searchTools 只做通用的工具元数据检索，不判断任务类型，也不包含 GitHub、
// Playwright 等产品规则。真正要搜索什么由模型通过 query 决定。
func searchTools(tools []agent.Tool, query string, limit int) []agent.Tool {
	terms := searchTerms(query)
	if len(terms) == 0 || limit <= 0 {
		return nil
	}
	scored := make([]scoredTool, 0, len(tools))
	for _, tool := range tools {
		score := scoreTool(tool.Spec, terms)
		if score > 0 {
			scored = append(scored, scoredTool{tool: tool, score: score})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].tool.Spec.Name < scored[j].tool.Spec.Name
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	result := make([]agent.Tool, 0, len(scored))
	for _, match := range scored {
		result = append(result, match.tool)
	}
	return result
}

func scoreTool(spec agent.ToolSpec, terms []string) int {
	name := strings.ToLower(spec.Name)
	nameTokens := tokenSet(name)
	description := strings.ToLower(spec.Description)
	descriptionTokens := tokenSet(description)
	schema, _ := json.Marshal(spec.Parameters)
	parameters := strings.ToLower(string(schema))
	score := 0
	for _, term := range terms {
		if nameTokens[term] {
			score += 12
		} else if strings.Contains(name, term) {
			score += 8
		}
		if descriptionTokens[term] {
			score += 4
		} else if strings.Contains(description, term) {
			score += 2
		}
		if strings.Contains(parameters, term) {
			score++
		}
	}
	return score
}

var ignoredSearchTerms = map[string]bool{
	"a": true, "an": true, "and": true, "for": true, "in": true, "of": true,
	"on": true, "the": true, "to": true, "tool": true, "use": true, "with": true,
}

func searchTerms(value string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)
	for _, term := range splitSearchText(value) {
		if len([]rune(term)) < 2 || ignoredSearchTerms[term] || seen[term] {
			continue
		}
		seen[term] = true
		result = append(result, term)
	}
	return result
}

func tokenSet(value string) map[string]bool {
	result := make(map[string]bool)
	for _, token := range splitSearchText(value) {
		result[token] = true
	}
	return result
}

func splitSearchText(value string) []string {
	return strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

func searchResult(config store.MCPConfig, query string, matches []agent.Tool, newlyAvailable int) string {
	tools := make([]ToolInfo, 0, len(matches))
	for _, tool := range matches {
		tools = append(tools, ToolInfo{Name: tool.Spec.Name, Description: tool.Spec.Description})
	}
	result := struct {
		ID             string     `json:"id"`
		Name           string     `json:"name"`
		Query          string     `json:"query"`
		NewlyAvailable int        `json:"newlyAvailable"`
		Tools          []ToolInfo `json:"tools"`
	}{ID: config.ID, Name: config.Name, Query: query, NewlyAvailable: newlyAvailable, Tools: tools}
	data, _ := json.Marshal(result)
	return string(data)
}

func noMatchResult(config store.MCPConfig, query string, tools []ToolInfo) string {
	suggestions := append([]ToolInfo(nil), tools...)
	sort.Slice(suggestions, func(i, j int) bool { return suggestions[i].Name < suggestions[j].Name })
	if len(suggestions) > 12 {
		suggestions = suggestions[:12]
	}
	result := struct {
		OK          bool       `json:"ok"`
		ID          string     `json:"id"`
		Query       string     `json:"query"`
		Suggestions []ToolInfo `json:"suggestions"`
	}{OK: false, ID: config.ID, Query: query, Suggestions: suggestions}
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
