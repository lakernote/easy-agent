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
		{Spec: agent.ToolSpec{Name: "read", Description: "读取文件", Group: "files", GroupDescription: "文件操作", Parameters: map[string]any{"type": "object"}}, Run: func(context.Context, json.RawMessage) (string, error) { return "read", nil }},
		{Spec: agent.ToolSpec{Name: "shell", Description: "执行命令", Group: "execution", GroupDescription: "命令执行", Parameters: map[string]any{"type": "object"}}, Run: func(context.Context, json.RawMessage) (string, error) { return "shell", nil }},
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
	description := loader.Tool().Spec.Description
	if !strings.Contains(description, "execution：命令执行") || !strings.Contains(description, "files：文件操作") {
		t.Fatalf("精简目录没有提供稳定的能力组: %s", description)
	}
	if !strings.Contains(description, "已在 request.tools 中的工具应直接调用") || !strings.Contains(description, "groups 中的值") || !strings.Contains(description, "原生 function call") {
		t.Fatalf("工具目录没有说明两阶段加载方式: %s", description)
	}
	if output, err := loader.Tool().Run(context.Background(), json.RawMessage(`{"groups":["execution"]}`)); err != nil || !strings.Contains(output, `"shell"`) {
		t.Fatalf("load_tools failed: output=%s err=%v", output, err)
	}
	if len(registered) != 1 || registered[0].Spec.Name != "shell" {
		t.Fatalf("只应加载 shell: %+v", registered)
	}
	if strings.Contains(loader.Tool().Spec.Description, "execution：") {
		t.Fatalf("已完全加载的 execution 分组不应继续出现: %s", loader.Tool().Spec.Description)
	}
}

func TestLoaderPreloadsCoreTools(t *testing.T) {
	names := []string{"current_time", "calculate", "shell", "read", "grep", "find", "ls", "edit", "write", "web_research", "load_skill", "legacy_tool"}
	expected := []string{"read", "shell", "edit", "write", "load_skill"}
	catalog := make([]agent.Tool, 0, len(names))
	for _, name := range names {
		catalog = append(catalog, agent.Tool{Spec: agent.ToolSpec{Name: name, Group: "test", Description: name, Parameters: map[string]any{"type": "object"}}, Run: func(context.Context, json.RawMessage) (string, error) { return "", nil }})
	}
	loader, err := NewLoader(catalog)
	if err != nil {
		t.Fatal(err)
	}
	tools := loader.PreloadCore()
	if len(tools) != len(expected) {
		t.Fatalf("核心工具应直接预加载且稳定排序: tools=%+v", tools)
	}
	for index, name := range expected {
		if tools[index].Spec.Name != name {
			t.Fatalf("核心工具顺序错误: got %q at %d, want %q", tools[index].Spec.Name, index, name)
		}
	}
}

func TestLoaderPreloadsReadOnlyExplorationCore(t *testing.T) {
	names := []string{"current_time", "calculate", "read", "grep", "find", "ls", "web_research", "load_skill"}
	expected := []string{"read", "grep", "find", "ls", "load_skill"}
	catalog := make([]agent.Tool, 0, len(names))
	for _, name := range names {
		catalog = append(catalog, agent.Tool{Spec: agent.ToolSpec{Name: name, Group: "test", Description: name, Parameters: map[string]any{"type": "object"}}, Run: func(context.Context, json.RawMessage) (string, error) { return "", nil }})
	}
	loader, err := NewLoader(catalog)
	if err != nil {
		t.Fatal(err)
	}
	loaded := loader.PreloadCore()
	if len(loaded) != len(expected) {
		t.Fatalf("只读核心工具错误: %+v", loaded)
	}
	for index, name := range expected {
		if loaded[index].Spec.Name != name {
			t.Fatalf("只读核心工具顺序错误: got %q at %d, want %q", loaded[index].Spec.Name, index, name)
		}
	}
}

func TestLoaderDirectoryOmitsFullyLoadedGroups(t *testing.T) {
	catalog := []agent.Tool{
		{Spec: agent.ToolSpec{Name: "read", Group: "files", GroupDescription: "文件"}, Run: func(context.Context, json.RawMessage) (string, error) { return "", nil }},
		{Spec: agent.ToolSpec{Name: "load_skill", Group: "skills", GroupDescription: "技能"}, Run: func(context.Context, json.RawMessage) (string, error) { return "", nil }},
		{Spec: agent.ToolSpec{Name: "web_research", Group: "web", GroupDescription: "联网"}, Run: func(context.Context, json.RawMessage) (string, error) { return "", nil }},
	}
	loader, err := NewLoader(catalog)
	if err != nil {
		t.Fatal(err)
	}
	loader.Preload([]string{"read", "load_skill"})
	description := loader.Tool().Spec.Description
	if strings.Contains(description, "files：") || strings.Contains(description, "skills：") || !strings.Contains(description, "web：联网") {
		t.Fatalf("Loader 应只展示仍有未加载工具的能力组: %s", description)
	}
}

func TestLoaderPreloadsExplicitSelection(t *testing.T) {
	tool := agent.Tool{Spec: agent.ToolSpec{Name: "read", Group: "files", GroupDescription: "文件操作"}, Run: func(context.Context, json.RawMessage) (string, error) { return "", nil }}
	loader, err := NewLoader([]agent.Tool{tool})
	if err != nil {
		t.Fatal(err)
	}
	if loaded := loader.Preload([]string{"missing", "read", "read"}); len(loaded) != 1 || loaded[0].Spec.Name != "read" {
		t.Fatalf("显式工具没有正确预加载: %+v", loaded)
	}
}
