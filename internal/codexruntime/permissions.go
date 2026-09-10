package codexruntime

import "github.com/lakernote/easy-agent/internal/permissions"

// codexSandboxMode is the legacy thread/start value. turn/start uses the
// object form below; keeping this translation here prevents protocol details
// from leaking into the server and EasyAgent settings.
func codexSandboxMode(policy permissions.Policy) string {
	switch policy.Normalize().Mode {
	case permissions.ModeReadOnly:
		return "read-only"
	case permissions.ModeWorkspaceWrite:
		return "workspace-write"
	case permissions.ModeCustom:
		return "workspace-write"
	default:
		return "danger-full-access"
	}
}

func codexSandboxPolicy(policy permissions.Policy, defaults []string) map[string]any {
	policy = policy.Normalize()
	switch policy.Mode {
	case permissions.ModeReadOnly:
		return map[string]any{"type": "readOnly"}
	case permissions.ModeWorkspaceWrite:
		return map[string]any{"type": "workspaceWrite", "writableRoots": defaults, "networkAccess": policy.NetworkAccess}
	case permissions.ModeCustom:
		return map[string]any{"type": "workspaceWrite", "writableRoots": policy.EffectiveWritableRoots(defaults), "networkAccess": policy.NetworkAccess}
	default:
		return map[string]any{"type": "dangerFullAccess"}
	}
}
