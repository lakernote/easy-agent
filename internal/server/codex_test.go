package server

import (
	"testing"

	"github.com/lakernote/easy-agent/internal/codexruntime"
	"github.com/lakernote/easy-agent/internal/store"
)

func TestCodexActivityResolverDoesNotGuessBetweenConcurrentIdenticalCalls(t *testing.T) {
	resolver := newCodexActivityResolver()
	event := codexruntime.Event{Kind: "codex_item", Status: "started", Name: "shell", Input: `{"cmd":"same"}`}
	first := resolver.resolve(event)
	second := resolver.resolve(event)
	terminal := resolver.resolve(codexruntime.Event{Kind: "codex_item", Status: "success", Name: event.Name, Input: event.Input})
	if first.ID == second.ID || terminal.ID == first.ID || terminal.ID == second.ID {
		t.Fatalf("ambiguous calls were paired: first=%+v second=%+v terminal=%+v", first, second, terminal)
	}
	if terminal.Guarantee != store.ToolGuaranteeUncorrelated {
		t.Fatalf("ambiguous terminal guarantee = %q", terminal.Guarantee)
	}
}

func TestCodexActivityResolverPairsOnlyUnambiguousFallback(t *testing.T) {
	resolver := newCodexActivityResolver()
	started := resolver.resolve(codexruntime.Event{Status: "started", Name: "shell", Input: `{"cmd":"one"}`})
	terminal := resolver.resolve(codexruntime.Event{Status: "success", Name: "shell", Input: `{"cmd":"one"}`})
	if terminal.ID != started.ID || terminal.Guarantee != store.ToolGuaranteeObserved {
		t.Fatalf("unambiguous call did not pair: started=%+v terminal=%+v", started, terminal)
	}
	stable := resolver.resolve(codexruntime.Event{ActivityID: "provider-id", Status: "success"})
	if stable.ID != "provider-id" || stable.Guarantee != store.ToolGuaranteeObserved {
		t.Fatalf("stable identity = %+v", stable)
	}
}
