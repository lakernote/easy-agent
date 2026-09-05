// Package codexruntime 是 EasyAgent 对 Codex app-server 的窄适配层。
// 工具、Skill、沙箱和会话历史由 Codex 自己管理，避免两套 Runtime 重复注入
// 巨大的工具 Schema。
package codexruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lakernote/easy-agent/internal/appenv"
)

const installDocsURL = "https://developers.openai.com/codex/cli"
const installScriptURL = "https://chatgpt.com/codex/install.sh"
const detectCommandTimeout = 5 * time.Second

type Status struct {
	// Installed 表示 codex CLI 可执行文件存在。app-server 不是另一个安装包，
	// 而是同一个 CLI 的子命令；单独暴露 AppServerAvailable，避免页面只检测到
	// CLI 就误报“Runtime 可用”。
	Installed          bool   `json:"installed"`
	AppServerAvailable bool   `json:"appServerAvailable"`
	Path               string `json:"path,omitempty"`
	Version            string `json:"version,omitempty"`
	Message            string `json:"message"`
	InstallCommand     string `json:"installCommand"`
	InstallURL         string `json:"installUrl"`
}

// Detect 只做本机文件和 --version 检查，不启动 app-server，也不修改用户环境。
func Detect(environment *appenv.Environment) Status {
	status := Status{InstallURL: installDocsURL, InstallCommand: "curl -fsSL " + installScriptURL + " | sh"}
	paths := []string{}
	if environment != nil {
		if value, err := environment.ResolveCommand("codex"); err == nil {
			paths = append(paths, value)
		}
	}
	if value, err := exec.LookPath("codex"); err == nil {
		paths = append(paths, value)
	}
	if runtime.GOOS == "darwin" {
		paths = append(paths, "/Applications/ChatGPT.app/Contents/Resources/codex")
	}
	if userHome, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(userHome, ".local", "bin", "codex"))
	}
	seen := map[string]struct{}{}
	for _, path := range paths {
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), detectCommandTimeout)
		command := exec.CommandContext(ctx, path, "--version")
		if environment != nil {
			command.Env = environment.Environ(nil)
		}
		output, runErr := command.Output()
		cancel()
		if runErr != nil {
			continue
		}
		status.Installed = true
		status.Path = path
		status.Version = strings.TrimSpace(string(output))
		appCtx, appCancel := context.WithTimeout(context.Background(), detectCommandTimeout)
		appServer := exec.CommandContext(appCtx, path, "app-server", "--help")
		if environment != nil {
			appServer.Env = environment.Environ(nil)
		}
		_, appErr := appServer.Output()
		appCancel()
		if appErr == nil {
			status.AppServerAvailable = true
			status.Message = "Codex CLI 与 app-server 已就绪"
		} else {
			status.Message = "已找到 Codex CLI，但 app-server 子命令不可用"
		}
		return status
	}
	status.Message = "未检测到 Codex CLI；服务器只需安装 CLI，不需要 ChatGPT Desktop"
	return status
}

// Install 下载并执行官方 Codex CLI 安装脚本。安装在当前 EasyAgent 进程用户的
// HOME 下，不使用 sudo；这样服务器上的检测、配置和后续 app-server 会保持同一用户。
func Install(ctx context.Context, environment *appenv.Environment) (string, error) {
	if environment == nil {
		return "", errors.New("Codex Runtime 缺少运行环境")
	}
	curlPath, err := environment.ResolveCommand("curl")
	if err != nil {
		return "", errors.New("服务器未找到 curl，请先安装 curl")
	}
	command := exec.CommandContext(ctx, curlPath, "-fsSL", installScriptURL)
	command.Env = environment.Environ(nil)
	output, err := command.Output()
	if err != nil {
		if exitErr := new(exec.ExitError); errors.As(err, &exitErr) {
			return string(exitErr.Stderr), fmt.Errorf("下载 Codex CLI 安装脚本失败: %w", err)
		}
		return "", fmt.Errorf("下载 Codex CLI 安装脚本失败: %w", err)
	}
	install := exec.CommandContext(ctx, "/bin/sh")
	install.Env = environment.Environ(nil)
	install.Stdin = bytes.NewReader(output)
	installOutput, err := install.CombinedOutput()
	if err != nil {
		return string(installOutput), fmt.Errorf("安装 Codex CLI 失败: %w", err)
	}
	return string(installOutput), nil
}

