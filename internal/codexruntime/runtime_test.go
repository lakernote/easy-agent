package codexruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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
    *'"method":"turn/start"'*) echo '{"id":3,"result":{"turn":{"id":"turn-test","status":"inProgress"}}}'; echo '{"method":"item/agentMessage/delta","params":{"delta":"hello"}}'; echo '{"method":"turn/completed","params":{"turn":{"status":"completed","error":null}}}' ;;
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
