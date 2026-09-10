//go:build !darwin && !linux

package tools

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/lakernote/easy-agent/internal/permissions"
)

func newPermissionedShellCommand(ctx context.Context, script string, policy permissions.Policy, _ []string) (*exec.Cmd, error) {
	if policy.IsFullAccess() {
		return newShellCommand(ctx, script), nil
	}
	return nil, fmt.Errorf("当前系统暂不提供 EasyAgent 工作区沙盒；请选择“完全访问”")
}
