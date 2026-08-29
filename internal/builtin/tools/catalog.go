// Package tools 提供 EasyAgent 无需安装即可使用的最小工具集。
// 每个工具是普通 Go 函数；MCP 工具会在运行时追加到同一个 []agent.Tool。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/lakernote/easy-agent/internal/agent"
)

type SkillSource interface {
	EnabledSkills() []Skill
	Skill(name string) (Skill, bool)
}

type Skill struct {
	Name        string
	Description string
	Content     string
}

type Info struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Category    string `json:"category"`
}

func Catalog(skills SkillSource) []agent.Tool {
	// 文件工具共享同一个工作区和“已读取版本”记录。这样 write 可以阻止模型在
	// 没看过现有文件时直接覆盖，同时整套能力仍然只属于本轮 Agent。
	files := newFileWorkspace()
	result := []agent.Tool{currentTimeTool(), weatherTool(), calculateTool(), webSearchTool()}
	result = append(result, files.tools()...)
	result = append(result, shellTool())
	if skills != nil {
		result = append(result, loadSkillTool(skills))
	}
	return result
}

func InfoList(skills SkillSource) []Info {
	tools := Catalog(skills)
	result := make([]Info, 0, len(tools))
	for _, tool := range tools {
		result = append(result, Info{Name: tool.Spec.Name, Description: tool.Spec.Description, Source: "内置", Category: toolCategory(tool.Spec.Name)})
	}
	return result
}

func toolCategory(name string) string {
	switch name {
	case "read", "grep", "find", "ls", "edit", "write":
		return "文件"
	case "shell", "calculate":
		return "执行"
	case "current_time", "weather", "web_search":
		return "信息"
	case "load_skill":
		return "扩展"
	default:
		return "内置"
	}
}

func loadSkillTool(source SkillSource) agent.Tool {
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name: "load_skill", Description: "按名称加载一个相关 Skill 的完整说明；不要无目的加载全部 Skill。",
			Parameters: objectSchema(map[string]any{"name": stringSchema("Skill 名称")}, []string{"name"}),
		},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var arguments struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(raw, &arguments); err != nil {
				return "", fmt.Errorf("Skill 参数错误: %w", err)
			}
			skill, ok := source.Skill(arguments.Name)
			if !ok {
				names := make([]string, 0)
				for _, item := range source.EnabledSkills() {
					names = append(names, item.Name)
				}
				sort.Strings(names)
				return "", fmt.Errorf("Skill %q 不存在或未启用，可用 Skills: %v", arguments.Name, names)
			}
			return skill.Content, nil
		},
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	result := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}
