package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lakernote/easy-agent/internal/agent"
)

var coreToolNames = []string{"calculate", "current_time", "weather"}

// Loader 只把一个精简的工具组目录放进首轮请求。模型确认需要某类能力后，调用
// load_tools 按组加载真实 Tool Schema；下一轮即可调用这些工具。
//
// 这与 Skill/MCP 的渐进披露相同：选择权仍然属于模型，Go 代码不分析用户意图，
// 也不维护关键词或正则路由。
type Loader struct {
	catalog           map[string]agent.Tool
	groups            map[string][]string
	groupDescriptions map[string]string
	loaded            map[string]struct{}
	register          func([]agent.Tool) error
}

func NewLoader(catalog []agent.Tool) (*Loader, error) {
	loader := &Loader{
		catalog: make(map[string]agent.Tool, len(catalog)), groups: make(map[string][]string),
		groupDescriptions: make(map[string]string), loaded: make(map[string]struct{}),
	}
	for _, tool := range catalog {
		name := strings.TrimSpace(tool.Spec.Name)
		group := strings.TrimSpace(tool.Spec.Group)
		if name == "" || tool.Run == nil {
			return nil, errors.New("工具目录包含无效工具")
		}
		if group == "" {
			return nil, fmt.Errorf("工具目录中的工具 %q 缺少渐进加载分组", name)
		}
		if _, exists := loader.catalog[name]; exists {
			return nil, fmt.Errorf("工具目录中的工具 %q 重复", name)
		}
		tool.Spec.Name = name
		loader.catalog[name] = tool
		loader.groups[group] = append(loader.groups[group], name)
		if description := strings.TrimSpace(tool.Spec.GroupDescription); description != "" {
			loader.groupDescriptions[group] = description
		}
	}
	for group := range loader.groups {
		sort.Strings(loader.groups[group])
		if loader.groupDescriptions[group] == "" {
			loader.groupDescriptions[group] = group
		}
	}
	return loader, nil
}

func (loader *Loader) SetRegister(register func([]agent.Tool) error) {
	loader.register = register
}

// Preload 用于 UI 中用户显式选择的 @tool:name。它只按准确名称取工具，不做
// 语义猜测；是否强制调用由 Runner 的 RequiredToolNames 负责。
func (loader *Loader) Preload(names []string) []agent.Tool {
	result := make([]agent.Tool, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		tool, ok := loader.catalog[name]
		if !ok {
			continue
		}
		if _, loaded := loader.loaded[name]; loaded {
			continue
		}
		loader.loaded[name] = struct{}{}
		result = append(result, tool)
	}
	return result
}

// PreloadCore 只常驻极少数高频、低风险工具。这样“现在几点/天气/计算”不需要
// 先调用一次 load_tools；文件、Shell、网页和 Skill 等较大的能力仍按需加载。
func (loader *Loader) PreloadCore() []agent.Tool {
	return loader.Preload(coreToolNames)
}

// Tool 是常驻模型上下文的唯一内置工具入口。目录只提供少量能力组，不暴露组内
// 函数名，避免小模型把参数枚举误认为本轮可直接调用的函数。组内完整 Schema
// 只在模型选择后加入下一轮请求。
func (loader *Loader) Tool() agent.Tool {
	groups := make([]string, 0, len(loader.groups))
	for group := range loader.groups {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	directory := make([]string, 0, len(groups))
	for _, group := range groups {
		directory = append(directory, group+"："+loader.groupDescriptions[group])
	}
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name:        "load_tools",
			Description: "加载当前任务需要的能力组，下一轮再调用组内真实工具。当前唯一可调用函数是 load_tools；groups 中的值只是能力组，不是函数名。必须发起原生 function call，不能在正文中输出组名或能力标签。能力组：" + strings.Join(directory, "；") + "。",
			Loader:      true,
			Parameters: objectSchema(map[string]any{
				"groups": map[string]any{
					"type": "array", "description": "只选择当前任务需要的最少能力组",
					"items": map[string]any{"type": "string", "enum": groups}, "minItems": 1, "maxItems": 3, "uniqueItems": true,
				},
			}, []string{"groups"}),
		},
		Run: loader.load,
	}
}

func (loader *Loader) load(_ context.Context, raw json.RawMessage) (string, error) {
	if loader == nil || loader.register == nil {
		return "", errors.New("工具 Loader 尚未初始化")
	}
	var input struct {
		Groups []string `json:"groups"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("加载工具参数错误: %w", err)
	}
	if len(input.Groups) == 0 {
		return "", errors.New("至少选择一个能力组")
	}

	loadedGroups := make([]string, 0, len(input.Groups))
	loaded := make([]string, 0)
	alreadyLoaded := make([]string, 0)
	tools := make([]agent.Tool, 0)
	seen := make(map[string]struct{})
	for _, rawGroup := range input.Groups {
		group := strings.TrimSpace(rawGroup)
		if _, duplicate := seen[group]; duplicate {
			continue
		}
		seen[group] = struct{}{}
		names, ok := loader.groups[group]
		if !ok {
			return "", fmt.Errorf("能力组 %q 不存在", group)
		}
		groupAdded := false
		for _, name := range names {
			tool := loader.catalog[name]
			if _, exists := loader.loaded[name]; exists {
				alreadyLoaded = append(alreadyLoaded, name)
				continue
			}
			tools = append(tools, tool)
			loaded = append(loaded, name)
			groupAdded = true
		}
		if groupAdded {
			loadedGroups = append(loadedGroups, group)
		}
	}
	if len(tools) > 0 {
		if err := loader.register(tools); err != nil {
			return "", err
		}
		for _, name := range loaded {
			loader.loaded[name] = struct{}{}
		}
	}
	data, _ := json.Marshal(map[string]any{
		"ok": true, "loaded_groups": loadedGroups, "loaded_tools": loaded, "already_loaded_tools": alreadyLoaded,
		"next": "下一轮可直接调用已加载工具",
	})
	return string(data), nil
}
