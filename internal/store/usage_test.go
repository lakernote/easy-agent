package store

import (
	"testing"
	"time"
)

func TestUsageAggregatesByPeriodAndModel(t *testing.T) {
	database, err := Open(t.TempDir() + "/usage.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().Truncate(time.Second)
	if _, err := database.CreateSession(CreateSessionParams{ID: "s1", Title: "一", Runtime: RuntimeEasyAgent, ProfileID: "p1", Model: "qwen", CreatedAt: now.Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateSession(CreateSessionParams{ID: "s2", Title: "二", Runtime: RuntimeCodex, ProfileID: "p2", Model: "gpt-5", CreatedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []struct {
		session string
		event   Event
	}{
		{"s1", Event{Kind: "model_end", Name: "qwen", InputTokens: 100, OutputTokens: 20, TotalTokens: 120, DurationMS: 50, CreatedAt: now.Add(-90 * time.Minute)}},
		{"s1", Event{Kind: "tool_end", Name: "calculate", DurationMS: 12, CreatedAt: now.Add(-80 * time.Minute)}},
		{"s1", Event{Kind: "tool_end", Name: "load_tools", ActivityKind: "loader", DurationMS: 4, CreatedAt: now.Add(-79 * time.Minute)}},
		{"s1", Event{Kind: "tool_end", Name: "load_skill", DurationMS: 3, CreatedAt: now.Add(-78 * time.Minute)}},
		{"s2", Event{Kind: "codex_usage", Turn: 1, Name: "gpt-5", InputTokens: 300, OutputTokens: 40, TotalTokens: 340, CacheReported: true, CreatedAt: now.Add(-50 * time.Minute)}},
		{"s2", Event{Kind: "codex_item", Turn: 1, Name: "web_search", ActivityKind: "tool", Status: "started", CreatedAt: now.Add(-49 * time.Minute)}},
		{"s2", Event{Kind: "codex_item", Turn: 1, Name: "web_search", ActivityKind: "tool", Status: "success", DurationMS: 20, CreatedAt: now.Add(-48 * time.Minute)}},
		{"s2", Event{Kind: "codex_item", Turn: 1, Name: "plan", ActivityKind: "plan", Status: "success", DurationMS: 5, CreatedAt: now.Add(-47 * time.Minute)}},
		{"s2", Event{Kind: "codex_end", Turn: 1, Name: "gpt-5", Status: "success", DurationMS: 125, CreatedAt: now.Add(-46 * time.Minute)}},
	} {
		if err := database.AppendEvent(value.session, value.event); err != nil {
			t.Fatal(err)
		}
	}
	values, err := database.UsageAggregates("day", now.Add(-24*time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 {
		t.Fatalf("got %d daily buckets, want 2: %+v", len(values), values)
	}
	byModel := map[string]UsageAggregate{}
	for _, value := range values {
		byModel[value.Model] = value
	}
	if byModel["qwen"].InputTokens != 100 || byModel["qwen"].ToolCalls != 1 || byModel["qwen"].Sessions != 1 {
		t.Fatalf("unexpected EasyAgent aggregate: %+v", byModel["qwen"])
	}
	if byModel["gpt-5"].TotalTokens != 340 || byModel["gpt-5"].ModelCalls != 1 || byModel["gpt-5"].ToolCalls != 1 || byModel["gpt-5"].ModelDurationMS != 125 || byModel["gpt-5"].ToolDurationMS != 20 || !byModel["gpt-5"].CacheReported {
		t.Fatalf("unexpected Codex aggregate: %+v", byModel["gpt-5"])
	}
	weekly, err := database.UsageAggregates("week", now.Add(-24*time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(weekly) != 2 || !weekly[0].PeriodStart.Equal(weekly[1].PeriodStart) {
		t.Fatalf("week buckets should share the same Monday: %+v", weekly)
	}
}
