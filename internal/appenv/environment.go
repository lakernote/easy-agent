// Package appenv 管理 EasyAgent 自己的数据目录、工作区和命令运行环境。
//
// 服务进程可能由 systemd、launchd 或 Docker 启动，此时进程 CWD 和 PATH 都不可靠。
// 这个包在启动时把这些隐式状态解析成明确的绝对路径，后续 Tool、MCP 和依赖安装
// 都只使用同一份 Environment，避免“终端能运行、服务却找不到命令”的问题。
package appenv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Config 是应用内部使用的环境配置。正式启动时不把 Home 和 Workspace 暴露成
// 命令行参数；字段主要用于测试以及为某个会话派生独立工作区。
type Config struct {
	Home       string
	Workspace  string
	ExtraPaths []string
}

// Environment 是整个进程共享的、已经规范化的运行环境。
type Environment struct {
	home        string
	workspace   string
	directories []string
	runtime     string
	bin         string
	path        string

	commandMu    sync.Mutex
	commandCache map[string]string
}

// Open 解析目录、创建 EasyAgent 自己需要的目录，并生成确定性的 PATH。
func Open(config Config) (*Environment, error) {
	home, err := configuredHome(config.Home)
	if err != nil {
		return nil, err
	}
	workspace, err := configuredWorkspace(config.Workspace, home)
	if err != nil {
		return nil, err
	}
	runtimeDirectory := filepath.Join(home, "runtime")
	binDirectory := filepath.Join(runtimeDirectory, "bin")
	mcpDirectory := filepath.Join(runtimeDirectory, "mcp")
	for _, directory := range []string{home, workspace, runtimeDirectory, binDirectory, mcpDirectory} {
		// EasyAgent Home、运行时和默认工作区可能包含凭证、数据库及工具输出，
		// 新建目录默认只允许当前用户访问；已有用户工作区不强行改权限。
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, fmt.Errorf("创建 EasyAgent 目录 %s: %w", directory, err)
		}
	}
	privateDirectories := []string{home, runtimeDirectory, binDirectory, mcpDirectory}
	if strings.TrimSpace(config.Workspace) == "" {
		privateDirectories = append(privateDirectories, workspace)
	}
	for _, directory := range privateDirectories {
		if err := os.Chmod(directory, 0o700); err != nil {
			return nil, fmt.Errorf("保护 EasyAgent 目录 %s: %w", directory, err)
		}
	}

	return &Environment{
		home:         home,
		workspace:    workspace,
		runtime:      runtimeDirectory,
		bin:          binDirectory,
		path:         buildPath(binDirectory, config.ExtraPaths, discoveredLoginPath(), os.Getenv("PATH")),
		commandCache: make(map[string]string),
	}, nil
}

func configuredHome(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("读取用户目录: %w", err)
		}
		value = filepath.Join(userHome, ".easyagent")
	}
	return cleanAbsolute(value)
}

func configuredWorkspace(value, home string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = filepath.Join(home, "workspaces", "default")
	}
	return cleanAbsolute(value)
}

func cleanAbsolute(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	}
	return absolute, nil
}

func buildPath(binDirectory string, extraPaths []string, discovered, inherited string) string {
	values := []string{binDirectory}
	values = append(values, extraPaths...)
	values = append(values, filepath.SplitList(discovered)...)
	values = append(values, filepath.SplitList(inherited)...)

	seen := make(map[string]struct{})
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		absolute, err := filepath.Abs(value)
		if err == nil {
			value = filepath.Clean(absolute)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return strings.Join(result, string(os.PathListSeparator))
}

var (
	loginPathOnce sync.Once
	loginPath     string
)

// discoveredLoginPath 只在进程启动后读取一次登录 Shell 的 PATH。服务管理器通常
// 只给 /usr/bin:/bin，而开发工具可能由 Homebrew、nodenv 或 asdf 安装。这里仅
// 读取固定的 PATH 输出，不执行用户输入；结果随后被冻结进 Environment。
func discoveredLoginPath() string {
	loginPathOnce.Do(func() {
		shell := strings.TrimSpace(os.Getenv("SHELL"))
		if shell == "" || !filepath.IsAbs(shell) {
			return
		}
		if _, err := executableFile(shell); err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, shell, "-lc", `printf '%s' "$PATH"`)
		output, err := command.Output()
		if err == nil {
			loginPath = strings.TrimSpace(string(output))
		}
	})
	return loginPath
}

