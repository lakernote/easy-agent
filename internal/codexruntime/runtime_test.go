package codexruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lakernote/easy-agent/internal/appenv"
)

func TestDetectFindsCodexOnEnvironmentPath(t *testing.T) {
	bin := t.TempDir()
	path := filepath.Join(bin, "codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo codex-test; exit 0; fi\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	environment, err := appenv.Open(appenv.Config{Home: filepath.Join(t.TempDir(), "home"), ExtraPaths: []string{bin}})
	if err != nil {
		t.Fatal(err)
	}
	status := Detect(environment)
	if !status.Installed || status.Path != path || status.Version != "codex-test" {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestRunMessageUsesAppServerThreadAndStreamsAnswer(t *testing.T) {
	bin := t.TempDir()
	path := filepath.Join(bin, "codex")
	script := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*) echo '{"id":1,"result":{"userAgent":"fake","codexHome":"/tmp","platformFamily":"unix","platformOs":"macos"}}' ;;
    *'"method":"thread/start"'*) echo '{"id":2,"result":{"thread":{"id":"thread-test"}}}' ;;
    *'"method":"thread/resume"'*) echo '{"id":2,"result":{"thread":{"id":"thread-test"}}}' ;;
	    *'"method":"turn/start"'*) echo '{"id":3,"result":{"turn":{"id":"turn-test","status":"inProgress"}}}'; echo '{"method":"item/completed","params":{"item":{"type":"userMessage","content":[{"type":"text","text":"say hello"}]}}}'; echo '{"method":"item/agentMessage/delta","params":{"delta":"hello"}}'; echo '{"method":"turn/completed","params":{"turn":{"status":"completed","error":null}}}' ;;
  esac
done
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	var delta string
	result, err := RunMessage(context.Background(), Config{Path: path, Workspace: workspace, Timeout: time.Second, OnDelta: func(value string) { delta += value }}, "say hello")
	if err != nil {
		t.Fatal(err)
	}
	if result.ThreadID != "thread-test" || result.Answer != "hello" || delta != "hello" {
		t.Fatalf("unexpected result: %+v, delta=%q", result, delta)
	}
	resumed, err := RunMessage(context.Background(), Config{Path: path, Workspace: workspace, ThreadID: result.ThreadID, Timeout: time.Second}, "continue")
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ThreadID != result.ThreadID || resumed.Answer != "hello" {
		t.Fatalf("unexpected resumed result: %+v", resumed)
	}
}

func TestRunMessageAnswersUnsupportedServerRequestAndContinues(t *testing.T) {
	bin := t.TempDir()
	path := filepath.Join(bin, "codex")
	script := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*) echo '{"id":1,"result":{}}' ;;
    *'"method":"thread/start"'*)
      printf '%s' "$line" > "$EASYAGENT_THREAD_REQUEST_FILE"
      echo '{"method":"item/tool/requestUserInput","id":99,"params":{"threadId":"thread-test"}}'
      IFS= read -r response
      printf '%s' "$response" > "$EASYAGENT_RESPONSE_FILE"
      echo '{"id":2,"result":{"thread":{"id":"thread-test"}}}'
      ;;
    *'"method":"turn/start"'*)
      printf '%s' "$line" > "$EASYAGENT_TURN_REQUEST_FILE"
      echo '{"id":3,"result":{"turn":{"id":"turn-test","status":"inProgress"}}}'
      echo '{"method":"item/agentMessage/delta","params":{"delta":"continued"}}'
      echo '{"method":"turn/completed","params":{"turn":{"status":"completed","error":null}}}'
      ;;
  esac
done
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	responseFile := filepath.Join(t.TempDir(), "response.json")
	threadRequestFile := filepath.Join(t.TempDir(), "thread-request.json")
	turnRequestFile := filepath.Join(t.TempDir(), "turn-request.json")
	workspace := t.TempDir()
	result, err := RunMessage(context.Background(), Config{
		Path: path, Workspace: workspace, Timeout: time.Second,
		Env: append(os.Environ(), "EASYAGENT_RESPONSE_FILE="+responseFile, "EASYAGENT_THREAD_REQUEST_FILE="+threadRequestFile, "EASYAGENT_TURN_REQUEST_FILE="+turnRequestFile),
	}, "continue")
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer != "continued" {
		t.Fatalf("unexpected result: %+v", result)
	}
	response, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response), `"id":99`) || !strings.Contains(string(response), `"code":-32000`) {
		t.Fatalf("expected JSON-RPC error response, got %s", response)
	}
	threadRequest, err := os.ReadFile(threadRequestFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(threadRequest), `"sandbox":"danger-full-access"`) || strings.Contains(string(threadRequest), `"sandboxPolicy"`) {
		t.Fatalf("thread request should use SandboxMode, got %s", threadRequest)
	}
	turnRequest, err := os.ReadFile(turnRequestFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(turnRequest), `"sandboxPolicy":{"type":"dangerFullAccess"}`) || strings.Contains(string(turnRequest), `"writableRoots"`) {
		t.Fatalf("turn request should use dangerFullAccess, got %s", turnRequest)
	}
}

