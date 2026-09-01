package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestApplyLimitedPublishesSessionsAndBacksOff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	manager := NewManager(Options{
		Cache: NewCache(filepath.Join(home, "remote-cache")),
		Now:   func() time.Time { return now },
		Refresh: func(context.Context, string, int64, time.Time) (refreshResult, error) {
			return refreshResult{
				Sessions: []*session.Session{{
					Agent: vendors.AgentClaude, ID: "s1", LastActivityTime: now.UnixMilli(),
				}},
				Coverage: []AgentCoverage{
					{Agent: vendors.AgentClaude, CandidateFiles: 12, SelectedFiles: 12},
					{Agent: vendors.AgentCodex, Error: genericErrorCopy(ReasonRefreshTimeout)},
				},
				Failures: []error{fmt.Errorf("collect Codex remote data: %w", context.DeadlineExceeded)},
			}, nil
		},
	})
	if err := manager.ApplySettings(&settings.RemoteSettings{
		ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true,
	}); err != nil {
		t.Fatalf("ApplySettings: %v", err)
	}
	waitUntil(t, func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		return !manager.refreshing && manager.state == StateLimited
	})

	view := manager.ListView(0)
	if view.Health.State != StateLimited {
		t.Fatalf("state=%s, want limited", view.Health.State)
	}
	if len(view.Sessions) != 1 {
		t.Fatalf("sessions=%d, want 1 published", len(view.Sessions))
	}
	if view.Sessions[0].DisplayStale {
		t.Fatal("limited partial snapshot should not display as stale")
	}
	if view.Sessions[0].EligibleForAggregates {
		t.Fatal("limited snapshot should not be eligible for aggregates")
	}

	manager.mu.Lock()
	if manager.cached == nil {
		t.Fatal("expected limited snapshot to be cached")
	}
	if manager.nextRetryAt.IsZero() {
		t.Fatal("expected retry backoff after limited refresh")
	}
	manager.cached.FetchedAtMs = now.Add(-FreshnessInterval - time.Second).UnixMilli()
	manager.nextRetryAt = now.Add(time.Hour)
	started := 0
	manager.refresh = func(context.Context, string, int64, time.Time) (refreshResult, error) {
		started++
		return refreshResult{}, errors.New("should not refresh during backoff")
	}
	manager.mu.Unlock()

	_ = manager.ListView(0)
	if started != 0 {
		t.Fatalf("refresh starts during backoff: %d", started)
	}
}

func TestClassifyErrorTimeoutNotInvalidData(t *testing.T) {
	err := fmt.Errorf("collect Codex remote data: %w", context.DeadlineExceeded)
	if got := classifyError(err); got != ReasonRefreshTimeout {
		t.Fatalf("classifyError=%s, want refresh_timeout", got)
	}
	// Legacy wrap used "parse …", which previously matched invalid_remote_data.
	legacy := fmt.Errorf("parse Codex remote data: %w", context.DeadlineExceeded)
	if got := classifyError(legacy); got != ReasonRefreshTimeout {
		t.Fatalf("legacy wrap classifyError=%s, want refresh_timeout", got)
	}
	if got := classifyError(errors.New("collect Codex remote data: unexpected EOF")); got != ReasonConnectionFailed {
		t.Fatalf("classifyError EOF=%s, want connection_failed", got)
	}
}

func TestClassifyErrorMalformedTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "malformed.jsonl")
	if err := os.WriteFile(path, []byte("{x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, parseErr := vendors.ParseJSONLSource[map[string]any](vendors.LocalReadSource, path)
	if parseErr == nil {
		t.Fatal("expected malformed transcript error")
	}
	tests := []error{
		fmt.Errorf("collect Codex remote data: %w", parseErr),
		fmt.Errorf(
			"collect Codex remote data: %w: first row type %q is not session_meta",
			vendors.ErrInvalidData, "event_msg",
		),
	}
	for _, err := range tests {
		if got := classifyError(err); got != ReasonInvalidData {
			t.Errorf("classifyError(%q)=%s, want invalid_remote_data", err, got)
		}
	}
}

func waitUntil(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}
