package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lakernote/easy-agent/internal/appenv"
	"github.com/lakernote/easy-agent/internal/permissions"
)

func testEnvironment(t *testing.T, workspace string) *appenv.Environment {
	t.Helper()
	environment, err := appenv.Open(appenv.Config{Home: filepath.Join(t.TempDir(), "home"), Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	return environment
}

func TestCurrentTimeIncludesOffset(t *testing.T) {
	tool := currentTimeTool()
	output, err := tool.Run(context.Background(), json.RawMessage(`{"timezone":"Asia/Shanghai"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"timezone": "Asia/Shanghai"`, `"utc_offset": "+08:00"`, `"weekday"`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("current_time 缺少 %s: %s", expected, output)
		}
	}
}

func TestCalculate(t *testing.T) {
	tool := calculateTool()
	output, err := tool.Run(context.Background(), json.RawMessage(`{"expression":"pow(2, 10) + sqrt(81)"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"result": "1033"`) {
		t.Fatalf("计算结果错误: %s", output)
	}
}

func TestCalculateAcceptsTypographicOperators(t *testing.T) {
	output, err := calculateTool().Run(context.Background(), json.RawMessage(`{"expression":"8＋8−30×1"}`))
	if err != nil || !strings.Contains(output, `"result": "-14"`) {
		t.Fatalf("排版运算符计算错误: output=%s err=%v", output, err)
	}
}

func TestCalculateRejectsDivisionByZero(t *testing.T) {
	_, err := calculateTool().Run(context.Background(), json.RawMessage(`{"expression":"1 / 0"}`))
	if err == nil || !strings.Contains(err.Error(), "除以零") {
		t.Fatalf("应拒绝除以零，实际错误: %v", err)
	}
}

func TestShellCapturesExitCodeAndDirectory(t *testing.T) {
	directory := t.TempDir()
	raw, _ := json.Marshal(map[string]any{
		"command": "pwd; printf problem >&2; exit 7", "working_directory": directory,
	})
	output, err := shellTool(testEnvironment(t, directory)).Run(context.Background(), raw)
	if err == nil || !strings.Contains(err.Error(), "退出码 7") {
		t.Fatalf("非零退出码应该明确返回失败: %v", err)
	}
	for _, expected := range []string{`"ok": false`, `"exit_code": 7`, `"stderr": "problem"`, `"error": "Shell 命令执行失败，退出码 7"`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("Shell 结果缺少 %q: %s", expected, output)
		}
	}
	if !strings.Contains(output, filepath.Clean(directory)) {
		t.Fatalf("Shell 返回给 Agent 的结果应保留真实工作目录: %s", output)
	}
}

func TestFileToolsReadSearchEditAndWrite(t *testing.T) {
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	if err := os.Mkdir(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "src", "service.go")
	if err := os.WriteFile(path, []byte("package src\n\nfunc Answer() int { return 41 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := &fileWorkspace{root: root, reads: map[string][sha256.Size]byte{}}

	readOutput, err := workspace.read(context.Background(), json.RawMessage(`{"path":"src/service.go","offset":3,"limit":1}`))
	if err != nil || !strings.Contains(readOutput, `"content": "func Answer() int { return 41 }"`) {
		t.Fatalf("read 结果错误: %s, %v", readOutput, err)
	}
	grepOutput, err := workspace.grep(context.Background(), json.RawMessage(`{"query":"Answer","glob":"*.go"}`))
	if err != nil || !strings.Contains(grepOutput, `"line": 3`) || !strings.Contains(grepOutput, `"path": "src/service.go"`) {
		t.Fatalf("grep 结果错误: %s, %v", grepOutput, err)
	}
	findOutput, err := workspace.find(context.Background(), json.RawMessage(`{"pattern":"src/**/*.go"}`))
	if err != nil || !strings.Contains(findOutput, `"src/service.go"`) {
		t.Fatalf("find 结果错误: %s, %v", findOutput, err)
	}
	editOutput, err := workspace.edit(context.Background(), json.RawMessage(`{"path":"src/service.go","old_text":"return 41","new_text":"return 42"}`))
	if err != nil || !strings.Contains(editOutput, `"replacements": 1`) {
		t.Fatalf("edit 结果错误: %s, %v", editOutput, err)
	}
	updated, _ := os.ReadFile(path)
	if !strings.Contains(string(updated), "return 42") {
		t.Fatalf("edit 未写入文件: %s", updated)
	}

	writePath := filepath.Join(root, "README.md")
	if err := os.WriteFile(writePath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.write(context.Background(), json.RawMessage(`{"path":"README.md","content":"new","overwrite":true}`)); err == nil || !strings.Contains(err.Error(), "先调用 read") {
		t.Fatalf("覆盖未读取文件应该失败: %v", err)
	}
	if _, err := workspace.read(context.Background(), json.RawMessage(`{"path":"README.md"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.write(context.Background(), json.RawMessage(`{"path":"README.md","content":"new","overwrite":true}`)); err != nil {
		t.Fatalf("读取后应该允许覆盖: %v", err)
	}
}

func TestFileToolsRejectOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	workspace := &fileWorkspace{root: root, reads: map[string][sha256.Size]byte{}}
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })
	input, _ := json.Marshal(map[string]string{"path": outside})
	if _, err := workspace.read(context.Background(), input); err == nil || !strings.Contains(err.Error(), "超出") {
		t.Fatalf("工作区外文件应该被拒绝: %v", err)
	}
}

func TestFileToolsAllowAdditionalProjectSourceByAbsolutePath(t *testing.T) {
	primary := t.TempDir()
	secondary := t.TempDir()
	path := filepath.Join(secondary, "shared.txt")
	if err := os.WriteFile(path, []byte("shared source"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := newFileWorkspace(primary, []string{secondary})
	input, _ := json.Marshal(map[string]string{"path": path})
	output, err := workspace.read(context.Background(), input)
	if err != nil || !strings.Contains(output, "shared source") || !strings.Contains(output, filepath.ToSlash(path)) {
		t.Fatalf("额外源文件夹应支持绝对路径读取: output=%s err=%v", output, err)
	}
}

func TestGrepRejectsSymlinkToOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("outside-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.txt")); err != nil {
		t.Fatal(err)
	}
	workspace := &fileWorkspace{root: root, reads: map[string][sha256.Size]byte{}}
	output, err := workspace.grep(context.Background(), json.RawMessage(`{"query":"outside-secret"}`))
	if err != nil || strings.Contains(output, "outside-secret") {
		t.Fatalf("grep 不应读取工作区外符号链接: output=%s err=%v", output, err)
	}
}

func TestShellTimeout(t *testing.T) {
	raw := json.RawMessage(`{"command":"sleep 5","timeout_seconds":1}`)
	startedAt := time.Now()
	output, err := shellTool(testEnvironment(t, t.TempDir())).Run(context.Background(), raw)
	if err == nil || !strings.Contains(err.Error(), "已终止") || !strings.Contains(output, `"timed_out": true`) || time.Since(startedAt) > 3*time.Second {
		t.Fatalf("Shell 没有按时终止: output=%s err=%v", output, err)
	}
}

func TestReadOnlyCatalogDoesNotExposeMutationTools(t *testing.T) {
	tools := CatalogWithResearchConfigAndPermissions(testEnvironment(t, t.TempDir()), nil, ResearchConfigFromEnvironment(), permissions.Policy{Mode: permissions.ModeReadOnly})
	for _, tool := range tools {
		if tool.Spec.Name == "shell" || tool.Spec.Name == "edit" || tool.Spec.Name == "write" {
			t.Fatalf("只读目录不应暴露 %q", tool.Spec.Name)
		}
	}
}

func TestWorkspaceWriteShellUsesHostSandboxWhenAvailable(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("当前测试只覆盖 macOS sandbox-exec")
	}
	root := t.TempDir()
	environment := testEnvironment(t, root)
	raw := json.RawMessage(`{"command":"printf ok > allowed.txt"}`)
	if _, err := shellToolWithPolicy(environment, permissions.Policy{Mode: permissions.ModeWorkspaceWrite}).Run(context.Background(), raw); err != nil {
		t.Fatalf("工作区写入应能通过 macOS 沙盒: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "allowed.txt"))
	if err != nil || string(content) != "ok" {
		t.Fatalf("沙盒内文件没有正确写入: content=%q err=%v", content, err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	command := fmt.Sprintf("printf blocked > %q", outside)
	if _, err := shellToolWithPolicy(environment, permissions.Policy{Mode: permissions.ModeWorkspaceWrite}).Run(context.Background(), mustJSON(map[string]string{"command": command})); err == nil {
		t.Fatal("工作区沙盒不应允许写入工作区外文件")
	}
}

func mustJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func TestOutputCaptureKeepsHeadAndTail(t *testing.T) {
	capture := newOutputCapture(10)
	_, _ = capture.Write([]byte("abcdefghijklmnop"))
	output := capture.String()
	if !strings.HasPrefix(output, "abcde") || !strings.HasSuffix(output, "lmnop") || !strings.Contains(output, "截断") {
		t.Fatalf("输出截断错误: %q", output)
	}
}

func TestParseDuckDuckGoResultsResolvesRealURL(t *testing.T) {
	body := `<div class="result"><a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Farticles%2Fagent&amp;rut=abc"><b>Agent Runtime</b></a><a class="result__snippet" href="x">A <b>technical</b> article.</a></div>`
	results := parseDuckDuckGoResults(body, 5)
	if len(results) != 1 || results[0].URL != "https://example.com/articles/agent" || results[0].Title != "Agent Runtime" || results[0].Snippet != "A technical article." {
		t.Fatalf("搜索结果解析错误: %+v", results)
	}
}

func TestToolCategoriesComeFromRegistration(t *testing.T) {
	categories := make(map[string]string)
	for _, info := range InfoList(testEnvironment(t, t.TempDir()), nil) {
		categories[info.Name] = info.Category
	}
	for name, expected := range map[string]string{
		"read": categoryFile, "shell": categoryExecution,
		"web_research": categoryInformation, "current_time": categoryInformation,
	} {
		if categories[name] != expected {
			t.Fatalf("工具 %s 分类错误: got=%q want=%q", name, categories[name], expected)
		}
	}
}

func TestCatalogExposesOnlyHighLevelWebResearch(t *testing.T) {
	for _, info := range InfoList(testEnvironment(t, t.TempDir()), nil) {
		if info.Name == "web_search" || info.Name == "web_fetch" || info.Name == "weather" {
			t.Fatalf("旧联网工具不应再暴露: %+v", info)
		}
	}
}

func TestWebResearchExtractsMetadataAndEvidence(t *testing.T) {
	page := `<html><head><title>官方标题</title><meta property="article:published_time" content="2026-09-08"></head><body><nav>导航噪声</nav><main><h1>研究正文</h1><p>` + strings.Repeat("内容", 800) + `</p></main></body></html>`
	title, published := pageMetadata(page, "候选标题")
	content := cleanHTMLText(mainOrArticleHTML(page))
	if title != "官方标题" || published != "2026-09-08" || !strings.Contains(content, "研究正文") || strings.Contains(content, "导航噪声") {
		t.Fatalf("网页正文或元数据提取错误: title=%q published=%q content=%q", title, published, content[:min(len(content), 100)])
	}
	truncated, ok := truncateRunes(content, 1000)
	if !ok || !strings.Contains(truncated, "内容已截断") {
		t.Fatalf("来源正文截断标记错误: %q", truncated)
	}
}

func TestWebResearchToolIsHighLevelAndDirect(t *testing.T) {
	tool := webResearchTool()
	if tool.Spec.Name != "web_research" || tool.Spec.DiscoveryOnly || !strings.Contains(tool.Spec.Description, "自动") || !strings.Contains(tool.Spec.Description, "[S1]") {
		t.Fatalf("web_research 应是高层证据工具: %+v", tool.Spec)
	}
}
