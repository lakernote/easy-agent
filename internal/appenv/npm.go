package appenv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// InstallNPMPackage 把 MCP 的 Node 包安装到 EasyAgent 私有 runtime/mcp 目录，
// 不写全局 npm 目录，也不污染用户项目的 package.json/node_modules。
func (environment *Environment) InstallNPMPackage(ctx context.Context, id, packageSpec, executable string) (string, error) {
	packageSpec = strings.TrimSpace(packageSpec)
	executable = strings.TrimSpace(executable)
	destination, err := environment.npmPackageDirectory(id)
	if err != nil || packageSpec == "" || executable == "" {
		return "", fmt.Errorf("NPM MCP 安装参数不完整")
	}
	npm, err := environment.ResolveCommand("npm")
	if err != nil {
		return "", fmt.Errorf("找不到 npm；请安装 Node.js，并确认登录 Shell 的 PATH 可以找到 npm: %w", err)
	}
	command := exec.CommandContext(ctx, npm, "install", "--prefix", destination, "--omit=dev", "--no-audit", "--no-fund", "--save-exact", packageSpec)
	command.Dir = environment.runtime
	command.Env = environment.Environ(nil)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 4000 {
			message = message[len(message)-4000:]
		}
		return "", fmt.Errorf("安装 %s 失败: %w\n%s", packageSpec, err, message)
	}
	reference := "@runtime/mcp/" + id + "/node_modules/.bin/" + executable
	if _, err := environment.ResolveCommand(reference); err != nil {
		return "", fmt.Errorf("%s 安装完成但缺少可执行文件 %s: %w", packageSpec, executable, err)
	}
	return reference, nil
}

// UninstallNPMPackage 只删除 EasyAgent 自己的 runtime/mcp/<id>。它不会执行 npm
// 全局卸载，也不会接触用户项目中的 package.json 或 node_modules。
func (environment *Environment) UninstallNPMPackage(id string) error {
	destination, err := environment.npmPackageDirectory(id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(destination); err != nil {
		return fmt.Errorf("卸载私有 MCP 包: %w", err)
	}
	return nil
}

func (environment *Environment) npmPackageDirectory(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, `/\\`) || id == "." || id == ".." {
		return "", errors.New("MCP ID 不合法")
	}
	root := filepath.Join(environment.runtime, "mcp")
	destination := filepath.Join(root, id)
	relative, err := filepath.Rel(root, destination)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("MCP 私有安装路径不合法")
	}
	return destination, nil
}
