// Package tools 提供 EasyAgent 无需安装即可使用的最小工具集。
// 每个工具是普通 Go 函数；MCP 工具会在运行时追加到同一个 []agent.Tool。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/appenv"
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

// entry 把工具本身和仅用于页面展示的分类放在同一个注册点。
// Category 不参与模型决策，也不会发给模型；模型只看到 ToolSpec。
type entry struct {
	tool     agent.Tool
	category string
	group    string
}

const (
	categoryFile        = "文件"
	categoryExecution   = "执行"
	categoryInformation = "信息"
	categoryExtension   = "扩展"
	groupInformation    = "information"
	groupFiles          = "files"
	groupExecution      = "execution"
	groupWeb            = "web"
	groupSkills         = "skills"
)

var groupDescriptions = map[string]string{
	groupInformation: "日期、时间和天气",
	groupFiles:       "工作区文件的列出、查找、搜索、读取和修改",
	groupExecution:   "数学计算、Shell、构建、测试和 CLI",
	groupWeb:         "互联网搜索和已知网页读取",
	groupSkills:      "按需加载与任务相关的 Skill 方法",
}

func Catalog(environment *appenv.Environment, skills SkillSource) []agent.Tool {
	entries := catalogEntries(environment, skills)
	result := make([]agent.Tool, 0, len(entries))
	for _, item := range entries {
		tool := item.tool
		tool.Spec.Group = item.group
		tool.Spec.GroupDescription = groupDescriptions[item.group]
		tool.Spec.ActivityKind = "tool"
		tool.Spec.ActivitySource = "builtin"
		tool.Spec.DisplayName = tool.Spec.Name
		if tool.Spec.Name == "load_skill" {
			tool.Spec.ActivityKind = "skill"
			tool.Spec.DisplayName = "Skill"
		}
		result = append(result, tool)
	}
	return result
}

func catalogEntries(environment *appenv.Environment, skills SkillSource) []entry {
	// 文件工具共享同一个工作区和“已读取版本”记录。这样 write 可以阻止模型在
	// 没看过现有文件时直接覆盖，同时整套能力仍然只属于本轮 Agent。
	files := newFileWorkspace(environment.Workspace())
	result := []entry{
		{tool: currentTimeTool(), category: categoryInformation, group: groupInformation},
		{tool: weatherTool(), category: categoryInformation, group: groupInformation},
		{tool: calculateTool(), category: categoryExecution, group: groupExecution},
		{tool: webSearchTool(), category: categoryInformation, group: groupWeb},
		{tool: webFetchTool(), category: categoryInformation, group: groupWeb},
	}
	for _, tool := range files.tools() {
		result = append(result, entry{tool: tool, category: categoryFile, group: groupFiles})
	}
	result = append(result, entry{tool: shellTool(environment), category: categoryExecution, group: groupExecution})
	if skills != nil {
		result = append(result, entry{tool: loadSkillTool(skills), category: categoryExtension, group: groupSkills})
	}
	return result
}

func InfoList(environment *appenv.Environment, skills SkillSource) []Info {
	entries := catalogEntries(environment, skills)
	result := make([]Info, 0, len(entries))
	for _, item := range entries {
		result = append(result, Info{
			Name: item.tool.Spec.Name, Description: item.tool.Spec.Description,
			Source: "内置", Category: item.category,
		})
	}
	return result
}

func loadSkillTool(source SkillSource) agent.Tool {
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name: "load_skill", Description: "按名称加载一个与当前任务相关的 Skill 完整说明，并在后续步骤遵守它。只在元数据表明 Skill 适用时加载，不要批量加载或把加载本身当成完成。",
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