type Config struct {
	Path                  string
	Workspace             string
	AdditionalDirectories []string
	Model                 string
	ThreadID              string
	Timeout               time.Duration
	Env                   []string
	Skills                []SkillRef
	OnDelta               func(string)
	OnEvent               func(Event)
	OnUsage               func(Usage)
	// OnServerRequest 可选地处理 app-server 发起的反向 JSON-RPC 请求。
	// 未设置时，RunMessage 会回复标准 JSON-RPC 方法未实现错误，避免把
	// “服务器请求”误判成协议损坏并直接断开连接。
	OnServerRequest func(ServerRequest) (any, error)
}

type SkillRef struct {
	Name string
	Path string
}

type ServerRequest struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

type Usage struct {
	Reported              bool
	InputTokens           int
	OutputTokens          int
	CachedInputTokens     int
	CacheWriteInputTokens int
	ReasoningOutputTokens int
	TotalTokens           int
	ModelContextWindow    int
}

type Event struct {
	Kind           string
	Name           string
	Status         string
	Detail         string
	Input          string
	Output         string
	ActivityID     string
	ActivityKind   string
	ActivitySource string
	DisplayName    string
	Duration       time.Duration
}

type eventTimers struct {
	itemStartedAt map[string]time.Time
	turnStartedAt time.Time
}

