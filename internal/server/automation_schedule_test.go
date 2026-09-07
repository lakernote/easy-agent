package server

import (
	"testing"
	"time"
)

func TestNextAutomationRunWithCron(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	friday := time.Date(2026, time.March, 6, 11, 0, 0, 0, location)
	next, err := nextAutomationRun(friday, "0 10 * * 1-5")
	if err != nil {
		t.Fatalf("nextAutomationRun returned error: %v", err)
	}
	want := time.Date(2026, time.March, 9, 10, 0, 0, 0, location)
	if !next.Equal(want) {
		t.Fatalf("next run = %s, want %s", next, want)
	}

	quarter, err := nextAutomationRun(time.Date(2026, time.March, 6, 10, 7, 0, 0, location), "*/15 * * * *")
	if err != nil {
		t.Fatalf("quarter-hour cron returned error: %v", err)
	}
	wantQuarter := time.Date(2026, time.March, 6, 10, 15, 0, 0, location)
	if !quarter.Equal(wantQuarter) {
		t.Fatalf("quarter-hour next run = %s, want %s", quarter, wantQuarter)
	}
}

func TestParseAutomationCronRejectsInvalidExpression(t *testing.T) {
	if _, err := parseAutomationCron("not a cron"); err == nil {
		t.Fatal("expected invalid Cron expression to be rejected")
	}
}
