package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/appenv"
	"github.com/lakernote/easy-agent/internal/permissions"
)

const (
	defaultShellTimeout = 60 * time.Second
	maxShellTimeout     = 5 * time.Minute
	maxShellOutputBytes = 64 * 1024
)

// shellTool 是 EasyAgent 的通用本地执行底座。模型可以用它运行构建、测试、
// 项目脚本和系统已有的 CLI。每次调用的命令、结果、耗时都会由 Runner 写入 Trace。
func shellTool(environment *appenv.Environment) agent.Tool {
	return shellToolWithPolicy(environment, permissions.Default())
}

func shellToolWithPolicy(environment *appenv.Environment, policy permissions.Policy) agent.Tool {
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name:        "shell",
			Description: "在 EasyAgent 工作区执行确实需要运行的构建、测试、Git、脚本、CLI 或安装命令，并返回真实退出码与输出。不要用于普通问答或仅生成命令示例；文件读取、搜索和小范围修改优先使用专用文件工具。",
			Parameters: objectSchema(map[string]any{
				"command": stringSchema("必填，要执行的完整 Shell 命令"),
				"working_directory": map[string]any{
					"type": "string", "description": "可选工作目录；相对路径以 EasyAgent 工作区为基准",
				},
				"timeout_seconds": map[string]any{
					"type": "integer", "description": "可选超时秒数，默认 60，最大 300", "minimum": 1, "maximum": 300,
				},
			}, []string{"command"}),
		},
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			return runShellWithPolicy(ctx, environment, policy, raw)
		},
	}
}

func runShell(parent context.Context, environment *appenv.Environment, raw json.RawMessage) (string, error) {
	return runShellWithPolicy(parent, environment, permissions.Default(), raw)
}

func runShellWithPolicy(parent context.Context, environment *appenv.Environment, policy permissions.Policy, raw json.RawMessage) (string, error) {
	policy = policy.Normalize()
	if policy.IsReadOnly() {
		return "", errors.New("当前权限模式为只读，EasyAgent 已禁用 Shell")
	}
	var arguments struct {
		Command          string `json:"command"`
		WorkingDirectory string `json:"working_directory"`
		TimeoutSeconds   int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return "", fmt.Errorf("Shell 参数错误: %w", err)
	}
	arguments.Command = strings.TrimSpace(arguments.Command)
	if arguments.Command == "" {
		return "", errors.New("Shell command 不能为空")
	}

	timeout := defaultShellTimeout
	if arguments.TimeoutSeconds > 0 {
		timeout = time.Duration(arguments.TimeoutSeconds) * time.Second
	}
	if timeout > maxShellTimeout {
		return "", fmt.Errorf("Shell 最长只能运行 %d 秒", int(maxShellTimeout/time.Second))
	}

	directory := strings.TrimSpace(arguments.WorkingDirectory)
	directory, err := environment.ResolveWorkspacePath(directory)
	if err != nil {
		return "", fmt.Errorf("解析工作目录失败: %w", err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		return "", fmt.Errorf("工作目录不可用: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("工作目录不是目录: %s", directory)
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	roots := append([]string{environment.Workspace()}, environment.Directories()...)
	command, err := newPermissionedShellCommand(ctx, arguments.Command, policy, policy.EffectiveWritableRoots(roots))
	if err != nil {
		return "", err
	}
	command.Dir = directory
	command.Env = environment.Environ(nil)
	command.WaitDelay = 2 * time.Second
	stdout := newOutputCapture(maxShellOutputBytes)
	stderr := newOutputCapture(maxShellOutputBytes)
	command.Stdout = stdout
	command.Stderr = stderr
	startedAt := time.Now()
	err = command.Run()
	duration := time.Since(startedAt)

	exitCode := 0
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	if timedOut {
		exitCode = 124
	} else if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		} else {
			return "", fmt.Errorf("启动 Shell 失败: %w", err)
		}
	}

	result := struct {
		OK               bool   `json:"ok"`
		Command          string `json:"command"`
		WorkingDirectory string `json:"working_directory"`
		ExitCode         int    `json:"exit_code"`
		TimedOut         bool   `json:"timed_out"`
		DurationMS       int64  `json:"duration_ms"`
		Stdout           string `json:"stdout"`
		Stderr           string `json:"stderr"`
		Error            string `json:"error,omitempty"`
	}{
		OK: exitCode == 0 && !timedOut, Command: arguments.Command, WorkingDirectory: directory, ExitCode: exitCode,
		TimedOut: timedOut, DurationMS: duration.Milliseconds(), Stdout: stdout.String(), Stderr: stderr.String(),
	}
	if timedOut {
		result.Error = fmt.Sprintf("Shell 运行超过 %d 秒，已终止", int(timeout/time.Second))
	} else if exitCode != 0 {
		result.Error = fmt.Sprintf("Shell 命令执行失败，退出码 %d", exitCode)
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	if timedOut {
		return string(encoded), fmt.Errorf("Shell 运行超过 %d 秒，已终止", int(timeout/time.Second))
	}
	if parent.Err() != nil {
		return string(encoded), fmt.Errorf("Shell 已取消: %w", parent.Err())
	}
	if exitCode != 0 {
		return string(encoded), errors.New(result.Error)
	}
	return string(encoded), nil
}

// outputCapture 保留输出的开头和结尾，避免某个失控命令耗尽服务器内存；
// 错误通常位于输出末尾，因此不能只做简单的前缀截断。
type outputCapture struct {
	limit int
	total int64
	head  []byte
	tail  []byte
}

func newOutputCapture(limit int) *outputCapture {
	return &outputCapture{limit: limit}
}

func (capture *outputCapture) Write(value []byte) (int, error) {
	originalLength := len(value)
	capture.total += int64(originalLength)
	headLimit := capture.limit / 2
	if len(capture.head) < headLimit {
		count := min(headLimit-len(capture.head), len(value))
		capture.head = append(capture.head, value[:count]...)
		value = value[count:]
	}
	if len(value) > 0 {
		tailLimit := capture.limit - headLimit
		if len(value) >= tailLimit {
			capture.tail = append(capture.tail[:0], value[len(value)-tailLimit:]...)
		} else {
			overflow := len(capture.tail) + len(value) - tailLimit
			if overflow > 0 {
				capture.tail = append(capture.tail[:0], capture.tail[overflow:]...)
			}
			capture.tail = append(capture.tail, value...)
		}
	}
	return originalLength, nil
}

func (capture *outputCapture) String() string {
	if capture.total <= int64(capture.limit) {
		return string(append(append([]byte{}, capture.head...), capture.tail...))
	}
	marker := fmt.Sprintf("\n\n... EasyAgent 已截断 %d 字节输出 ...\n\n", capture.total-int64(capture.limit))
	return string(capture.head) + marker + string(capture.tail)
}
