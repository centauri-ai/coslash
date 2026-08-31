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

func TestOperationRecordsSlowSuccessOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("COSLASH_DEBUG", "1")

	Operation("http", time.Now(), "error", "route", "GET /api/sessions", "status", 500)
	Operation("http", time.Now(), "ok", "route", "GET /api/sessions", "status", 200)
	if entries, _ := os.ReadDir(LogDir()); len(entries) != 0 {
		t.Fatalf("error/fast success should not write a line, got %v", entries)
	}

	Operation("http", time.Now().Add(-slowOperation), "ok", "route", "GET /api/sessions", "status", 200)
	entries, err := os.ReadDir(LogDir())
	if err != nil {
		t.Fatalf("read log dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one slow-success log file, got %v", entries)
	}
	body, err := os.ReadFile(filepath.Join(LogDir(), entries[0].Name()))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	line := string(body)
	if strings.Count(strings.TrimSpace(line), "\n")+1 != 1 {
		t.Fatalf("want one line, got %q", line)
	}
	if !strings.Contains(line, "operation operation=http outcome=ok") {
		t.Fatalf("missing slow operation line: %q", line)
	}
	if strings.Contains(line, "detail=") || strings.Contains(line, "issue.operation") {
		t.Fatalf("unexpected fields: %q", line)
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

func TestEventWritesOneIssueLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("COSLASH_DEBUG", "1")
	t.Setenv("COSLASH_REMOTE_DEBUG", "")

	Event("issue.api.error", "route", "settings", "reason", "invalid_json", "status", 400)

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
	line := string(body)
	if strings.Count(strings.TrimSpace(line), "\n")+1 != 1 {
		t.Fatalf("want one line, got %q", line)
	}
	if !strings.Contains(line, "issue.api.error route=settings reason=invalid_json status=400") {
		t.Fatalf("missing issue line: %q", line)
	}
	for _, banned := range []string{"detail=", "password", "token=", "secret", "/Users/"} {
		if strings.Contains(line, banned) {
			t.Fatalf("line contains banned content %q: %q", banned, line)
		}
	}
}