func TestRunMessageReturnsWhenContextExpiresWithOpenChildPipe(t *testing.T) {
	bin := t.TempDir()
	path := filepath.Join(bin, "codex")
	script := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*) echo '{"id":1,"result":{}}' ;;
    *'"method":"thread/start"'*) echo '{"id":2,"result":{"thread":{"id":"thread-test"}}}' ;;
    *'"method":"turn/start"'*)
      echo '{"id":3,"result":{"turn":{"id":"turn-test","status":"inProgress"}}}'
      sleep 10
      ;;
  esac
done
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now()
	_, err := RunMessage(context.Background(), Config{Path: path, Workspace: t.TempDir(), Timeout: 100 * time.Millisecond}, "timeout")
	if err == nil || time.Since(startedAt) > 2*time.Second {
		t.Fatalf("RunMessage should honor context cancellation: err=%v elapsed=%s", err, time.Since(startedAt))
	}
}

func TestConsumeNotificationMapsDetailsAndDurations(t *testing.T) {
	timers := &eventTimers{itemStartedAt: make(map[string]time.Time)}
	var events []Event
	config := Config{OnEvent: func(event Event) { events = append(events, event) }}
	started := rpcMessage{Method: "item/started", Params: json.RawMessage(`{"item":{"id":"item-1","type":"webSearch","query":"合肥明天天气"}}`)}
	completed := rpcMessage{Method: "item/completed", Params: json.RawMessage(`{"item":{"id":"item-1","type":"webSearch","status":"completed","query":"合肥明天天气","action":{"type":"search"}}}`)}
	consumeNotificationWithAnswer(started, config, &strings.Builder{}, timers, nil)
	time.Sleep(2 * time.Millisecond)
	consumeNotificationWithAnswer(completed, config, &strings.Builder{}, timers, nil)
	if len(events) != 2 {
		t.Fatalf("expected start and completed events, got %d", len(events))
	}
	if events[0].Name != "webSearch" || events[0].Detail != "搜索：合肥明天天气" || events[0].Input != "合肥明天天气" {
		t.Fatalf("unexpected web search event: %+v", events[0])
	}
	if events[1].Status != "success" || events[1].Duration <= 0 {
		t.Fatalf("expected completed event with duration: %+v", events[1])
	}

	consumeNotificationWithAnswer(rpcMessage{Method: "turn/started", Params: json.RawMessage(`{"turn":{"status":"inProgress"}}`)}, config, &strings.Builder{}, timers, nil)
	time.Sleep(2 * time.Millisecond)
	consumeNotificationWithAnswer(rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"turn":{"status":"completed","error":null}}`)}, config, &strings.Builder{}, timers, nil)
	if events[len(events)-1].Kind != "codex_turn" || events[len(events)-1].Duration <= 0 {
		t.Fatalf("expected completed turn with duration: %+v", events[len(events)-1])
	}
}

func TestConsumeNotificationPreservesMCPIdentity(t *testing.T) {
	var events []Event
	config := Config{OnEvent: func(event Event) { events = append(events, event) }}
	message := rpcMessage{Method: "item/completed", Params: json.RawMessage(`{"item":{"id":"mcp-1","type":"mcpToolCall","status":"completed","server":"context7","tool":"query-docs","arguments":{"libraryId":"/reactjs/react.dev"},"result":{"content":[]}}}`)}
	consumeNotificationWithAnswer(message, config, &strings.Builder{}, &eventTimers{itemStartedAt: make(map[string]time.Time)}, nil)
	if len(events) != 1 || events[0].ActivityID != "mcp-1" || events[0].ActivityKind != "mcp" || events[0].ActivitySource != "context7" || events[0].DisplayName != "query-docs" || events[0].Detail != "context7 / query-docs" {
		t.Fatalf("MCP identity was not preserved: %+v", events)
	}
}

func TestConsumeNotificationMapsPlanAndFileProgress(t *testing.T) {
	var events []Event
	config := Config{OnEvent: func(event Event) { events = append(events, event) }}
	consumeNotificationWithAnswer(rpcMessage{Method: "turn/plan/updated", Params: json.RawMessage(`{"turnId":"turn-1","explanation":"开始实现","plan":[{"step":"检查代码","status":"completed"},{"step":"修改 UI","status":"inProgress"},{"step":"运行测试","status":"pending"}]}`)}, config, &strings.Builder{}, nil, nil)
	consumeNotificationWithAnswer(rpcMessage{Method: "item/fileChange/patchUpdated", Params: json.RawMessage(`{"itemId":"file-1","changes":[{"path":"web/src/App.tsx","kind":"update","diff":"@@ -1 +1 @@\n-old\n+new"}]}`)}, config, &strings.Builder{}, nil, nil)
	if len(events) != 2 {
		t.Fatalf("计划和文件进度没有进入事件流: %+v", events)
	}
	if events[0].Name != "plan" || events[0].ActivityKind != "plan" || events[0].ActivityID != "turn-1" || events[0].DisplayName != "修改 UI" || !strings.Contains(events[0].Detail, "第 2/3 步") || !strings.Contains(events[0].Output, "运行测试") {
		t.Fatalf("计划进度映射错误: %+v", events[0])
	}
	if events[1].Name != "fileChange" || events[1].ActivityKind != "tool" || events[1].ActivityID != "file-1" || !strings.Contains(events[1].Input, "web/src/App.tsx") {
		t.Fatalf("文件变更进度映射错误: %+v", events[1])
	}
}

func TestCodexStatusAndItemDuration(t *testing.T) {
	if codexStatus("in_progress") != "started" || codexStatus("cancelled") != "error" || codexStatus("completed") != "success" {
		t.Fatal("unexpected Codex status mapping")
	}
	if got := itemDuration(map[string]any{"durationMs": float64(42)}); got != 42*time.Millisecond {
		t.Fatalf("unexpected item duration: %s", got)
	}
}

func TestParseTokenUsage(t *testing.T) {
	usage, ok := parseTokenUsage(json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","tokenUsage":{"last":{"inputTokens":1200,"outputTokens":300,"cachedInputTokens":900,"cacheWriteInputTokens":50,"reasoningOutputTokens":100,"totalTokens":1500},"total":{"inputTokens":1200,"outputTokens":300,"cachedInputTokens":900,"cacheWriteInputTokens":50,"reasoningOutputTokens":100,"totalTokens":1500},"modelContextWindow":32768}}`))
	if !ok || !usage.Reported || usage.InputTokens != 1200 || usage.CachedInputTokens != 900 || usage.CacheWriteInputTokens != 50 || usage.ModelContextWindow != 32768 {
		t.Fatalf("unexpected token usage: %+v", usage)
	}
}

