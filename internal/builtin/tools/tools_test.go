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
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"exit_code": 7`, filepath.Clean(directory), `"stderr": "problem"`} {
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
