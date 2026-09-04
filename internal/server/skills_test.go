package server

import (
	"testing"

	"github.com/lakernote/easy-agent/internal/store"
)

func TestCoreSkillsFollowDevelopmentWorkflow(t *testing.T) {
	want := []string{
		"problem-analysis",
		"code-review",
		"api-design",
		"test-and-e2e",
		"browser-validation",
		"incident-rca",
		"release-engineering",
		"docs-maintenance",
		"git-worktree-workflow",
		"web-research",
	}
	items := []store.SkillOverride{{Name: "z-custom"}}
	for index := len(want) - 1; index >= 0; index-- {
		items = append(items, store.SkillOverride{Name: want[index]})
	}
	items = append(items, store.SkillOverride{Name: "a-custom"})
	sortSkillOverrides(items)
	for index, name := range want {
		if items[index].Name != name {
			t.Fatalf("核心 Skill 顺序错误: got %q at %d, want %q", items[index].Name, index, name)
		}
	}
	if items[len(want)].Name != "a-custom" || items[len(want)+1].Name != "z-custom" {
		t.Fatalf("自定义 Skill 应排在核心能力之后并按名称排序: %+v", items)
	}
}