type Result struct {
	ThreadID     string
	Answer       string
	Usage        Usage
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	Duration     time.Duration
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

const (
	maxTraceValueBytes    = 64 * 1024
	maxProgressValueBytes = 4 * 1024
)

type synchronizedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (b *synchronizedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit > 0 {
		if len(value) >= b.limit {
			b.buf.Reset()
			_, _ = b.buf.Write(value[len(value)-b.limit:])
			return len(value), nil
		}
		if overflow := b.buf.Len() + len(value) - b.limit; overflow > 0 {
			_ = b.buf.Next(overflow)
		}
	}
	return b.buf.Write(value)
}

func (b *synchronizedBuffer) Snapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// RunMessage 启动一个短生命周期 app-server 连接，并通过 thread/resume 保持会话。
// app-server 本身把 thread 持久化到 Codex Home，因此 EasyAgent 只需保存 thread id。
func RunMessage(ctx context.Context, config Config, userMessage string) (Result, error) {
	if strings.TrimSpace(config.Path) == "" {
		return Result{}, errors.New("Codex Runtime 未安装")
	}
	if strings.TrimSpace(config.Workspace) == "" {
		return Result{}, errors.New("Codex Runtime 缺少工作区")
	}
	if strings.TrimSpace(userMessage) == "" {
		return Result{}, errors.New("Codex Runtime 收到空消息")
	}
	if config.Timeout <= 0 {
		config.Timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	// 不使用 CommandContext 的自动 Kill：取消时先给 app-server 一个协议层
	// turn/interrupt 机会，随后再用进程组终止作为兜底。
	command := exec.Command(config.Path, "app-server")
	command.Dir = config.Workspace
	if len(config.Env) > 0 {
		command.Env = config.Env
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return Result{}, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return Result{}, err
	}
	configureProcessTree(command)
	var stderrTail synchronizedBuffer
	stderrTail.limit = 32 * 1024
	// 持续排空 stderr，避免 app-server/工具写满 pipe 后阻塞；只保留
	// 最后的少量内容用于错误诊断，避免异常进程无限占用内存。
	go func() { _, _ = io.Copy(&stderrTail, stderr) }()
	if err := command.Start(); err != nil {
		return Result{}, fmt.Errorf("启动 Codex app-server: %w", err)
	}
	var threadID, turnID string
	interrupt := func() {}
	defer func() {
		if ctx.Err() != nil && turnID != "" {
			interrupt()
		}
		_ = stdin.Close()
		terminateProcessTree(command)
		_ = command.Wait()
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	type readResult struct {
		message rpcMessage
		err     error
	}
	readResults := make(chan readResult, 1)
	emitReadResult := func(result readResult) bool {
		select {
		case readResults <- result:
			return true
		case <-ctx.Done():
			return false
		}
	}
	go func() {
		for scanner.Scan() {
			var message rpcMessage
			if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
				emitReadResult(readResult{err: fmt.Errorf("Codex app-server 返回无效 JSON: %w", err)})
				return
			}
			if !emitReadResult(readResult{message: message}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			emitReadResult(readResult{err: err})
			return
		}
		if stderr := strings.TrimSpace(stderrTail.Snapshot()); stderr != "" {
			emitReadResult(readResult{err: fmt.Errorf("Codex app-server 已退出: %s", stderr)})
			return
		}
		emitReadResult(readResult{err: io.EOF})
	}()
	var writeMu sync.Mutex
	write := func(value any) error {
		payload, err := json.Marshal(value)
		if err != nil {
			return err
		}
		payload = append(payload, '\n')
		writeMu.Lock()
		defer writeMu.Unlock()
		_, err = stdin.Write(payload)
		return err
	}
	send := func(method string, id int, params any) error {
		return write(struct {
			Method string `json:"method"`
			ID     int    `json:"id,omitempty"`
			Params any    `json:"params,omitempty"`
		}{Method: method, ID: id, Params: params})
	}
	interrupt = func() {
		_ = send("turn/interrupt", 99, map[string]any{"threadId": threadID, "turnId": turnID})
		// Give the protocol message a short window to be consumed before the
		// process-group kill below. This is deliberately bounded during shutdown.
		time.Sleep(120 * time.Millisecond)
	}
	sendResponse := func(id json.RawMessage, result any, responseErr error) error {
		if responseErr != nil {
			return write(struct {
				ID    json.RawMessage `json:"id"`
				Error map[string]any  `json:"error"`
			}{ID: id, Error: map[string]any{"code": -32000, "message": responseErr.Error()}})
		}
		return write(struct {
			ID     json.RawMessage `json:"id"`
			Result any             `json:"result"`
		}{ID: id, Result: result})
	}
	handleServerRequest := func(message rpcMessage) error {
		if config.OnServerRequest == nil {
			return sendResponse(message.ID, nil, fmt.Errorf("EasyAgent 未实现 app-server 方法: %s", message.Method))
		}
		result, requestErr := config.OnServerRequest(ServerRequest{ID: message.ID, Method: message.Method, Params: message.Params})
		return sendResponse(message.ID, result, requestErr)
	}
	read := func() (rpcMessage, error) {
		if err := ctx.Err(); err != nil {
			return rpcMessage{}, err
		}
		select {
		case <-ctx.Done():
			return rpcMessage{}, ctx.Err()
		case result := <-readResults:
			return result.message, result.err
		}
	}
	timers := &eventTimers{itemStartedAt: make(map[string]time.Time)}
	var latestUsage Usage
	request := func(method string, id int, params any) (json.RawMessage, error) {
		if err := send(method, id, params); err != nil {
			return nil, err
		}
		for {
			message, err := read()
			if err != nil {
				return nil, err
			}
			if len(message.ID) > 0 && message.Method != "" {
				if err := handleServerRequest(message); err != nil {
					return nil, err
				}
				continue
			}
			if len(message.ID) == 0 {
				consumeNotificationWithAnswer(message, config, &strings.Builder{}, timers, &latestUsage)
				continue
			}
			if string(message.ID) != fmt.Sprintf("%d", id) {
				continue
			}
			if len(message.Error) > 0 && string(message.Error) != "null" {
				return nil, rpcError(message.Error)
			}
			return message.Result, nil
		}
	}

	startedAt := time.Now()
	if _, err := request("initialize", 1, map[string]any{"clientInfo": map[string]string{"name": "easyagent", "title": "EasyAgent", "version": "0.1.0"}}); err != nil {
		return Result{}, err
	}
	if err := send("initialized", 0, map[string]any{}); err != nil {
		return Result{}, err
	}
	threadParams := map[string]any{
		"cwd": config.Workspace, "approvalPolicy": "never",
		// thread/start 和 thread/resume 使用 SandboxMode 字符串；只有
		// turn/start 使用 sandboxPolicy 对象。EasyAgent 的 Codex Runtime
		// 默认按产品约定使用完全访问模式。
		"sandbox": "danger-full-access",
	}
	if config.Model != "" {
		threadParams["model"] = config.Model
	}
	method := "thread/start"
	if config.ThreadID != "" {
		method = "thread/resume"
		threadParams["threadId"] = config.ThreadID
	}
	threadResult, err := request(method, 2, threadParams)
	if err != nil {
		return Result{}, err
	}
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(threadResult, &thread); err != nil || thread.Thread.ID == "" {
		return Result{}, errors.New("Codex app-server 没有返回 thread id")
	}
	threadID = thread.Thread.ID
	input := []map[string]string{{"type": "text", "text": codexCapabilityText(userMessage, config.AdditionalDirectories...)}}
	for _, skill := range config.Skills {
		if strings.TrimSpace(skill.Name) == "" || strings.TrimSpace(skill.Path) == "" {
			continue
		}
		input = append(input, map[string]string{"type": "skill", "name": skill.Name, "path": skill.Path})
	}
	turnResult, err := request("turn/start", 3, map[string]any{
		"threadId": threadID, "input": input,
		"cwd": config.Workspace, "approvalPolicy": "never",
		"sandboxPolicy": map[string]any{"type": "dangerFullAccess"},
	})
	if err != nil {
		return Result{}, err
	}
	var startedTurn struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(turnResult, &startedTurn) == nil {
		turnID = startedTurn.Turn.ID
	}

	var answer strings.Builder
	for {
		message, err := read()
		if err != nil {
			return Result{}, err
		}
		if len(message.ID) > 0 && message.Method != "" {
			if err := handleServerRequest(message); err != nil {
				return Result{}, err
			}
			continue
		}
		if message.Method == "item/agentMessage/delta" {
			var params struct {
				Delta string `json:"delta"`
			}
			if json.Unmarshal(message.Params, &params) == nil && params.Delta != "" {
				answer.WriteString(params.Delta)
				if config.OnDelta != nil {
					config.OnDelta(params.Delta)
				}
			}
			continue
		}
		if message.Method == "turn/completed" {
			var params struct {
				Turn struct {
					Status string          `json:"status"`
					Error  json.RawMessage `json:"error"`
				} `json:"turn"`
			}
			_ = json.Unmarshal(message.Params, &params)
			consumeNotificationWithAnswer(message, config, &answer, timers, &latestUsage)
			if codexStatus(params.Turn.Status) == "error" {
				return Result{}, rpcError(params.Turn.Error)
			}
			if answer.Len() == 0 {
				answer.WriteString(extractCompletedAgentText(message.Params))
			}
			if strings.TrimSpace(answer.String()) == "" {
				return Result{}, errors.New("Codex Runtime 未返回可用回答")
			}
			return Result{ThreadID: threadID, Answer: strings.TrimSpace(answer.String()), Usage: latestUsage, Duration: time.Since(startedAt)}, nil
		}
		consumeNotificationWithAnswer(message, config, &answer, timers, &latestUsage)
	}
}

func codexCapabilityText(value string, directories ...string) string {
	pattern := regexp.MustCompile(`(?i)@(skill|mcp):([a-z0-9][a-z0-9._-]*)`)
	value = strings.TrimSpace(pattern.ReplaceAllStringFunc(value, func(token string) string {
		match := pattern.FindStringSubmatch(token)
		if len(match) != 3 {
			return token
		}
		if strings.EqualFold(match[1], "skill") {
			return "$" + match[2]
		}
		return "[优先使用 MCP server easyagent_" + strings.ReplaceAll(match[2], ".", "_") + "]"
	}))
	if len(directories) == 0 {
		return value
	}
	return "<easyagent_project_sources>\nAdditional source folders in this project; keep cwd unchanged and use absolute paths when needed:\n- " + strings.Join(directories, "\n- ") + "\n</easyagent_project_sources>\n\n" + value
}

func consumeNotificationWithAnswer(message rpcMessage, config Config, answer *strings.Builder, timers *eventTimers, latestUsage *Usage) {
	if message.Method == "item/completed" && answer.Len() == 0 && isAgentMessage(message.Params) {
		answer.WriteString(extractCompletedAgentText(message.Params))
	}
	if message.Method == "thread/tokenUsage/updated" {
		usage, ok := parseTokenUsage(message.Params)
		if ok {
			if latestUsage != nil {
				*latestUsage = usage
			}
			if config.OnUsage != nil {
				config.OnUsage(usage)
			}
		}
		return
	}
	if config.OnEvent == nil {
		return
	}
	if event, ok := progressEvent(message); ok {
		config.OnEvent(event)
		return
	}
	if message.Method == "turn/started" || message.Method == "turn/completed" {
		var payload struct {
			Turn struct {
				Status string          `json:"status"`
				Error  json.RawMessage `json:"error"`
			} `json:"turn"`
		}
		if json.Unmarshal(message.Params, &payload) != nil {
			return
		}
		status := "started"
		if message.Method == "turn/completed" {
			status = codexStatus(payload.Turn.Status)
		}
		detail := ""
		if len(payload.Turn.Error) > 0 && string(payload.Turn.Error) != "null" {
			detail = rpcErrorText(payload.Turn.Error)
		}
		duration := time.Duration(0)
		if timers != nil {
			if message.Method == "turn/started" {
				timers.turnStartedAt = time.Now()
			} else if !timers.turnStartedAt.IsZero() {
				duration = time.Since(timers.turnStartedAt)
				timers.turnStartedAt = time.Time{}
			}
		}
		config.OnEvent(Event{Kind: "codex_turn", Name: "turn", Status: status, Detail: detail, Duration: duration})
		return
	}
	if message.Method != "item/started" && message.Method != "item/completed" {
		return
	}
	var payload struct {
		Item map[string]any `json:"item"`
	}
	if json.Unmarshal(message.Params, &payload) != nil {
		return
	}
	status := "success"
	if message.Method == "item/started" {
		status = "started"
	} else if itemStatus, ok := payload.Item["status"].(string); ok {
		status = codexStatus(itemStatus)
	}
	name, _ := payload.Item["type"].(string)
	itemID, _ := payload.Item["id"].(string)
	duration := time.Duration(0)
	if timers != nil && itemID != "" {
		if message.Method == "item/started" {
			timers.itemStartedAt[itemID] = time.Now()
		} else if started, ok := timers.itemStartedAt[itemID]; ok {
			duration = time.Since(started)
			delete(timers.itemStartedAt, itemID)
		}
	}
	if reported := itemDuration(payload.Item); reported > 0 {
		duration = reported
	}
	activityKind, activitySource, displayName := itemActivity(payload.Item)
	config.OnEvent(Event{Kind: "codex_item", Name: name, Status: status, Detail: itemDetail(payload.Item), Input: itemInput(payload.Item), Output: itemOutput(payload.Item), ActivityID: itemID, ActivityKind: activityKind, ActivitySource: activitySource, DisplayName: displayName, Duration: duration})
}

// progressEvent maps high-volume app-server notifications into bounded Trace
// rows. Keeping the protocol name in Name makes new Codex notifications
// observable even before EasyAgent gets a dedicated UI renderer for them.
func progressEvent(message rpcMessage) (Event, bool) {
	progressNames := map[string]string{
		"turn/plan/updated":                 "plan",
		"item/plan/delta":                   "plan",
		"item/commandExecution/outputDelta": "commandExecution",
		"item/fileChange/outputDelta":       "fileChange",
		"item/fileChange/patchUpdated":      "fileChange",
		"item/mcpToolCall/progress":         "mcpToolCall",
		"item/reasoning/summaryTextDelta":   "reasoning",
		"item/reasoning/summaryPartAdded":   "reasoning",
		"thread/status/changed":             "thread",
		"serverRequest/resolved":            "serverRequest",
	}
	name, ok := progressNames[message.Method]
	if !ok {
		return Event{}, false
	}
	var payload map[string]any
	if json.Unmarshal(message.Params, &payload) != nil {
		return Event{}, false
	}
	if message.Method == "turn/plan/updated" {
		detail, displayName := planProgressSummary(payload)
		return Event{
			Kind: "codex_progress", Name: "plan", Status: "updated", Detail: detail,
			Output:     marshalItemValue(map[string]any{"explanation": payload["explanation"], "plan": payload["plan"]}),
			ActivityID: firstString(payload, "turnId"), ActivityKind: "plan", ActivitySource: "codex", DisplayName: displayName,
		}, true
	}
	detail := firstString(payload, "delta", "outputDelta", "text", "status", "message")
	output := firstString(payload, "patch", "output", "delta", "outputDelta")
	input := ""
	status := "progress"
	if message.Method == "thread/status/changed" {
		status = "updated"
	}
	if message.Method == "serverRequest/resolved" {
		status = "resolved"
	}
	activityID := firstString(payload, "itemId")
	activityKind := ""
	activitySource := ""
	displayName := ""
	if name == "mcpToolCall" {
		activityKind = "mcp"
	} else if name == "commandExecution" || name == "fileChange" {
		activityKind, activitySource = "tool", "codex"
		if name == "commandExecution" {
			displayName = "Shell"
		} else {
			displayName = "文件修改"
			if changes, exists := payload["changes"]; exists {
				input = marshalItemValue(changes)
			}
		}
	}
	return Event{Kind: "codex_progress", Name: name, Status: status, Detail: detail, Input: input, Output: output, ActivityID: activityID, ActivityKind: activityKind, ActivitySource: activitySource, DisplayName: displayName}, true
}

func planProgressSummary(payload map[string]any) (string, string) {
	items, _ := payload["plan"].([]any)
	if len(items) == 0 {
		return "更新计划", "计划"
	}
	current := len(items) - 1
	for index, raw := range items {
		item, _ := raw.(map[string]any)
		if status, _ := item["status"].(string); status == "inProgress" {
			current = index
			break
		}
		if status, _ := item["status"].(string); status == "pending" {
			current = index
			break
		}
	}
	item, _ := items[current].(map[string]any)
	step, _ := item["step"].(string)
	step = truncateUTF8(strings.TrimSpace(step), maxProgressValueBytes/2)
	if step == "" {
		step = "处理任务"
	}
	return fmt.Sprintf("第 %d/%d 步 · %s", current+1, len(items), step), step
}

func itemActivity(item map[string]any) (kind, source, displayName string) {
	typeName, _ := item["type"].(string)
	switch typeName {
	case "mcpToolCall":
		source, _ = item["server"].(string)
		displayName, _ = item["tool"].(string)
		return "mcp", strings.TrimSpace(source), strings.TrimSpace(displayName)
	case "commandExecution":
		return "tool", "codex", "Shell"
	case "fileChange":
		return "tool", "codex", "文件修改"
	case "webSearch":
		return "tool", "codex", "Web Search"
	case "imageView":
		return "tool", "codex", "图片查看"
	case "dynamicToolCall":
		displayName, _ = item["tool"].(string)
		if strings.TrimSpace(displayName) == "" {
			displayName, _ = item["name"].(string)
		}
		return "tool", "codex", strings.TrimSpace(displayName)
	default:
		return "", "", ""
	}
}

func firstString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
			return truncateUTF8(strings.TrimSpace(text), maxProgressValueBytes)
		}
	}
	return ""
}

