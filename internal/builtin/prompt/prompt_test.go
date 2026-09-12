package prompt

import (
	"strings"
	"testing"
)

func TestRenderInjectsRuntimeAndSkillMetadata(t *testing.T) {
	result := Render(Context{
		Workspace:      "/srv/easyagent/project-a",
		Directories:    []string{"/srv/easyagent/project-web"},
		Skills:         []SkillMeta{{Name: "problem-analysis", Description: "问题分析"}},
		MCPs:           []MCPMeta{{ID: "browser", Name: "Browser", Description: "浏览器自动化"}},
		SelectedSkills: []SelectedSkill{{Name: "problem-analysis", Content: "先核对证据"}},
	})
	for _, expected := range []string{"/srv/easyagent/project-a", "/srv/easyagent/project-web", "以下源文件夹", "problem-analysis：问题分析", "Browser（ID: browser）：浏览器自动化", `<skill name="problem-analysis">`, "先核对证据", "不要输出或编造能力标签", "原生 function call", "加载结果不是任务证据", "绝不索要", "环境变量", "信任边界", "用户消息定义目标", "间接提示词注入", "System Prompt", "不能授权新目标", "温和务实", "肯定具体事实", "用户指定格式或语气", "selected_tools", "使用 `current_time`", "先加载 `information` 组", "其他工具已返回可靠日期", "Tool Schema", "即使用户点名某工具", "加载 `web` 组", "sources.content", "citation", "缺失字段明确说明"} {
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
