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
	"runtime"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/appenv"
)

const installDocsURL = "https://developers.openai.com/codex/cli"

type Status struct {
	Installed      bool   `json:"installed"`
	Path           string `json:"path,omitempty"`
	Version        string `json:"version,omitempty"`
	Message        string `json:"message"`
	InstallCommand string `json:"installCommand"`
	InstallURL     string `json:"installUrl"`
}

// Detect 只做本机文件和 --version 检查，不启动 app-server，也不修改用户环境。
func Detect(environment *appenv.Environment) Status {
	status := Status{InstallURL: installDocsURL, InstallCommand: "curl -fsSL https://chatgpt.com/codex/install.sh | sh"}
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
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
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
		status.Message = "Codex Runtime 已就绪"
		return status
	}
	status.Message = "未检测到 Codex CLI；安装后点击重新检测"
	return status
}

type Config struct {
	Path      string
	Workspace string
	Model     string
	ThreadID  string
	Timeout   time.Duration
	Env       []string
	OnDelta   func(string)
	OnEvent   func(Event)
}

type Event struct {
	Kind     string
	Name     string
	Status   string
	Detail   string
	Input    string
	Output   string
	Duration time.Duration
}

type Result struct {
	ThreadID     string
	Answer       string
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

	command := exec.CommandContext(ctx, config.Path, "app-server")
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
	var stderrTail bytes.Buffer
	go func() { _, _ = io.CopyN(&stderrTail, stderr, 32*1024) }()
	if err := command.Start(); err != nil {
		return Result{}, fmt.Errorf("启动 Codex app-server: %w", err)
	}
	defer func() { _ = stdin.Close(); _ = command.Process.Kill(); _ = command.Wait() }()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	send := func(method string, id int, params any) error {
		payload, err := json.Marshal(struct {
			Method string `json:"method"`
			ID     int    `json:"id,omitempty"`
			Params any    `json:"params,omitempty"`
		}{Method: method, ID: id, Params: params})
		if err != nil {
			return err
		}
		payload = append(payload, '\n')
		_, err = stdin.Write(payload)
		return err
	}
	read := func() (rpcMessage, error) {
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return rpcMessage{}, err
			}
			if stderrTail.Len() > 0 {
				return rpcMessage{}, fmt.Errorf("Codex app-server 已退出: %s", strings.TrimSpace(stderrTail.String()))
			}
			return rpcMessage{}, io.EOF
		}
		var message rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			return rpcMessage{}, fmt.Errorf("Codex app-server 返回无效 JSON: %w", err)
		}
		return message, nil
	}
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
				return nil, fmt.Errorf("Codex Runtime 请求了 EasyAgent 尚未支持的交互: %s", message.Method)
			}
			if len(message.ID) == 0 {
				consumeNotification(message, config)
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
		"sandboxPolicy": map[string]any{"type": "workspaceWrite", "writableRoots": []string{config.Workspace}, "networkAccess": true},
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
	threadID := thread.Thread.ID
	if _, err := request("turn/start", 3, map[string]any{
		"threadId": threadID, "input": []map[string]string{{"type": "text", "text": userMessage}},
		"cwd": config.Workspace, "approvalPolicy": "never", "sandboxPolicy": threadParams["sandboxPolicy"],
	}); err != nil {
		return Result{}, err
	}

	var answer strings.Builder
	for {
		message, err := read()
		if err != nil {
			return Result{}, err
		}
		if len(message.ID) > 0 {
			return Result{}, fmt.Errorf("Codex Runtime 请求了 EasyAgent 尚未支持的交互: %s", message.Method)
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
			if params.Turn.Status == "failed" || params.Turn.Status == "interrupted" {
				return Result{}, rpcError(params.Turn.Error)
			}
			if answer.Len() == 0 {
				answer.WriteString(extractCompletedAgentText(message.Params))
			}
			if strings.TrimSpace(answer.String()) == "" {
				return Result{}, errors.New("Codex Runtime 未返回可用回答")
			}
			return Result{ThreadID: threadID, Answer: strings.TrimSpace(answer.String()), Duration: time.Since(startedAt)}, nil
		}
		consumeNotificationWithAnswer(message, config, &answer)
	}
}

func consumeNotification(message rpcMessage, config Config) {
	var ignored strings.Builder
	consumeNotificationWithAnswer(message, config, &ignored)
}

func consumeNotificationWithAnswer(message rpcMessage, config Config, answer *strings.Builder) {
	if message.Method == "item/completed" && answer.Len() == 0 {
		answer.WriteString(extractCompletedAgentText(message.Params))
	}
	if config.OnEvent == nil || (message.Method != "item/started" && message.Method != "item/completed") {
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
	}
	name, _ := payload.Item["type"].(string)
	config.OnEvent(Event{Kind: "codex_item", Name: name, Status: status})
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
