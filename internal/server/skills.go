package server

import (
	"sort"

	builtinskills "github.com/lakernote/easy-agent/internal/builtin/skills"
	builtintools "github.com/lakernote/easy-agent/internal/builtin/tools"
	"github.com/lakernote/easy-agent/internal/store"
)

// skillCatalog 合并“编译进二进制的默认 Skill”和“SQLite 中的用户覆盖”。
// 默认文件始终保留，所以用户点恢复默认时只需删除覆盖记录。
type skillCatalog struct {
	items []store.SkillOverride
}

func loadSkillCatalog(database *store.Store) (*skillCatalog, error) {
	builtin, err := builtinskills.Catalog()
	if err != nil {
		return nil, err
	}
	overrides, err := database.ListSkillOverrides()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]store.SkillOverride, len(builtin)+len(overrides))
	for _, value := range builtin {
		byName[value.Name] = store.SkillOverride{
			Name: value.Name, Description: value.Description, Content: value.Content,
			Enabled: value.Enabled, Builtin: true,
		}
	}
	for _, value := range overrides {
		if original, ok := byName[value.Name]; ok {
			value.Builtin = original.Builtin
		}
		byName[value.Name] = value
	}
	items := make([]store.SkillOverride, 0, len(byName))
	for _, value := range byName {
		items = append(items, value)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return &skillCatalog{items: items}, nil
}

func (catalog *skillCatalog) All() []store.SkillOverride {
	if catalog == nil {
		return []store.SkillOverride{}
	}
	return append([]store.SkillOverride(nil), catalog.items...)
}

func (catalog *skillCatalog) EnabledSkills() []builtintools.Skill {
	result := make([]builtintools.Skill, 0)
	for _, value := range catalog.items {
		if value.Enabled {
			result = append(result, builtintools.Skill{Name: value.Name, Description: value.Description, Content: value.Content})
		}
	}
	return result
}

func (catalog *skillCatalog) Skill(name string) (builtintools.Skill, bool) {
	for _, value := range catalog.items {
		if value.Enabled && value.Name == name {
			return builtintools.Skill{Name: value.Name, Description: value.Description, Content: value.Content}, true
		}
	}
	return builtintools.Skill{}, false
}
