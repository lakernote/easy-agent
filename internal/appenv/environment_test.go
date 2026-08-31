package appenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenUsesStableDefaultWorkspace(t *testing.T) {
	home := t.TempDir()
	environment, err := Open(Config{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	want, err := cleanAbsolute(filepath.Join(home, "workspaces", "default"))
	if err != nil {
		t.Fatal(err)
	}
	if environment.Workspace() != want {
		t.Fatalf("workspace = %q, want %q", environment.Workspace(), want)
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("default workspace was not created: %v", err)
	}
}

func TestWithWorkspaceSelectsExistingDirectory(t *testing.T) {
	environment, err := Open(Config{Home: filepath.Join(t.TempDir(), "home")})
	if err != nil {
		t.Fatal(err)
	}
	selected := t.TempDir()
	derived, err := environment.WithWorkspace(selected)
	if err != nil {
		t.Fatal(err)
	}
	want, err := cleanAbsolute(selected)
	if err != nil {
		t.Fatal(err)
	}
	if derived.Workspace() != want {
		t.Fatalf("workspace = %q, want %q", derived.Workspace(), want)
	}
	if environment.Workspace() == derived.Workspace() {
		t.Fatal("selecting a workspace mutated the default environment")
	}
}

func TestWithWorkspaceRejectsMissingDirectory(t *testing.T) {
	environment, err := Open(Config{Home: filepath.Join(t.TempDir(), "home")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := environment.WithWorkspace(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected missing workspace to fail")
	}
}

func TestResolveWorkspacePathRejectsEscape(t *testing.T) {
	environment, err := Open(Config{Home: filepath.Join(t.TempDir(), "home"), Workspace: filepath.Join(t.TempDir(), "workspace")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := environment.ResolveWorkspacePath("../secret"); err == nil {
		t.Fatal("expected workspace escape to fail")
	}
	resolved, err := environment.ResolveWorkspacePath("src")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resolved, environment.Workspace()+string(filepath.Separator)) {
		t.Fatalf("resolved path %q is outside workspace", resolved)
	}
}

func TestEnvironOwnsPathAndKeepsExtraValues(t *testing.T) {
	home := t.TempDir()
	extras := t.TempDir()
	environment, err := Open(Config{Home: home, ExtraPaths: []string{extras}})
	if err != nil {
		t.Fatal(err)
	}
	values := strings.Join(environment.Environ(map[string]string{"EASYAGENT_TEST": "yes", "PATH": "ignored"}), "\n")
	if !strings.Contains(values, "EASYAGENT_TEST=yes") {
		t.Fatalf("extra environment missing: %s", values)
	}
	if !strings.Contains(values, "PATH="+environment.Bin()+string(os.PathListSeparator)+extras) {
		t.Fatalf("deterministic PATH missing: %s", values)
	}
}
