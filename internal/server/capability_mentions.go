package server

import (
	"regexp"
	"strings"

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

func latestUserMessage(messages []store.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "user" {
			return messages[index].Content
		}
	}
	return ""
}