func (environment *Environment) Home() string      { return environment.home }
func (environment *Environment) Workspace() string { return environment.workspace }
func (environment *Environment) Directories() []string {
	return append([]string(nil), environment.directories...)
}
func (environment *Environment) Runtime() string { return environment.runtime }
func (environment *Environment) Bin() string     { return environment.bin }
func (environment *Environment) Path() string    { return environment.path }

// WithWorkspace 为一次会话选择工作区。空值表示使用 EasyAgent 的默认工作区；
// 用户提供的目录必须已经存在且必须是目录。返回新 Environment，避免并发会话
// 修改进程级状态，也避免一个会话的工作区泄漏到另一个会话。
func (environment *Environment) WithWorkspace(value string) (*Environment, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = environment.workspace
	} else if value == "~" || strings.HasPrefix(value, "~"+string(filepath.Separator)) {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("读取用户目录: %w", err)
		}
		value = filepath.Join(userHome, strings.TrimPrefix(strings.TrimPrefix(value, "~"), string(filepath.Separator)))
	}
	workspace, err := cleanAbsolute(value)
	if err != nil {
		return nil, fmt.Errorf("解析工作区: %w", err)
	}
	info, err := os.Stat(workspace)
	if err != nil {
		return nil, fmt.Errorf("工作区不可用 %s: %w", workspace, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("工作区不是目录: %s", workspace)
	}
	return &Environment{
		home:         environment.home,
		workspace:    workspace,
		runtime:      environment.runtime,
		bin:          environment.bin,
		path:         environment.path,
		commandCache: make(map[string]string),
	}, nil
}

// WithDirectories 扩展当前项目的源文件夹范围。相对路径仍以主工作区解析，
// 额外源文件夹必须使用绝对路径，因此不会悄悄改变命令的默认 cwd。
func (environment *Environment) WithDirectories(values []string) (*Environment, error) {
	directories := make([]string, 0, len(values))
	seen := map[string]struct{}{filepath.Clean(environment.workspace): {}}
	for _, value := range values {
		derived, err := environment.WithWorkspace(value)
		if err != nil {
			return nil, err
		}
		absolute := filepath.Clean(derived.Workspace())
		if _, exists := seen[absolute]; exists {
			continue
		}
		seen[absolute] = struct{}{}
		directories = append(directories, absolute)
	}
	return &Environment{
		home: environment.home, workspace: environment.workspace, directories: directories,
		runtime: environment.runtime, bin: environment.bin, path: environment.path,
		commandCache: make(map[string]string),
	}, nil
}

// Environ 返回给 Shell 和 MCP 子进程使用的环境变量。PATH 总是来自 Environment，
// extra 中的同名值会覆盖进程环境，但不能覆盖这个确定性 PATH。
func (environment *Environment) Environ(extra map[string]string) []string {
	values := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, found := strings.Cut(item, "=")
		if found {
			values[key] = value
		}
	}
	for key, value := range extra {
		values[key] = value
	}
	values["PATH"] = environment.path

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

// ResolveWorkspacePath 将相对路径固定解析到主工作区；绝对路径也可位于当前
// Project 配置的其他源文件夹中。
func (environment *Environment) ResolveWorkspacePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "." {
		return environment.workspace, nil
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(environment.workspace, value)
	}
	absolute, err := cleanAbsolute(value)
	if err != nil {
		return "", err
	}
	for _, root := range append([]string{environment.workspace}, environment.directories...) {
		relative, relativeErr := filepath.Rel(root, absolute)
		if relativeErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return absolute, nil
		}
	}
	return "", errors.New("路径必须位于当前项目的源文件夹中")
}

// ResolveCommand 只根据 EasyAgent 的 PATH 查找命令；不会依赖进程当前目录。
func (environment *Environment) ResolveCommand(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("命令名不能为空")
	}
	if strings.HasPrefix(name, "@runtime/") {
		return executableFile(filepath.Join(environment.runtime, strings.TrimPrefix(name, "@runtime/")))
	}
	if strings.ContainsRune(name, filepath.Separator) {
		if !filepath.IsAbs(name) {
			name = filepath.Join(environment.workspace, name)
		}
		return executableFile(name)
	}

	environment.commandMu.Lock()
	defer environment.commandMu.Unlock()
	if cached, ok := environment.commandCache[name]; ok {
		return cached, nil
	}
	for _, directory := range filepath.SplitList(environment.path) {
		candidate := filepath.Join(directory, name)
		if runtime.GOOS == "windows" {
			candidate += ".exe"
		}
		if resolved, err := executableFile(candidate); err == nil {
			environment.commandCache[name] = resolved
			return resolved, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func executableFile(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("%s 不是可执行文件", absolute)
	}
	return filepath.Clean(absolute), nil
}
