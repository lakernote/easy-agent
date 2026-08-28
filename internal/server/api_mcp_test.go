package server

import (
	"context"
	"strings"
	"testing"

	builtinmcp "github.com/lakernote/easy-agent/internal/builtin/mcp"
	"github.com/lakernote/easy-agent/internal/store"
)

func TestPresetRuntimeCheckReportsMissingCommand(t *testing.T) {
	preset := builtinmcp.Preset{Name: "fixture", Requirement: "fixture runtime", RequiredCommands: []string{"easyagent-command-that-does-not-exist"}}
	err := checkMCPPresetRuntime(context.Background(), preset)
	if err == nil || !strings.Contains(err.Error(), "easyagent-command-that-does-not-exist") {
		t.Fatalf("应返回明确的缺失依赖，实际为 %v", err)
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
