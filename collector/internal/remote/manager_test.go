package remote

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestApplySettingsWaitsForFirstListViewWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	var started atomic.Int32
	manager := NewManager(Options{
		Cache: NewCache(filepath.Join(home, "remote-cache")),
		Now:   func() time.Time { return now },
		Refresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2) (refreshOutcome, error) {
			started.Add(1)
			return refreshOutcome{}, nil
		},
	})
	if err := manager.ApplySettings(&settings.RemoteSettings{
		ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true,
	}); err != nil {
		t.Fatalf("ApplySettings: %v", err)
	}
	// Give any incorrectly-eager goroutine a chance to run before asserting.
	time.Sleep(20 * time.Millisecond)
	beforeListView := started.Load()
	if beforeListView != 0 {
		t.Fatalf("refresh started before any ListView call: started=%d", beforeListView)
	}

	manager.ListView(0)
	waitUntil(t, func() bool {
		return started.Load() > 0
	})
}

func TestApplyLimitedPublishesSessionsAndBacksOff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	manager := NewManager(Options{
		Cache: NewCache(filepath.Join(home, "remote-cache")),
		Now:   func() time.Time { return now },
		Refresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2) (refreshOutcome, error) {
			return refreshOutcome{
				Sessions: []*session.Session{{
					Agent: vendors.AgentClaude, ID: "s1", LastActivityTime: now.UnixMilli(),
				}},
				Snapshot: CachedSnapshotV2{
					Coverage: []AgentCoverage{
						{Agent: vendors.AgentClaude, CandidateFiles: 12, SelectedFiles: 12},
						{Agent: vendors.AgentCodex, Error: genericErrorCopy(ReasonRefreshTimeout)},
					},
				},
				Failures: []error{context.DeadlineExceeded},
			}, nil
		},
	})
	if err := manager.ApplySettings(&settings.RemoteSettings{
		ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true,
	}); err != nil {
		t.Fatalf("ApplySettings: %v", err)
	}
	manager.ListView(0)
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

	manager.mu.Lock()
	if manager.snapshot == nil {
		t.Fatal("expected limited snapshot to be cached")
	}
	if manager.nextRetryAt.IsZero() {
		t.Fatal("expected retry backoff after limited refresh")
	}
	manager.mu.Unlock()

	// A second cache load should see the v2 snapshot committed by the
	// limited refresh, not a legacy stale shell.
	loaded, ok, err := manager.cache.LoadV2("r_0123456789abcdef")
	if err != nil || !ok {
		t.Fatalf("LoadV2 after limited publish: ok=%v err=%v", ok, err)
	}
	if len(loaded.Families) != 0 {
		t.Fatalf("no families were produced by this fake refresh, got %d", len(loaded.Families))
	}
}

func TestHardFailureFallsBackToStaleWhenCacheExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	fail := true
	manager := NewManager(Options{
		Cache: NewCache(filepath.Join(home, "remote-cache")),
		Now:   func() time.Time { return now },
		Refresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2) (refreshOutcome, error) {
			if fail {
				return refreshOutcome{}, context.DeadlineExceeded
			}
			return refreshOutcome{Sessions: []*session.Session{{Agent: vendors.AgentClaude, ID: "s1"}}}, nil
		},
	})
	if err := manager.ApplySettings(&settings.RemoteSettings{
		ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	manager.ListView(0)
	waitUntil(t, func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		return !manager.refreshing
	})
	manager.mu.Lock()
	if manager.state != StateError {
		t.Fatalf("first failure with no cache should be StateError, got %s", manager.state)
	}
	manager.mu.Unlock()
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
