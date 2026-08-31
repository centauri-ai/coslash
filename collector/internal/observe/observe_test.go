package observe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestOperationRecordsFailuresAndSlowSuccesses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("COSLASH_DEBUG", "1")
	Operation("http", time.Now(), "error", "route", "GET /api/sessions")
	Operation("http", time.Now().Add(-slowOperation), "ok", "route", "GET /api/sessions")

	entries, err := os.ReadDir(LogDir())
	if err != nil {
		t.Fatalf("read log dir: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(LogDir(), entries[0].Name()))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if count := strings.Count(string(body), "operation operation=http"); count != 2 {
		t.Fatalf("operation lines = %d, want 2: %q", count, body)
	}
}

func TestPruneLogFilesKeepsRecentIssueLogs(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format("20060102")
	old := time.Now().AddDate(0, 0, -logRetentionDays).Format("20060102")
	recent := time.Now().AddDate(0, 0, -(logRetentionDays - 1)).Format("20060102")
	for _, date := range []string{old, recent} {
		if err := os.WriteFile(filepath.Join(dir, "issues-"+date+".log"), nil, 0o600); err != nil {
			t.Fatalf("write log: %v", err)
		}
	}
	pruneLogFiles(dir, today)
	if _, err := os.Stat(filepath.Join(dir, "issues-"+old+".log")); !os.IsNotExist(err) {
		t.Fatalf("old log remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "issues-"+recent+".log")); err != nil {
		t.Fatalf("recent log missing: %v", err)
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
