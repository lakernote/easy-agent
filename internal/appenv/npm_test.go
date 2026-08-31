package appenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUninstallNPMPackageOnlyRemovesPrivateMCPDirectory(t *testing.T) {
	environment, err := Open(Config{Home: filepath.Join(t.TempDir(), "home")})
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(environment.Runtime(), "mcp", "playwright", "node_modules", "fixture")
	if err := os.MkdirAll(installed, 0o755); err != nil {
		t.Fatal(err)
	}
	neighbor := filepath.Join(environment.Runtime(), "mcp", "keep", "marker")
	if err := os.MkdirAll(filepath.Dir(neighbor), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(neighbor, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := environment.UninstallNPMPackage("playwright"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(environment.Runtime(), "mcp", "playwright")); !os.IsNotExist(err) {
		t.Fatalf("目标 MCP 目录没有删除: %v", err)
	}
	if _, err := os.Stat(neighbor); err != nil {
		t.Fatalf("相邻 MCP 目录受到影响: %v", err)
	}
}

func TestUninstallNPMPackageRejectsPathTraversal(t *testing.T) {
	environment, err := Open(Config{Home: filepath.Join(t.TempDir(), "home")})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", ".", "..", "../outside", "nested/name"} {
		if err := environment.UninstallNPMPackage(id); err == nil {
			t.Fatalf("应拒绝 MCP ID %q", id)
		}
	}
}