func parseTokenUsage(raw json.RawMessage) (Usage, bool) {
	var payload struct {
		TokenUsage struct {
			Last struct {
				InputTokens           int `json:"inputTokens"`
				OutputTokens          int `json:"outputTokens"`
				CachedInputTokens     int `json:"cachedInputTokens"`
				CacheWriteInputTokens int `json:"cacheWriteInputTokens"`
				ReasoningOutputTokens int `json:"reasoningOutputTokens"`
				TotalTokens           int `json:"totalTokens"`
			} `json:"last"`
			Total struct {
				InputTokens           int `json:"inputTokens"`
				OutputTokens          int `json:"outputTokens"`
				CachedInputTokens     int `json:"cachedInputTokens"`
				CacheWriteInputTokens int `json:"cacheWriteInputTokens"`
				ReasoningOutputTokens int `json:"reasoningOutputTokens"`
				TotalTokens           int `json:"totalTokens"`
			} `json:"total"`
			ModelContextWindow *int `json:"modelContextWindow"`
		} `json:"tokenUsage"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return Usage{}, false
	}
	last := payload.TokenUsage.Last
	usage := Usage{
		Reported:              true,
		InputTokens:           last.InputTokens,
		OutputTokens:          last.OutputTokens,
		CachedInputTokens:     last.CachedInputTokens,
		CacheWriteInputTokens: last.CacheWriteInputTokens,
		ReasoningOutputTokens: last.ReasoningOutputTokens,
		TotalTokens:           last.TotalTokens,
	}
	if payload.TokenUsage.ModelContextWindow != nil {
		usage.ModelContextWindow = *payload.TokenUsage.ModelContextWindow
	}
	return usage, true
}

func codexStatus(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(strings.ReplaceAll(normalized, "_", ""), "-", "")
	switch normalized {
	case "inprogress", "started", "running":
		return "started"
	case "failed", "error", "declined", "interrupted", "canceled", "cancelled":
		return "error"
	default:
		return "success"
	}
}

func rpcErrorText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &value) == nil && value.Message != "" {
		return value.Message
	}
	return string(raw)
}

func itemDetail(item map[string]any) string {
	if query, ok := item["query"].(string); ok && strings.TrimSpace(query) != "" {
		return "搜索：" + query
	}
	if action, ok := item["action"].(map[string]any); ok {
		if actionType, ok := action["type"].(string); ok && strings.TrimSpace(actionType) != "" {
			return actionType
		}
	}
	if item["type"] == "mcpToolCall" {
		server, _ := item["server"].(string)
		tool, _ := item["tool"].(string)
		if server != "" || tool != "" {
			return strings.Trim(strings.TrimSpace(server)+" / "+strings.TrimSpace(tool), " / ")
		}
	}
	for _, key := range []string{"reason", "cwd", "phase", "serverName", "server"} {
		if value, ok := item[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	if summary, ok := item["summary"]; ok {
		value := marshalItemValue(summary)
		if value != "" && value != "[]" && value != "{}" && value != "null" {
			return value
		}
	}
	return ""
}

func itemInput(item map[string]any) string {
	for _, key := range []string{"query", "command", "arguments", "changes", "review", "action"} {
		if value, ok := item[key]; ok {
			return marshalItemValue(value)
		}
	}
	return ""
}

func itemOutput(item map[string]any) string {
	for _, key := range []string{"text", "aggregatedOutput", "output", "review", "contentItems", "result", "error"} {
		if value, ok := item[key]; ok {
			return marshalItemValue(value)
		}
	}
	return ""
}

func itemDuration(item map[string]any) time.Duration {
	value, ok := item["durationMs"]
	if !ok {
		return 0
	}
	switch number := value.(type) {
	case float64:
		return time.Duration(number * float64(time.Millisecond))
	case int:
		return time.Duration(number) * time.Millisecond
	case int64:
		return time.Duration(number) * time.Millisecond
	default:
		return 0
	}
}

func marshalItemValue(value any) string {
	var result string
	if text, ok := value.(string); ok {
		result = text
	} else {
		data, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		result = string(data)
	}
	return truncateUTF8(result, maxTraceValueBytes)
}

func truncateUTF8(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	const suffix = "… [truncated]"
	if limit <= len(suffix) {
		cut := limit
		for cut > 0 && !utf8.ValidString(value[:cut]) {
			cut--
		}
		return value[:cut]
	}
	cut := limit - len(suffix)
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut] + suffix
}

func isAgentMessage(raw json.RawMessage) bool {
	var payload struct {
		Item struct {
			Type string `json:"type"`
		} `json:"item"`
	}
	return json.Unmarshal(raw, &payload) == nil && payload.Item.Type == "agentMessage"
}

func extractCompletedAgentText(raw json.RawMessage) string {
	var payload struct {
		Item map[string]any `json:"item"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	if value, ok := payload.Item["text"].(string); ok {
		return value
	}
	if content, ok := payload.Item["content"].([]any); ok {
		var result strings.Builder
		for _, entry := range content {
			if item, ok := entry.(map[string]any); ok {
				if value, ok := item["text"].(string); ok {
					result.WriteString(value)
				}
			}
		}
		return result.String()
	}
	return ""
}

func rpcError(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return errors.New("Codex Runtime 未返回可用回答")
	}
	var value struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &value) == nil && value.Message != "" {
		return errors.New(value.Message)
	}
	return fmt.Errorf("Codex Runtime 请求失败: %s", string(raw))
}
