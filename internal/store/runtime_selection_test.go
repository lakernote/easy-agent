package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSessionRuntimeIsPinnedAtCreation(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	created, err := database.CreateSessionWithRuntime("codex-session", "Codex", RuntimeCodex, "gpt-5.6-sol", t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if created.Runtime != RuntimeCodex {
		t.Fatalf("created runtime = %q", created.Runtime)
	}
	loaded, err := database.LoadSessionWindow("codex-session", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Runtime != RuntimeCodex {
		t.Fatalf("loaded runtime = %q", loaded.Runtime)
	}
}
