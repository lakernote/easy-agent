// Package permissions contains the runtime-independent permission contract.
//
// EasyAgent and Codex have different execution engines, but users should not
// have to learn two permission vocabularies. The adapters translate this
// policy into their native tool/sandbox controls.
package permissions

import (
	"fmt"
	"path/filepath"
	"strings"
)

type Mode string

const (
	ModeReadOnly       Mode = "read_only"
	ModeWorkspaceWrite Mode = "workspace_write"
	ModeFullAccess     Mode = "full_access"
	ModeCustom         Mode = "custom"
)

type ApprovalPolicy string

const (
	ApprovalOnRequest ApprovalPolicy = "on-request"
	ApprovalNever     ApprovalPolicy = "never"
)

// Policy is persisted in RuntimeSettings and is shared by both runtimes.
// WritableRoots are absolute paths; an empty list means the current session
// workspace and its project source directories.
type Policy struct {
	Mode          Mode           `json:"mode"`
	Approval      ApprovalPolicy `json:"approval"`
	WritableRoots []string       `json:"writableRoots,omitempty"`
	NetworkAccess bool           `json:"networkAccess"`
}

func Default() Policy {
	return Policy{Mode: ModeFullAccess, Approval: ApprovalNever, NetworkAccess: true}
}

func (policy Policy) Normalize() Policy {
	if policy.Mode == "" {
		policy.Mode = ModeFullAccess
	}
	if policy.Approval == "" {
		if policy.Mode == ModeFullAccess {
			policy.Approval = ApprovalNever
		} else {
			policy.Approval = ApprovalOnRequest
		}
	}
	roots := make([]string, 0, len(policy.WritableRoots))
	seen := make(map[string]struct{}, len(policy.WritableRoots))
	for _, root := range policy.WritableRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if absolute, err := filepath.Abs(root); err == nil {
			root = filepath.Clean(absolute)
		}
		if _, exists := seen[root]; exists {
			continue
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	policy.WritableRoots = roots
	return policy
}

func (policy Policy) Validate() error {
	policy = policy.Normalize()
	switch policy.Mode {
	case ModeReadOnly, ModeWorkspaceWrite, ModeFullAccess, ModeCustom:
	default:
		return fmt.Errorf("不支持的权限模式: %q", policy.Mode)
	}
	switch policy.Approval {
	case ApprovalOnRequest, ApprovalNever:
	default:
		return fmt.Errorf("不支持的审批策略: %q", policy.Approval)
	}
	if policy.Mode == ModeReadOnly && len(policy.WritableRoots) > 0 {
		return fmt.Errorf("只读模式不能配置可写目录")
	}
	return nil
}

func (policy Policy) IsReadOnly() bool { return policy.Normalize().Mode == ModeReadOnly }

func (policy Policy) IsFullAccess() bool { return policy.Normalize().Mode == ModeFullAccess }

// EffectiveWritableRoots returns the roots that the EasyAgent adapter should
// enforce for a session. A caller supplies workspace-derived defaults so a
// global policy remains valid for per-session worktrees.
func (policy Policy) EffectiveWritableRoots(defaults []string) []string {
	policy = policy.Normalize()
	if len(policy.WritableRoots) > 0 {
		return append([]string(nil), policy.WritableRoots...)
	}
	return append([]string(nil), defaults...)
}
