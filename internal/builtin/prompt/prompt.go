// Package prompt 管理 EasyAgent 唯一的基础 System Prompt。
//
// 基础行为放在 Markdown，而不是散落在 Go 字符串中；Go 代码只负责注入每轮
// 运行时事实和 Skill 元数据。
package prompt

import (
	_ "embed"
	"fmt"
	"strings"
	"time"
)

//go:embed system.md
var systemPrompt string

//go:embed compaction.md
var compactionPrompt string

// Template 返回编译进二进制的原始模板，供能力管理页面查看。
// 页面只能查看；修改基础原则应通过代码评审完成，任务方法和团队约定放到 Skill 中。
func Template() string { return strings.TrimSpace(systemPrompt) }

// CompactionTemplate 返回独立的检查点 Prompt。它不混入常规 Agent 规则，
// 避免摘要模型误以为自己要继续执行用户任务。
func CompactionTemplate() string { return strings.TrimSpace(compactionPrompt) }

type SkillMeta struct {
	Name        string
	Description string
}

type MCPMeta struct {
	ID          string
	Name        string
	Description string
}

type Context struct {
	Now    time.Time
	Skills []SkillMeta
	MCPs   []MCPMeta
}

// Render 生成一轮会话使用的 System Prompt。日期、星期和时区是稳定运行时事实；
// 精确到秒的时间仍由 current_time 工具读取。
func Render(context Context) string {
	now := context.Now
	if now.IsZero() {
		now = time.Now()
	}
	zone, offset := now.Zone()
	runtime := fmt.Sprintf("当前日期：%s；星期：%s；时区：%s（UTC%+03d:%02d）。精确当前时间请调用 current_time。",
		now.Format("2006-01-02"), weekday(now.Weekday()), zone, offset/3600, abs(offset%3600)/60)
	var skills strings.Builder
	if len(context.Skills) == 0 {
		skills.WriteString("- 无")
	} else {
		for _, skill := range context.Skills {
			fmt.Fprintf(&skills, "- %s：%s\n", skill.Name, skill.Description)
		}
	}
	result := strings.ReplaceAll(systemPrompt, "{{RUNTIME_CONTEXT}}", runtime)
	result = strings.ReplaceAll(result, "{{SKILLS}}", strings.TrimSpace(skills.String()))

	var mcps strings.Builder
	if len(context.MCPs) == 0 {
		mcps.WriteString("- 无")
	} else {
		for _, server := range context.MCPs {
			fmt.Fprintf(&mcps, "- %s（ID: %s）：%s\n", server.Name, server.ID, server.Description)
		}
	}
	result = strings.ReplaceAll(result, "{{MCPS}}", strings.TrimSpace(mcps.String()))
	return strings.TrimSpace(result)
}

func weekday(day time.Weekday) string {
	return [...]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}[day]
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
