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
	created, err := database.CreateSession(CreateSessionParams{ID: "codex-session", Title: "Codex", Runtime: RuntimeCodex, Model: "gpt-5.6-sol", Workspace: t.TempDir(), CreatedAt: time.Now()})
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
