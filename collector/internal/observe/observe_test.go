package observe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnabledEnv(t *testing.T) {
	t.Setenv("COSLASH_DEBUG", "")
	t.Setenv("COSLASH_REMOTE_DEBUG", "")
	if !Enabled() {
		t.Fatal("expected default on")
	}
	t.Setenv("COSLASH_DEBUG", "0")
	if Enabled() {
		t.Fatal("expected COSLASH_DEBUG=0 to win")
	}
	t.Setenv("COSLASH_DEBUG", "")
	t.Setenv("COSLASH_REMOTE_DEBUG", "0")
	if Enabled() {
		t.Fatal("expected COSLASH_REMOTE_DEBUG=0 fallback")
	}
	t.Setenv("COSLASH_REMOTE_DEBUG", "1")
	if !Enabled() {
		t.Fatal("expected COSLASH_REMOTE_DEBUG=1 fallback")
	}
}

func TestEventWritesIssuesLog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("COSLASH_DEBUG", "1")
	t.Setenv("COSLASH_REMOTE_DEBUG", "")

	Event("issue.launch.failed", "reason", "terminal_missing", "mode", "resume")

	entries, err := os.ReadDir(LogDir())
	if err != nil {
		t.Fatalf("read log dir: %v", err)
	}
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "issues-") {
		t.Fatalf("unexpected log entries: %v", entries)
	}
	body, err := os.ReadFile(filepath.Join(LogDir(), entries[0].Name()))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(body), "issue.launch.failed reason=terminal_missing mode=resume") {
		t.Fatalf("missing issue line: %q", body)
	}
}
