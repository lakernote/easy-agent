package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lakernote/easy-agent/internal/appenv"
	builtinmcp "github.com/lakernote/easy-agent/internal/builtin/mcp"
	"github.com/lakernote/easy-agent/internal/store"
)

func TestPresetRuntimeCheckReportsMissingCommand(t *testing.T) {
	preset := builtinmcp.Preset{Name: "fixture", Requirement: "fixture runtime", RequiredCommands: []string{"easyagent-command-that-does-not-exist"}}
	environment, openErr := appenv.Open(appenv.Config{Home: filepath.Join(t.TempDir(), "home")})
	if openErr != nil {
		t.Fatal(openErr)
	}
	err := (&Server{env: environment}).checkMCPPresetRuntime(context.Background(), preset)
	if err == nil || !strings.Contains(err.Error(), "easyagent-command-that-does-not-exist") {
		t.Fatalf("应返回明确的缺失依赖，实际为 %v", err)
	}
}

func TestUninstallPresetRemovesOnlyPrivatePackageAndConfig(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	environment, err := appenv.Open(appenv.Config{Home: filepath.Join(t.TempDir(), "home")})
	if err != nil {
		t.Fatal(err)
	}
	application := New(database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}, environment)
	defer application.Shutdown(context.Background())

	if err := database.SaveMCP(store.MCPConfig{ID: "playwright", Name: "Playwright", Transport: "stdio"}); err != nil {
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

	request := httptest.NewRequest(http.MethodDelete, "/api/v1/mcp/presets/playwright/install", nil)
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("卸载响应错误: HTTP %d body=%s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(environment.Runtime(), "mcp", "playwright")); !os.IsNotExist(err) {
		t.Fatalf("私有 MCP 包没有删除: %v", err)
	}
	if _, err := os.Stat(neighbor); err != nil {
		t.Fatalf("其他 MCP 包受到影响: %v", err)
	}
	values, err := database.MCPs()
	if err != nil || len(values) != 0 {
		t.Fatalf("MCP 配置没有删除: values=%+v err=%v", values, err)
	}
}

func TestValidateMCPRequiresCredentialsOnlyWhenEnabled(t *testing.T) {
	tests := []struct {
		name    string
		config  store.MCPConfig
		wantErr string
	}{
		{name: "disabled bearer can be drafted", config: store.MCPConfig{Enabled: false, Transport: "http", Endpoint: "https://example.com", AuthType: "bearer"}},
		{name: "enabled bearer needs token", config: store.MCPConfig{Enabled: true, Transport: "http", Endpoint: "https://example.com", AuthType: "bearer"}, wantErr: "Token"},
		{name: "enabled basic needs both", config: store.MCPConfig{Enabled: true, Transport: "http", Endpoint: "https://example.com", AuthType: "basic", Username: "user"}, wantErr: "用户名和密码"},
		{name: "enabled bearer complete", config: store.MCPConfig{Enabled: true, Transport: "http", Endpoint: "https://example.com", AuthType: "bearer", Token: "secret"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateMCP(test.config)
			if test.wantErr == "" && err != nil {
				t.Fatalf("不应校验失败: %v", err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("期望错误包含 %q，实际为 %v", test.wantErr, err)
			}
		})
	}
}
