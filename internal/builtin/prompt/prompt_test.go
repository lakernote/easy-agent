package prompt

import (
	"strings"
	"testing"
	"time"
)

func TestRenderInjectsRuntimeAndSkillMetadata(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Shanghai")
	result := Render(Context{
		Now:            time.Date(2026, 8, 28, 10, 0, 0, 0, location),
		Workspace:      "/srv/easyagent/project-a",
		Skills:         []SkillMeta{{Name: "problem-analysis", Description: "问题分析"}},
		MCPs:           []MCPMeta{{ID: "browser", Name: "Browser", Description: "浏览器自动化"}},
		SelectedSkills: []SelectedSkill{{Name: "problem-analysis", Content: "先核对证据"}},
	})
	for _, expected := range []string{"2026-08-28", "星期五", "UTC+08:00", "/srv/easyagent/project-a", "problem-analysis：问题分析", "Browser（ID: browser）：浏览器自动化", `<skill name="problem-analysis">`, "先核对证据", "@skill:<name>", "@tool:<name>", "@mcp:<id>", "尚未加载不表示不可用"} {
		if !strings.Contains(result, expected) {
			t.Fatalf("System Prompt 缺少 %q: %s", expected, result)
		}
	}
}
