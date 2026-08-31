package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lakernote/easy-agent/internal/agent"
)

func TestLoaderRegistersOnlySelectedTools(t *testing.T) {
	catalog := []agent.Tool{
		{Spec: agent.ToolSpec{Name: "read", Description: "读取文件", Parameters: map[string]any{"type": "object"}}, Run: func(context.Context, json.RawMessage) (string, error) { return "read", nil }},
		{Spec: agent.ToolSpec{Name: "shell", Description: "执行命令", Parameters: map[string]any{"type": "object"}}, Run: func(context.Context, json.RawMessage) (string, error) { return "shell", nil }},
	}
	loader, err := NewLoader(catalog)
	if err != nil {
		t.Fatal(err)
	}
	var registered []agent.Tool
	loader.SetRegister(func(tools []agent.Tool) error {
		registered = append(registered, tools...)
		return nil
	})
	if output, err := loader.Tool().Run(context.Background(), json.RawMessage(`{"names":["shell"]}`)); err != nil || !strings.Contains(output, `"shell"`) {
		t.Fatalf("load_tools failed: output=%s err=%v", output, err)
	}
	if len(registered) != 1 || registered[0].Spec.Name != "shell" {
		t.Fatalf("只应加载 shell: %+v", registered)
	}
	if !strings.Contains(loader.Tool().Spec.Description, "可选名称：read, shell") {
		t.Fatalf("精简目录没有提供稳定的工具名称: %s", loader.Tool().Spec.Description)
	}
	if !strings.Contains(loader.Tool().Spec.Description, `{"names":["calculate"]}`) || !strings.Contains(loader.Tool().Spec.Description, "尚未加载不表示不可用") {
		t.Fatalf("工具目录没有说明两阶段加载方式: %s", loader.Tool().Spec.Description)
	}
}

func TestLoaderPreloadsExplicitSelection(t *testing.T) {
	tool := agent.Tool{Spec: agent.ToolSpec{Name: "read"}, Run: func(context.Context, json.RawMessage) (string, error) { return "", nil }}
	loader, err := NewLoader([]agent.Tool{tool})
	if err != nil {
		t.Fatal(err)
	}
	if loaded := loader.Preload([]string{"missing", "read", "read"}); len(loaded) != 1 || loaded[0].Spec.Name != "read" {
		t.Fatalf("显式工具没有正确预加载: %+v", loaded)
	}
}
