package server

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var automationCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func parseAutomationCron(expression string) (cron.Schedule, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return nil, errors.New("请输入 Cron 表达式")
	}
	schedule, err := automationCronParser.Parse(expression)
	if err != nil {
		return nil, fmt.Errorf("Cron 表达式无效：%w（格式为：分 时 日 月 周）", err)
	}
	return schedule, nil
}

func nextAutomationRun(after time.Time, expression string) (time.Time, error) {
	schedule, err := parseAutomationCron(expression)
	if err != nil {
		return time.Time{}, err
	}
	next := schedule.Next(after)
	if next.IsZero() {
		return time.Time{}, errors.New("Cron 表达式没有可执行的下一次时间")
	}
	return next, nil
}
