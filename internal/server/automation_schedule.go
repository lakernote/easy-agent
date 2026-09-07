package server

import (
	"errors"
	"fmt"
	"time"
)

const defaultAutomationScheduleTime = "10:00"

var supportedAutomationRepeats = map[string]struct{}{
	"interval": {},
	"daily":    {},
	"weekdays": {},
	"weekly":   {},
}

func normalizeAutomationSchedule(repeat, scheduleTime string, weekday int) (string, string, int, error) {
	if repeat == "" {
		repeat = "interval"
	}
	if _, ok := supportedAutomationRepeats[repeat]; !ok {
		return "", "", 0, errors.New("请选择有效的重复方式")
	}
	if repeat == "interval" {
		return repeat, "", 0, nil
	}
	if scheduleTime == "" {
		scheduleTime = defaultAutomationScheduleTime
	}
	if _, err := time.Parse("15:04", scheduleTime); err != nil {
		return "", "", 0, errors.New("请选择有效的执行时间")
	}
	if repeat == "weekly" {
		if weekday == 0 {
			weekday = 1
		}
		if weekday < 1 || weekday > 7 {
			return "", "", 0, errors.New("请选择有效的星期")
		}
	} else {
		weekday = 0
	}
	return repeat, scheduleTime, weekday, nil
}

func nextAutomationRun(after time.Time, repeat string, intervalMinutes, scheduleWeekday int, scheduleTime string) (time.Time, error) {
	if repeat == "interval" {
		if intervalMinutes <= 0 {
			return time.Time{}, errors.New("定时间隔必须大于 0")
		}
		return after.Add(time.Duration(intervalMinutes) * time.Minute), nil
	}
	repeat, scheduleTime, scheduleWeekday, err := normalizeAutomationSchedule(repeat, scheduleTime, scheduleWeekday)
	if err != nil {
		return time.Time{}, err
	}
	parsed, _ := time.Parse("15:04", scheduleTime)
	location := after.Location()
	for offset := 0; offset <= 8; offset++ {
		day := after.AddDate(0, 0, offset)
		candidate := time.Date(day.Year(), day.Month(), day.Day(), parsed.Hour(), parsed.Minute(), 0, 0, location)
		if !candidate.After(after) {
			continue
		}
		allowed := false
		switch repeat {
		case "daily":
			allowed = true
		case "weekdays":
			allowed = candidate.Weekday() >= time.Monday && candidate.Weekday() <= time.Friday
		case "weekly":
			candidateWeekday := int(candidate.Weekday())
			if candidateWeekday == 0 {
				candidateWeekday = 7
			}
			allowed = candidateWeekday == scheduleWeekday
		}
		if allowed {
			return candidate, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法计算下一次执行时间")
}
