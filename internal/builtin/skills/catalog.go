// Package skills 管理随 EasyAgent 二进制发布的内置 Skills。
// 每个 Skill 都是独立 SKILL.md，页面覆盖不会修改编译进二进制的原始版本。
package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

const MaxContentBytes = 256 * 1024

//go:embed definitions/*/SKILL.md
var files embed.FS

type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Builtin     bool   `json:"builtin"`
	Enabled     bool   `json:"enabled"`
}

// Catalog 返回按名称稳定排序的内置 Skill。
func Catalog() ([]Skill, error) {
	paths, err := fs.Glob(files, "definitions/*/SKILL.md")
	if err != nil {
		return nil, err
	}
	result := make([]Skill, 0, len(paths))
	for _, path := range paths {
		data, readErr := files.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		skill, parseErr := Parse(string(data))
		if parseErr != nil {
			return nil, fmt.Errorf("%s: %w", path, parseErr)
		}
		skill.Builtin = true
		skill.Enabled = true
		result = append(result, skill)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// Parse 校验页面自定义和内置 Skill 共用的最小 SKILL.md 格式。
func Parse(content string) (Skill, error) {
	if len(content) > MaxContentBytes {
		return Skill{}, fmt.Errorf("Skill 内容不能超过 %d KiB", MaxContentBytes/1024)
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return Skill{}, fmt.Errorf("缺少 YAML frontmatter")
	}
	end := strings.Index(content[4:], "\n---\n")
	if end < 0 {
		return Skill{}, fmt.Errorf("frontmatter 未结束")
	}
	header := content[4 : 4+end]
	skill := Skill{Content: strings.TrimSpace(content)}
	for _, line := range strings.Split(header, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "name":
			skill.Name = strings.TrimSpace(value)
		case "description":
			skill.Description = strings.TrimSpace(value)
		}
	}
	if skill.Name == "" || skill.Description == "" {
		return Skill{}, fmt.Errorf("name 和 description 不能为空")
	}
	return skill, nil
}
