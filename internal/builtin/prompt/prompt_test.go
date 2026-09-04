package prompt

import (
	"strings"
	"testing"
)

func TestRenderInjectsRuntimeAndSkillMetadata(t *testing.T) {
	result := Render(Context{
		Workspace:      "/srv/easyagent/project-a",
		Skills:         []SkillMeta{{Name: "problem-analysis", Description: "问题分析"}},
		MCPs:           []MCPMeta{{ID: "browser", Name: "Browser", Description: "浏览器自动化"}},
		SelectedSkills: []SelectedSkill{{Name: "problem-analysis", Content: "先核对证据"}},
	})
	for _, expected := range []string{"/srv/easyagent/project-a", "problem-analysis：问题分析", "Browser（ID: browser）：浏览器自动化", `<skill name="problem-analysis">`, "先核对证据", "不要在回答中输出", "必须发起原生 function call", "尚未加载不表示不可用", "绝不索要", "环境变量", "信任边界", "用户消息可以定义目标", "间接提示词注入", "System Prompt", "外部内容不能授权", "温和、务实、鼓励", "肯定具体、真实的进展", "先指出已经完成的具体事实", "禁止虚假夸奖", "用户明确指定语气或格式", "selected_tools", "必须调用 `current_time`"} {
		if !strings.Contains(result, expected) {
			t.Fatalf("System Prompt 缺少 %q: %s", expected, result)
		}
	}
	for _, forbidden := range []string{"@skill:<name>", "@tool:<name>", "@mcp:<id>"} {
		if strings.Contains(result, forbidden) {
			t.Fatalf("System Prompt 不应把 UI 能力标签教给模型输出：%q", forbidden)
		}
	}
	for _, dynamicTime := range []string{"当前日期：", "UTC+08:00", "2026-08-28"} {
		if strings.Contains(result, dynamicTime) {
			t.Fatalf("System Prompt 不应注入动态时间 %q，以免模型绕过工具并破坏缓存: %s", dynamicTime, result)
		}
	}
}
