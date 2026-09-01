package remote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
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

func TestCoverageSummaryIncludesClassifiedReason(t *testing.T) {
	got := coverageSummary([]AgentCoverage{
		{Agent: "claude", CandidateFiles: 12, SelectedFiles: 12},
		{
			Agent: "codex", Error: "refresh timed out", ErrorReason: string(ReasonRefreshTimeout),
		},
	})
	want := "claude:cand=12,sel=12,trunc=false;codex:cand=0,sel=0,trunc=false,reason=refresh_timeout"
	if got != want {
		t.Fatalf("coverageSummary=%q, want %q", got, want)
	}
}

func TestAgentSessionCounts(t *testing.T) {
	got := agentSessionCounts([]*session.Session{
		{Agent: "claude", ID: "a"},
		{Agent: "claude", ID: "b"},
		{Agent: "codex", ID: "c"},
	})
	if got != "claude=2,codex=1" {
		t.Fatalf("agentSessionCounts=%q", got)
	}
}
