package mcpclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/appenv"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestLoaderSearchesAndRegistersOnlyMatchingTools(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1.0.0"}, nil)
	type arguments struct {
		Name string `json:"name"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "greet", Description: "问候一个人"}, func(_ context.Context, _ *mcp.CallToolRequest, input arguments) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "你好，" + input.Name}}}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "read_issue", Description: "Read a repository issue"}, func(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()

	loader := NewLoader(testEnvironment(t), []Config{{ID: "demo", Name: "Demo", Enabled: true, Transport: "http", Endpoint: httpServer.URL}})
	defer loader.Close()
	var registered []agent.Tool
	loader.SetRegister(func(tools []agent.Tool) error {
		registered = append(registered, tools...)
		return nil
	})
	directLoader := NewLoader(testEnvironment(t), []Config{{ID: "demo", Name: "Demo", Enabled: true, Transport: "http", Endpoint: httpServer.URL}})
	defer directLoader.Close()
	direct, err := directLoader.Preload(context.Background(), []string{"demo"})
	if err != nil || len(direct) != 2 || direct[0].Spec.Name != "mcp__demo__greet" || direct[1].Spec.Name != "mcp__demo__read_issue" {
		t.Fatalf("显式选择的小 MCP 应直接预加载且稳定排序: tools=%+v err=%v", direct, err)
	}
	if direct[0].Spec.ActivityKind != "mcp" || direct[0].Spec.ActivitySource != "demo" || direct[0].Spec.DisplayName != "greet" {
		t.Fatalf("MCP 展示身份没有保留: %+v", direct[0].Spec)
	}
	if spec := loader.Tool().Spec; spec.ActivityKind != "mcp_loader" || spec.Loader != true {
		t.Fatalf("MCP Loader 没有标记为能力编排: %+v", spec)
	}
	if len(registered) != 0 {
		t.Fatal("search_mcp_tools 调用前不应注册远端工具")
	}

	output, err := loader.Tool().Run(context.Background(), json.RawMessage(`{"id":"demo","query":"greet a person"}`))
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
	output, err = loader.Tool().Run(context.Background(), json.RawMessage(`{"id":"demo","query":"greet a person"}`))
	if err != nil || len(registered) != 1 || !strings.Contains(output, `"newlyAvailable":0`) {
		t.Fatalf("重复搜索不应重复注册同名工具: tools=%d output=%s err=%v", len(registered), output, err)
	}
}

func TestSearchToolsRanksMetadataAndLimitsSchemaLoading(t *testing.T) {
	tools := []agent.Tool{
		{Spec: agent.ToolSpec{Name: "mcp__demo__browser_click", Description: "Click an element on the page"}},
		{Spec: agent.ToolSpec{Name: "mcp__demo__browser_navigate", Description: "Navigate to a URL"}},
		{Spec: agent.ToolSpec{Name: "mcp__demo__browser_snapshot", Description: "Capture page accessibility snapshot"}},
		{Spec: agent.ToolSpec{Name: "mcp__demo__browser_console", Description: "Read console messages"}},
	}
	matches := searchTools(tools, "navigate URL and inspect page snapshot", 2)
	if len(matches) != 2 || matches[0].Spec.Name != "mcp__demo__browser_navigate" || matches[1].Spec.Name != "mcp__demo__browser_snapshot" {
		t.Fatalf("应只返回最相关的两个工具: %+v", matches)
	}
}

func TestSearchToolsDoesNotGuessWhenMetadataDoesNotMatch(t *testing.T) {
	tools := []agent.Tool{{Spec: agent.ToolSpec{Name: "mcp__demo__browser_click", Description: "Click an element"}}}
	if matches := searchTools(tools, "查询数据库慢日志", 5); len(matches) != 0 {
		t.Fatalf("没有元数据匹配时不应随便注入工具: %+v", matches)
	}
}

func TestConnectRejectsServerWithoutTools(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "empty", Version: "1.0.0"}, nil)
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()

	_, err := Connect(context.Background(), testEnvironment(t), Config{ID: "empty", Name: "Empty", Enabled: true, Transport: "http", Endpoint: httpServer.URL})
	if err == nil || !strings.Contains(err.Error(), "没有提供可调用的工具") {
		t.Fatalf("空 MCP 应被拒绝，实际错误: %v", err)
	}
}

func TestServerMetadataUsesPurposeInsteadOfCommand(t *testing.T) {
	loader := NewLoader(testEnvironment(t), []Config{{
		ID: "private", Name: "Private", Description: "查询内部工单", Enabled: true,
		Transport: "stdio", Command: "secret-command", Args: []string{"--token", "secret"},
	}})
	servers := loader.Servers()
	if len(servers) != 1 || servers[0].Description != "查询内部工单" || strings.Contains(servers[0].Description, "secret") {
		t.Fatalf("MCP 元数据不应暴露启动参数: %+v", servers)
	}
}

func testEnvironment(t *testing.T) *appenv.Environment {
	t.Helper()
	environment, err := appenv.Open(appenv.Config{Home: filepath.Join(t.TempDir(), "home")})
	if err != nil {
		t.Fatal(err)
	}
	return environment
}
