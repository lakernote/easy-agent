package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lakernote/easy-agent/internal/permissions"
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
	if err := database.SetSessionModel(created.ID, "gpt-5.6-terra"); err != nil {
		t.Fatal(err)
	}
	loaded, err = database.LoadSessionWindow("codex-session", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Model != "gpt-5.6-terra" {
		t.Fatalf("effective model = %q", loaded.Model)
	}
}

func TestSessionPermissionsRoundTrip(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	writableRoot := filepath.Join(t.TempDir(), "workspace")
	want := permissions.Policy{Mode: permissions.ModeCustom, Approval: permissions.ApprovalOnRequest, WritableRoots: []string{writableRoot}, NetworkAccess: false}
	created, err := database.CreateSession(CreateSessionParams{ID: "permission-session", Title: "权限", Runtime: RuntimeEasyAgent, Model: "fixture", Permissions: want, CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if created.Permissions.Mode != permissions.ModeCustom || created.Permissions.Approval != permissions.ApprovalOnRequest || created.Permissions.NetworkAccess {
		t.Fatalf("created permissions = %+v", created.Permissions)
	}
	loaded, err := database.LoadSession("permission-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Permissions.WritableRoots) != 1 || loaded.Permissions.WritableRoots[0] != writableRoot {
		t.Fatalf("loaded permissions = %+v", loaded.Permissions)
	}
}
