package skills

import "testing"

func TestCatalogLoadsIndependentSkillFiles(t *testing.T) {
	catalog, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 5 || catalog[0].Name == "" || catalog[4].Content == "" {
		t.Fatalf("内置 Skill 加载错误: %+v", catalog)
	}
	foundAPI := false
	for _, skill := range catalog {
		if skill.Name == "general-assistant" {
			t.Fatal("通用回答属于基础 Prompt，不应重复成为 Skill")
		}
		foundAPI = foundAPI || skill.Name == "api-design"
	}
	if !foundAPI {
		t.Fatal("缺少高频 API 设计 Skill")
	}
}
