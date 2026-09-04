package skills

import "testing"

func TestCatalogLoadsIndependentSkillFiles(t *testing.T) {
	catalog, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 11 {
		t.Fatalf("内置 Skill 加载错误: %+v", catalog)
	}
	want := map[string]bool{
		"project-onboarding":    false,
		"api-design":            false,
		"docs-maintenance":      false,
		"git-worktree-workflow": false,
		"incident-rca":          false,
		"release-engineering":   false,
		"test-and-e2e":          false,
	}
	for _, skill := range catalog {
		if skill.Name == "general-assistant" {
			t.Fatal("通用回答属于基础 Prompt，不应重复成为 Skill")
		}
		if skill.Name == "" || skill.Description == "" || skill.Content == "" {
			t.Fatalf("内置 Skill 字段不完整: %+v", skill)
		}
		if _, ok := want[skill.Name]; ok {
			want[skill.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("缺少内置 Skill %q", name)
		}
	}
}
