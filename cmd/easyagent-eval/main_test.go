package main

import (
	"reflect"
	"testing"
	"time"
)

func TestSplitModelsTrimsAndDeduplicates(t *testing.T) {
	got := splitModels(" qwen3:14b ,qwen2.5:7b,qwen3:14b,, ")
	want := []string{"qwen3:14b", "qwen2.5:7b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitModels() = %#v, want %#v", got, want)
	}
}

func TestModelSummaryUsesAllRunsAndObservedFirstTokens(t *testing.T) {
	summary := modelSummary{
		Runs: 3, Passed: 2, totalInputTokens: 30, totalOutputTokens: 9,
		totalDuration: 9 * time.Second, totalFirstToken: 4 * time.Second, firstTokenObservations: 2,
	}
	summary.finish()
	if summary.PassRate != float64(2)/3 || summary.AverageInputTokens != 10 || summary.AverageOutputTokens != 3 || summary.AverageTotalTokens != 13 || summary.AverageDurationMS != 3000 || summary.AverageFirstTokenMS != 2000 {
		t.Fatalf("summary = %+v", summary)
	}
}
