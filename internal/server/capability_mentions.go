package server

import (
	"regexp"
	"strings"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/builtin/prompt"
	"github.com/lakernote/easy-agent/internal/store"
)

var capabilityMentionPattern = regexp.MustCompile(`(?i)@(skill|tool|mcp):([a-z0-9][a-z0-9._-]*)`)

// selectedSkills 把用户在 UI 中明确 @ 的 Skill 注入本轮上下文。用户显式
// 选择属于输入组装，不是语义路由；没有选择时模型仍通过 load_skill 按需加载。
// 这样也不依赖不同 Provider 对“指定函数名 tool_choice”的兼容程度。
func selectedSkills(messages []store.Message, catalog *skillCatalog) []prompt.SelectedSkill {
	text := latestUserMessage(messages)
	matches := capabilityMentionPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := map[string]struct{}{}
	selected := make([]prompt.SelectedSkill, 0)
	for _, match := range matches {
		kind, name := strings.ToLower(match[1]), match[2]
		if kind != "skill" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		skill, ok := catalog.Skill(name)
		if ok {
			selected = append(selected, prompt.SelectedSkill{Name: skill.Name, Content: skill.Content})
			seen[name] = struct{}{}
		}
	}
	return selected
}

func selectedSkillNames(messages []store.Message) map[string]struct{} {
	matches := capabilityMentionPattern.FindAllStringSubmatch(latestUserMessage(messages), -1)
	result := make(map[string]struct{})
	for _, match := range matches {
		if strings.EqualFold(match[1], "skill") {
			result[match[2]] = struct{}{}
		}
	}
	return result
}

// selectedToolNames 返回 UI 通过 @tool:name 明确选择的工具。它不分析自然语言，
// 只把用户的显式选择转换成首轮预加载和首轮工具约束。
func selectedToolNames(messages []store.Message) []string {
	matches := capabilityMentionPattern.FindAllStringSubmatch(latestUserMessage(messages), -1)
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, match := range matches {
		if strings.ToLower(match[1]) != "tool" {
			continue
		}
		name := match[2]
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func stripToolMentions(text string) string {
	return strings.TrimSpace(capabilityMentionPattern.ReplaceAllStringFunc(text, func(value string) string {
		match := capabilityMentionPattern.FindStringSubmatch(value)
		if len(match) > 1 && strings.EqualFold(match[1], "tool") {
			return ""
		}
		return value
	}))
}

// historicalToolNames 恢复仍在当前协议历史中的内置工具 Schema。
// 否则模型能看到旧 function call，却在本轮 request.tools 中找不到
// 同名函数，一些 Provider 会直接拒绝生成。Loader 本身不是业务工具。
func historicalToolNames(messages []agent.Message) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, message := range messages {
		if message.Role != agent.RoleAssistant {
			continue
		}
		for _, call := range message.ToolCalls {
			name := strings.TrimSpace(call.Name)
			if name == "" || name == "load_tools" || name == "search_mcp_tools" {
				continue
			}
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			result = append(result, name)
		}
	}
	return result
}

// selectedMCPIDs 返回用户通过 @mcp:id 明确选择的小型 MCP。它只做精确选择，
// 不根据自然语言猜测应该连接哪个外部服务。
func selectedMCPIDs(messages []store.Message) []string {
	matches := capabilityMentionPattern.FindAllStringSubmatch(latestUserMessage(messages), -1)
	seen := make(map[string]struct{})
	var result []string
	for _, match := range matches {
		if strings.ToLower(match[1]) != "mcp" {
			continue
		}
		name := match[2]
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func latestUserMessage(messages []store.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "user" {
			return messages[index].Content
		}
	}
	return ""
}
