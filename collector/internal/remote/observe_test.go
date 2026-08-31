package remote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestObserveEnabled(t *testing.T) {
	t.Setenv("COSLASH_REMOTE_DEBUG", "")
	if !ObserveEnabled() {
		t.Fatal("expected default on for testing branch")
	}
	t.Setenv("COSLASH_REMOTE_DEBUG", "0")
	if ObserveEnabled() {
		t.Fatal("expected off when COSLASH_REMOTE_DEBUG=0")
	}
	t.Setenv("COSLASH_REMOTE_DEBUG", "1")
	if !ObserveEnabled() {
		t.Fatal("expected on when COSLASH_REMOTE_DEBUG=1")
	}
}

func TestObserveWritesLogFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("COSLASH_REMOTE_DEBUG", "1")

	observe("test", "phase", "start", "outcome", "ok")

	entries, err := os.ReadDir(LogDir())
	if err != nil {
		t.Fatalf("read log dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one log file, got %d", len(entries))
	}
	body, err := os.ReadFile(filepath.Join(LogDir(), entries[0].Name()))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.HasPrefix(entries[0].Name(), "issues-") {
		t.Fatalf("expected issues-*.log, got %q", entries[0].Name())
	}
	if !strings.Contains(string(body), "remote.test phase=start outcome=ok") {
		t.Fatalf("missing observe line: %q", body)
	}
}