func TestTruncateUTF8KeepsTraceBounded(t *testing.T) {
	value := strings.Repeat("中", maxTraceValueBytes)
	result := truncateUTF8(value, maxTraceValueBytes)
	if len(result) > maxTraceValueBytes || !strings.HasSuffix(result, "… [truncated]") || !utf8.ValidString(result) {
		t.Fatalf("unexpected truncated value: bytes=%d valid=%v", len(result), utf8.ValidString(result))
	}
}

func TestCodexCapabilityTextUsesNativeSkillAndManagedMCPNames(t *testing.T) {
	got := codexCapabilityText("@skill:review-code 检查并使用 @mcp:openai.docs")
	if got != "$review-code 检查并使用 [优先使用 MCP server easyagent_openai_docs]" {
		t.Fatalf("Codex 能力引用转换错误: %q", got)
	}
}

func TestCodexCapabilityTextIncludesAdditionalProjectSources(t *testing.T) {
	got := codexCapabilityText("检查两个仓库", "/srv/api", "/srv/web")
	for _, expected := range []string{"<easyagent_project_sources>", "/srv/api", "/srv/web", "keep cwd unchanged", "检查两个仓库"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("Codex 项目源文件夹上下文缺少 %q: %s", expected, got)
		}
	}
}

func TestCodexTurnInputMapsTextImageAndPDF(t *testing.T) {
	input, cleanup, err := codexTurnInput("检查附件", Config{Attachments: []Attachment{
		{Name: "notes.txt", MIMEType: "text/plain", Kind: "text", Data: []byte("hello")},
		{Name: "screen.png", MIMEType: "image/png", Kind: "image", Data: []byte("png")},
		{Name: "spec.pdf", MIMEType: "application/pdf", Kind: "pdf", Data: []byte("pdf")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(input) != 3 || input[1]["type"] != "localImage" || input[2]["type"] != "text" {
		t.Fatalf("unexpected Codex input: %#v", input)
	}
	if !strings.Contains(input[0]["text"].(string), "hello") || !strings.Contains(input[2]["text"].(string), "spec.pdf") {
		t.Fatalf("attachment context missing: %#v", input)
	}
	imagePath := input[1]["path"].(string)
	if data, err := os.ReadFile(imagePath); err != nil || string(data) != "png" {
		t.Fatalf("materialized image = %q, err=%v", data, err)
	}
	cleanup()
	if _, err := os.Stat(imagePath); !os.IsNotExist(err) {
		t.Fatalf("temporary attachment was not removed: %v", err)
	}
}
