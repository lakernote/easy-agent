package store

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// UsageAggregate 是按时间桶和模型聚合的实际运行用量。数据来自已落库的
// model_end/codex_usage/codex_end/tool_end/codex_item 事件，不根据会话标题或页面轮询结果估算。
type UsageAggregate struct {
	PeriodStart      time.Time `json:"periodStart"`
	Runtime          string    `json:"runtime"`
	Model            string    `json:"model"`
	ProfileID        string    `json:"profileId,omitempty"`
	Sessions         int       `json:"sessions"`
	InputTokens      int       `json:"inputTokens"`
	OutputTokens     int       `json:"outputTokens"`
	CachedTokens     int       `json:"cachedTokens"`
	CacheWriteTokens int       `json:"cacheWriteTokens"`
	TotalTokens      int       `json:"totalTokens"`
	ModelCalls       int       `json:"modelCalls"`
	ToolCalls        int       `json:"toolCalls"`
	ModelDurationMS  int64     `json:"modelDurationMs"`
	ToolDurationMS   int64     `json:"toolDurationMs"`
	CacheReported    bool      `json:"cacheReported"`
}

type usageAggregateKey struct {
	PeriodStart string
	Runtime     string
	Model       string
	ProfileID   string
}

// UsageAggregates 读取指定时间范围内的真实运行事件，并按 day/week/month 聚合。
// 事件表有 session_id 索引，查询只取事件 JSON，不会把消息和 Trace 全量载入内存。
func (store *Store) UsageAggregates(period string, since, until time.Time) ([]UsageAggregate, error) {
	period = strings.ToLower(strings.TrimSpace(period))
	if period != "day" && period != "week" && period != "month" {
		period = "day"
	}
	rows, err := store.db.Query(`
SELECT e.session_id, e.event_json, e.created_at, s.runtime, s.model, s.profile_id
FROM ea_events e JOIN ea_sessions s ON s.id=e.session_id
WHERE e.created_at >= ? AND e.created_at < ?
ORDER BY e.created_at`, formatTime(since), formatTime(until))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type usageEventRow struct {
		sessionID    string
		createdAt    time.Time
		runtime      string
		sessionModel string
		profileID    string
		event        Event
	}
	eventRows := make([]usageEventRow, 0, 128)
	sessionModels := map[string]string{}
	codexUsageTurns := map[string]struct{}{}
	for rows.Next() {
		var sessionID, created, runtime, sessionModel, profileID string
		var data []byte
		if err := rows.Scan(&sessionID, &data, &created, &runtime, &sessionModel, &profileID); err != nil {
			return nil, err
		}
		var event Event
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, err
		}
		createdAt, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			continue
		}
		eventRows = append(eventRows, usageEventRow{sessionID: sessionID, createdAt: createdAt, runtime: runtime, sessionModel: sessionModel, profileID: profileID, event: event})
		if model := usageEventModel(event); strings.TrimSpace(sessionModel) == "" && model != "" {
			sessionModels[sessionID] = model
		}
		if event.Kind == "codex_usage" {
			codexUsageTurns[usageTurnKey(sessionID, event.Turn)] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	type bucket struct {
		UsageAggregate
		sessions map[string]struct{}
	}
	buckets := map[usageAggregateKey]*bucket{}
	for _, row := range eventRows {
		event := row.event
		if !isUsageEvent(event.Kind) {
			continue
		}
		localTime := row.createdAt.In(time.Local)
		start := usagePeriodStart(localTime, period)
		model := strings.TrimSpace(row.sessionModel)
		if model == "" {
			model = sessionModels[row.sessionID]
		}
		if model == "" {
			model = "默认模型"
		}
		key := usageAggregateKey{PeriodStart: formatTime(start), Runtime: row.runtime, Model: model, ProfileID: row.profileID}
		item := buckets[key]
		if item == nil {
			item = &bucket{UsageAggregate: UsageAggregate{PeriodStart: start, Runtime: row.runtime, Model: model, ProfileID: row.profileID}, sessions: map[string]struct{}{}}
			buckets[key] = item
		}
		item.sessions[row.sessionID] = struct{}{}
		item.Sessions = len(item.sessions)
		switch event.Kind {
		case "model_end", "compaction_end":
			item.ModelCalls++
			item.InputTokens += event.InputTokens
			item.OutputTokens += event.OutputTokens
			item.CachedTokens += event.CachedTokens
			item.CacheWriteTokens += event.CacheWriteTokens
			item.TotalTokens += event.TotalTokens
			item.ModelDurationMS += event.DurationMS
			item.CacheReported = item.CacheReported || event.CacheReported
		case "codex_usage":
			item.ModelCalls++
			item.InputTokens += event.InputTokens
			item.OutputTokens += event.OutputTokens
			item.CachedTokens += event.CachedTokens
			item.CacheWriteTokens += event.CacheWriteTokens
			item.TotalTokens += event.TotalTokens
			item.CacheReported = item.CacheReported || event.CacheReported
		case "codex_end":
			item.ModelDurationMS += event.DurationMS
			if event.Status == "success" {
				if _, reported := codexUsageTurns[usageTurnKey(row.sessionID, event.Turn)]; !reported {
					item.ModelCalls++
				}
			}
		case "tool_end":
			if isBusinessToolEvent(event) {
				item.ToolCalls++
				item.ToolDurationMS += event.DurationMS
			}
		case "codex_item":
			if event.Status != "started" && (event.ActivityKind == "tool" || event.ActivityKind == "mcp") {
				item.ToolCalls++
				item.ToolDurationMS += event.DurationMS
			}
		}
	}
	result := make([]UsageAggregate, 0, len(buckets))
	for _, item := range buckets {
		result = append(result, item.UsageAggregate)
	}
	sortUsageAggregates(result)
	return result, nil
}

func isBusinessToolEvent(event Event) bool {
	switch strings.TrimSpace(event.ActivityKind) {
	case "loader", "skill", "mcp_loader", "mcp_selected":
		return false
	case "tool", "mcp":
		return true
	}
	switch strings.TrimSpace(event.Name) {
	case "load_tools", "load_skill", "search_mcp_tools":
		return false
	default:
		return true
	}
}

func isUsageEvent(kind string) bool {
	return kind == "model_end" || kind == "compaction_end" || kind == "codex_usage" || kind == "codex_end" || kind == "tool_end" || kind == "codex_item"
}

func usageEventModel(event Event) string {
	switch event.Kind {
	case "model_end", "compaction_end", "codex_usage", "codex_end":
		return strings.TrimSpace(event.Name)
	default:
		return ""
	}
}

func usageTurnKey(sessionID string, turn int) string {
	return sessionID + ":" + strconv.Itoa(turn)
}

func usagePeriodStart(value time.Time, period string) time.Time {
	date := time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, value.Location())
	switch period {
	case "week":
		daysSinceMonday := (int(date.Weekday()) + 6) % 7
		return date.AddDate(0, 0, -daysSinceMonday)
	case "month":
		return time.Date(value.Year(), value.Month(), 1, 0, 0, 0, 0, value.Location())
	default:
		return date
	}
}

func sortUsageAggregates(values []UsageAggregate) {
	for i := 1; i < len(values); i++ {
		current := values[i]
		j := i - 1
		for j >= 0 && (values[j].PeriodStart.After(current.PeriodStart) || values[j].PeriodStart.Equal(current.PeriodStart) && values[j].Model > current.Model) {
			values[j+1] = values[j]
			j--
		}
		values[j+1] = current
	}
}
