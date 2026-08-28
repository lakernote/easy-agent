package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	output, err := shellTool().Run(context.Background(), raw)
	if err == nil || !strings.Contains(err.Error(), "退出码 7") {
		t.Fatalf("非零退出码应该明确返回失败: %v", err)
	}
	for _, expected := range []string{`"ok": false`, `"exit_code": 7`, filepath.Clean(directory), `"stderr": "problem"`, `"error": "Shell 命令执行失败，退出码 7"`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("Shell 结果缺少 %q: %s", expected, output)
		}
	}
}

func TestShellTimeout(t *testing.T) {
	raw := json.RawMessage(`{"command":"sleep 5","timeout_seconds":1}`)
	startedAt := time.Now()
	output, err := shellTool().Run(context.Background(), raw)
	if err == nil || !strings.Contains(err.Error(), "已终止") || !strings.Contains(output, `"timed_out": true`) || time.Since(startedAt) > 3*time.Second {
		t.Fatalf("Shell 没有按时终止: output=%s err=%v", output, err)
	}
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
	body := `<div class="result"><a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgithub.com%2Flakernote%2Feasy%2Dpostman&amp;rut=abc"><b>EasyPostman</b> - GitHub</a><a class="result__snippet" href="x">An <b>open-source</b> API tool.</a></div>`
	results := parseDuckDuckGoResults(body, 5)
	if len(results) != 1 || results[0].URL != "https://github.com/lakernote/easy-postman" || results[0].Title != "EasyPostman - GitHub" || results[0].Snippet != "An open-source API tool." {
		t.Fatalf("搜索结果解析错误: %+v", results)
	}
}
