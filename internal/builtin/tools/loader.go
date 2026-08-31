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

// Loader 只把一个精简的工具目录放进首轮请求。模型确认需要某项能力后，调用
// load_tools 按名称加载真实 Tool Schema；下一轮即可调用这些工具。
//
// 这与 Skill/MCP 的渐进披露相同：选择权仍然属于模型，Go 代码不分析用户意图，
// 也不维护关键词或正则路由。
type Loader struct {
	catalog  map[string]agent.Tool
	loaded   map[string]struct{}
	register func([]agent.Tool) error
}

func NewLoader(catalog []agent.Tool) (*Loader, error) {
	loader := &Loader{catalog: make(map[string]agent.Tool, len(catalog)), loaded: make(map[string]struct{})}
	for _, tool := range catalog {
		name := strings.TrimSpace(tool.Spec.Name)
		if name == "" || tool.Run == nil {
			return nil, errors.New("工具目录包含无效工具")
		}
		if _, exists := loader.catalog[name]; exists {
			return nil, fmt.Errorf("工具目录中的工具 %q 重复", name)
		}
		tool.Spec.Name = name
		loader.catalog[name] = tool
	}
	return loader, nil
}

func (loader *Loader) SetRegister(register func([]agent.Tool) error) {
	loader.register = register
}

// Preload 用于 UI 中用户显式选择的 @tool:name。它只按准确名称取工具，不做
// 语义猜测；普通自然语言任务仍由模型通过 load_tools 自主选择。
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

// Tool 是常驻模型上下文的唯一内置工具入口。目录只提供自解释的名称，不重复
// 每个工具较大的说明和 JSON Schema，因此小上下文模型也能先做正确选择。
func (loader *Loader) Tool() agent.Tool {
	names := make([]string, 0, len(loader.catalog))
	for name := range loader.catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name:        "load_tools",
			Description: "按名称加载当前任务需要的内置工具，下一轮再调用。尚未加载不表示不可用。例如用户要求 calculate 时先调用 {\"names\":[\"calculate\"]}；不能绕过工具猜答案。可选名称：" + strings.Join(names, ", ") + "。",
			Parameters: objectSchema(map[string]any{
				"names": map[string]any{
					"type": "array", "description": "要加载的工具名称；只选择当前任务需要的最少集合",
					"items": map[string]any{"type": "string", "enum": names}, "minItems": 1, "maxItems": 6, "uniqueItems": true,
				},
			}, []string{"names"}),
		},
		Run: loader.load,
	}
}

func (loader *Loader) load(_ context.Context, raw json.RawMessage) (string, error) {
	if loader == nil || loader.register == nil {
		return "", errors.New("工具 Loader 尚未初始化")
	}
	var input struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("加载工具参数错误: %w", err)
	}
	if len(input.Names) == 0 {
		return "", errors.New("至少选择一个工具")
	}

	loaded := make([]string, 0, len(input.Names))
	alreadyLoaded := make([]string, 0)
	tools := make([]agent.Tool, 0, len(input.Names))
	seen := make(map[string]struct{})
	for _, rawName := range input.Names {
		name := strings.TrimSpace(rawName)
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		tool, ok := loader.catalog[name]
		if !ok {
			return "", fmt.Errorf("内置工具 %q 不存在", name)
		}
		if _, exists := loader.loaded[name]; exists {
			alreadyLoaded = append(alreadyLoaded, name)
			continue
		}
		tools = append(tools, tool)
		loaded = append(loaded, name)
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
		"ok": true, "loaded": loaded, "already_loaded": alreadyLoaded,
		"next": "下一轮可直接调用已加载工具",
	})
	return string(data), nil
}
