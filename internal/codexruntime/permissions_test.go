package codexruntime

import (
	"reflect"
	"testing"

	"github.com/lakernote/easy-agent/internal/permissions"
)

func TestPermissionPolicyMapsToAppServer(t *testing.T) {
	roots := []string{"/workspace", "/shared"}
	tests := []struct {
		mode       permissions.Mode
		legacy     string
		turnType   string
		wantFields map[string]any
	}{
		{permissions.ModeReadOnly, "read-only", "readOnly", map[string]any{"type": "readOnly"}},
		{permissions.ModeWorkspaceWrite, "workspace-write", "workspaceWrite", map[string]any{"type": "workspaceWrite", "writableRoots": roots, "networkAccess": false}},
		{permissions.ModeFullAccess, "danger-full-access", "dangerFullAccess", map[string]any{"type": "dangerFullAccess"}},
		{permissions.ModeCustom, "workspace-write", "workspaceWrite", map[string]any{"type": "workspaceWrite", "writableRoots": []string{"/custom"}, "networkAccess": true}},
	}
	for _, test := range tests {
		policy := permissions.Policy{Mode: test.mode, WritableRoots: []string{"/custom"}, NetworkAccess: test.mode == permissions.ModeCustom}
		if got := codexSandboxMode(policy); got != test.legacy {
			t.Errorf("%s legacy sandbox=%q, want %q", test.mode, got, test.legacy)
		}
		got := codexSandboxPolicy(policy, roots)
		if !reflect.DeepEqual(got, test.wantFields) {
			t.Errorf("%s turn sandbox=%#v, want %#v", test.mode, got, test.wantFields)
		}
	}
}
