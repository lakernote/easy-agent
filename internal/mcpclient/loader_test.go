package mcpclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestLoaderConnectsAndRegistersToolsOnlyWhenRequested(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1.0.0"}, nil)
	type arguments struct {
		Name string `json:"name"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "greet", Description: "问候一个人"}, func(_ context.Context, _ *mcp.CallToolRequest, input arguments) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "你好，" + input.Name}}}, nil, nil
	})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()

	loader := NewLoader([]store.MCPConfig{{ID: "demo", Name: "Demo", Enabled: true, Transport: "http", Endpoint: httpServer.URL}})
	defer loader.Close()
	var registered []agent.Tool
	loader.SetRegister(func(tools []agent.Tool) error {
		registered = append(registered, tools...)
		return nil
	})
	if len(registered) != 0 {
		t.Fatal("load_mcp 调用前不应注册远端工具")
	}

	output, err := loader.Tool().Run(context.Background(), json.RawMessage(`{"id":"demo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 1 || registered[0].Spec.Name != "mcp__demo__greet" || !strings.Contains(output, "mcp__demo__greet") {
		t.Fatalf("MCP 工具没有按需注册: tools=%+v output=%s", registered, output)
	}
	result, err := registered[0].Run(context.Background(), json.RawMessage(`{"name":"小易"}`))
	if err != nil || !strings.Contains(result, "你好，小易") {
		t.Fatalf("远端工具执行失败: output=%s err=%v", result, err)
	}
}

func TestConnectRejectsServerWithoutTools(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "empty", Version: "1.0.0"}, nil)
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()

	_, err := Connect(context.Background(), store.MCPConfig{ID: "empty", Name: "Empty", Enabled: true, Transport: "http", Endpoint: httpServer.URL})
	if err == nil || !strings.Contains(err.Error(), "没有提供可调用的工具") {
		t.Fatalf("空 MCP 应被拒绝，实际错误: %v", err)
	}
}

func TestServerMetadataUsesPurposeInsteadOfCommand(t *testing.T) {
	loader := NewLoader([]store.MCPConfig{{
		ID: "private", Name: "Private", Description: "查询内部工单", Enabled: true,
		Transport: "stdio", Command: "secret-command", Args: []string{"--token", "secret"},
	}})
	servers := loader.Servers()
	if len(servers) != 1 || servers[0].Description != "查询内部工单" || strings.Contains(servers[0].Description, "secret") {
		t.Fatalf("MCP 元数据不应暴露启动参数: %+v", servers)
	}
}
