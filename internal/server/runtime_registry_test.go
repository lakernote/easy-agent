package server

import (
	"testing"

	"github.com/lakernote/easy-agent/internal/store"
)

func TestRuntimeRegistryResolvesConfiguredRuntimes(t *testing.T) {
	registry := newRuntimeRegistry(&Server{})

	for _, name := range []string{store.RuntimeEasyAgent, store.RuntimeCodex} {
		executor, err := registry.resolve(name)
		if err != nil {
			t.Fatalf("resolve %q: %v", name, err)
		}
		if executor.Name() != name {
			t.Fatalf("resolve %q returned %q", name, executor.Name())
		}
	}
}

func TestRuntimeRegistryRejectsUnknownRuntime(t *testing.T) {
	registry := newRuntimeRegistry(&Server{})

	if _, err := registry.resolve("unknown-runtime"); err == nil {
		t.Fatal("expected unknown runtime to be rejected")
	}
}
