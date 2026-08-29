package remote

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (clock *testClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.t
}

func (clock *testClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.t = clock.t.Add(duration)
}

func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

func successfulRefresh() refreshResult {
	return refreshResult{
		Sessions: []*session.Session{{
			Agent: "claude", ID: "session-1", StartedAt: 100, LastActivityTime: 200,
		}},
		Coverage: []AgentCoverage{
			{Agent: "claude", CandidateFiles: 1, SelectedFiles: 1},
			{Agent: "codex"},
		},
		RoundTrip: 20 * time.Millisecond,
	}
}

func enabledSettings() *settings.RemoteSettings {
	return &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "gpu-server", Enabled: true}
}

func TestManagerRefreshesCachesAndIndexesSessions(t *testing.T) {
	clock := &testClock{t: time.UnixMilli(1_000_000)}
	root := t.TempDir()
	manager := NewManager(Options{
		Cache: NewCache(root), Now: clock.Now,
		Refresh: func(context.Context, string, int64, time.Time) (refreshResult, error) {
			return successfulRefresh(), nil
		},
	})
	if err := manager.ApplySettings(enabledSettings()); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return manager.DiagnosticsHealth().State == StateOK })
	result := manager.ListView(0)
	if len(result.Sessions) != 1 || !result.Sessions[0].EligibleForAggregates {
		t.Fatalf("sessions=%+v", result.Sessions)
	}
	if result.Sessions[0].Key.SourceID != enabledSettings().ID {
		t.Fatalf("key=%+v", result.Sessions[0].Key)
	}
	if _, ok, err := NewCache(root).Load(enabledSettings().ID); err != nil || !ok {
		t.Fatalf("cache ok=%v err=%v", ok, err)
	}
}

func TestManagerUsesOneFlightAndManualRetryBypassesBackoff(t *testing.T) {
	clock := &testClock{t: time.UnixMilli(2_000_000)}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	manager := NewManager(Options{
		Cache: NewCache(t.TempDir()), Now: clock.Now,
		Refresh: func(ctx context.Context, _ string, _ int64, _ time.Time) (refreshResult, error) {
			calls.Add(1)
			started <- struct{}{}
			select {
			case <-release:
				return successfulRefresh(), nil
			case <-ctx.Done():
				return refreshResult{}, ctx.Err()
			}
		},
	})
	if err := manager.ApplySettings(enabledSettings()); err != nil {
		t.Fatal(err)
	}
	<-started
	manager.Retry()
	manager.Retry()
	if calls.Load() != 1 {
		t.Fatalf("expected one flight, calls=%d", calls.Load())
	}
	close(release)
	waitUntil(t, func() bool { return manager.DiagnosticsHealth().State == StateOK })

	clock.Advance(FreshnessInterval + time.Second)
	manager.refresh = func(context.Context, string, int64, time.Time) (refreshResult, error) {
		calls.Add(1)
		return refreshResult{}, errors.New("ssh failed")
	}
	manager.ListView(0)
	waitUntil(t, func() bool { return manager.DiagnosticsHealth().State == StateStale })
	before := calls.Load()
	manager.ListView(0)
	if calls.Load() != before {
		t.Fatal("automatic retry ignored backoff")
	}
	manager.Retry()
	waitUntil(t, func() bool { return calls.Load() > before })
}

func TestManagerClassifiesLimitedResults(t *testing.T) {
	tests := []struct {
		name   string
		result refreshResult
		reason Reason
	}{
		{
			name: "truncated", reason: ReasonHistoryTruncated,
			result: refreshResult{Coverage: []AgentCoverage{{Agent: "claude", CandidateFiles: 2, SelectedFiles: 1, Truncated: true}}},
		},
		{
			name: "partial", reason: ReasonPartialAgentData,
			result: refreshResult{Coverage: []AgentCoverage{{Agent: "claude", CandidateFiles: 1}}, Failures: []error{errors.New("codex denied")}},
		},
		{
			name: "empty", reason: ReasonNoSupportedData,
			result: refreshResult{Coverage: []AgentCoverage{{Agent: "claude"}, {Agent: "codex"}}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := NewManager(Options{
				Cache: NewCache(t.TempDir()),
				Refresh: func(context.Context, string, int64, time.Time) (refreshResult, error) {
					return test.result, nil
				},
			})
			if err := manager.ApplySettings(enabledSettings()); err != nil {
				t.Fatal(err)
			}
			waitUntil(t, func() bool { return !manager.DiagnosticsHealth().Refreshing })
			health := manager.DiagnosticsHealth()
			if health.State != StateLimited || health.Reason == nil || *health.Reason != test.reason || health.Complete {
				t.Fatalf("health=%+v", health)
			}
		})
	}
}

func TestManagerDisableRetainsCacheAndRemoveDeletesIt(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(Options{
		Cache: NewCache(root),
		Refresh: func(context.Context, string, int64, time.Time) (refreshResult, error) {
			return successfulRefresh(), nil
		},
	})
	config := enabledSettings()
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return manager.DiagnosticsHealth().State == StateOK })
	disabled := *config
	disabled.Enabled = false
	if err := manager.ApplySettings(&disabled); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := NewCache(root).Load(config.ID); !ok {
		t.Fatal("disable removed last-good cache")
	}
	if err := manager.ApplySettings(nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := NewCache(root).Load(config.ID); ok {
		t.Fatal("removing remote retained cache")
	}
}

func TestManagerTestAliasInspectsAgentData(t *testing.T) {
	tests := []struct {
		name   string
		result refreshResult
		err    error
		state  State
		reason *Reason
	}{
		{name: "ready", result: successfulRefresh(), state: StateOK},
		{
			name: "no supported data", state: StateLimited, reason: reasonPtr(ReasonNoSupportedData),
			result: refreshResult{Coverage: []AgentCoverage{{Agent: "claude"}, {Agent: "codex"}}},
		},
		{
			name: "partial agent data", state: StateLimited, reason: reasonPtr(ReasonPartialAgentData),
			result: refreshResult{
				Coverage: []AgentCoverage{{Agent: "claude", CandidateFiles: 1}, {Agent: "codex", Error: "agent data is not readable"}},
				Failures: []error{fs.ErrPermission},
			},
		},
		{
			name: "permission denied", state: StateError, reason: reasonPtr(ReasonPermissionDenied),
			err: fs.ErrPermission,
		},
		{
			name: "SFTP unavailable", state: StateError, reason: reasonPtr(ReasonSFTPUnavailable),
			err: wrapSSHError(errors.New("open SFTP subsystem: EOF"), "user@host: subsystem request failed"),
		},
		{
			name: "authentication failed", state: StateError, reason: reasonPtr(ReasonAuthentication),
			err: wrapSSHError(errors.New("open SFTP subsystem: EOF"), "user@host: Permission denied (publickey)."),
		},
		{
			name: "host key failed", state: StateError, reason: reasonPtr(ReasonHostKey),
			err: wrapSSHError(errors.New("open SFTP subsystem: EOF"), "Host key verification failed."),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := NewManager(Options{Test: func(context.Context, string, int64, time.Time) (refreshResult, error) {
				return test.result, test.err
			}})
			health, err := manager.TestAlias(context.Background(), "gpu-server")
			if err != nil || health.State != test.state || !reasonsEqual(health.Reason, test.reason) {
				t.Fatalf("health=%+v err=%v", health, err)
			}
			if test.err != nil && health.Complete {
				t.Fatalf("failed preflight is complete: %+v", health)
			}
		})
	}

	manager := NewManager(Options{Test: func(context.Context, string, int64, time.Time) (refreshResult, error) {
		return refreshResult{}, wrapSSHError(
			errors.New("open SFTP subsystem: EOF"),
			"user@host: subsystem request failed",
		)
	}})
	health, err := manager.TestAlias(context.Background(), "gpu-server")
	if err != nil {
		t.Fatal(err)
	}
	if health.DiagnosticStderr != "[redacted] subsystem request failed" {
		t.Fatalf("diagnostic=%q", health.DiagnosticStderr)
	}
}

func reasonsEqual(left, right *Reason) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func TestManagerMidRefreshDisconnectPreservesLastGood(t *testing.T) {
	cache := NewCache(t.TempDir())
	disconnectStarted := make(chan struct{})
	releaseDisconnect := make(chan struct{})
	var calls atomic.Int32
	manager := NewManager(Options{
		Cache: cache,
		Refresh: func(context.Context, string, int64, time.Time) (refreshResult, error) {
			if calls.Add(1) == 1 {
				return successfulRefresh(), nil
			}
			close(disconnectStarted)
			<-releaseDisconnect
			return refreshResult{Stderr: "connection reset by peer"}, errors.New("connection reset")
		},
	})
	config := enabledSettings()
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return manager.DiagnosticsHealth().State == StateOK })
	want, ok, err := cache.Load(config.ID)
	if err != nil || !ok {
		t.Fatalf("initial cache ok=%v err=%v", ok, err)
	}

	manager.Retry()
	<-disconnectStarted
	close(releaseDisconnect)
	waitUntil(t, func() bool { return manager.DiagnosticsHealth().State == StateStale })
	got, ok, err := cache.Load(config.ID)
	if err != nil || !ok {
		t.Fatalf("stale cache ok=%v err=%v", ok, err)
	}
	if len(got.Sessions) != len(want.Sessions) || got.FetchedAtMs != want.FetchedAtMs {
		t.Fatalf("last-good changed: got=%+v want=%+v", got, want)
	}
	if sessions := manager.ListView(0).Sessions; len(sessions) != 1 || !sessions[0].DisplayStale {
		t.Fatalf("stale sessions=%+v", sessions)
	}
}

func TestManagerLimitedRefreshPreservesLastGood(t *testing.T) {
	cache := NewCache(t.TempDir())
	var calls atomic.Int32
	manager := NewManager(Options{
		Cache: cache,
		Refresh: func(context.Context, string, int64, time.Time) (refreshResult, error) {
			if calls.Add(1) == 1 {
				return successfulRefresh(), nil
			}
			return refreshResult{
				Coverage: []AgentCoverage{{Agent: "claude", CandidateFiles: 2, SelectedFiles: 1, Truncated: true}},
			}, nil
		},
	})
	config := enabledSettings()
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return manager.DiagnosticsHealth().State == StateOK })
	want, ok, err := cache.Load(config.ID)
	if err != nil || !ok {
		t.Fatalf("initial cache ok=%v err=%v", ok, err)
	}

	manager.Retry()
	waitUntil(t, func() bool { return manager.DiagnosticsHealth().State == StateLimited })
	got, ok, err := cache.Load(config.ID)
	if err != nil || !ok {
		t.Fatalf("last-good cache ok=%v err=%v", ok, err)
	}
	if len(got.Sessions) != len(want.Sessions) || got.FetchedAtMs != want.FetchedAtMs {
		t.Fatalf("last-good changed: got=%+v want=%+v", got, want)
	}
	if sessions := manager.ListView(0).Sessions; len(sessions) != 1 || !sessions[0].DisplayStale {
		t.Fatalf("limited sessions=%+v", sessions)
	}
}

func TestManagerAliasChangeClearsCache(t *testing.T) {
	cache := NewCache(t.TempDir())
	config := enabledSettings()
	if err := cache.Store(config.ID, snapshotForCache(successfulRefresh().Sessions, successfulRefresh().Coverage, nil, 0, 1, 1)); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Options{
		Cache: cache,
		Refresh: func(context.Context, string, int64, time.Time) (refreshResult, error) {
			return refreshResult{}, errors.New("connection reset")
		},
	})
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return manager.DiagnosticsHealth().State == StateStale })
	changed := *config
	changed.SSHAlias = "new-host"
	if err := manager.ApplySettings(&changed); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return manager.DiagnosticsHealth().State == StateError })
	if _, ok, err := cache.Load(config.ID); err != nil || ok {
		t.Fatalf("alias change retained cache: ok=%v err=%v", ok, err)
	}
	if sessions := manager.ListView(0).Sessions; len(sessions) != 0 {
		t.Fatalf("alias change retained sessions=%+v", sessions)
	}
}

func TestManagerReplacementAndShutdownCancelInflightRefresh(t *testing.T) {
	t.Run("replacement", func(t *testing.T) {
		oldStarted := make(chan struct{})
		oldCanceled := make(chan struct{})
		manager := NewManager(Options{
			Cache: NewCache(t.TempDir()),
			Refresh: func(ctx context.Context, alias string, _ int64, _ time.Time) (refreshResult, error) {
				if alias == "old-host" {
					close(oldStarted)
					<-ctx.Done()
					close(oldCanceled)
					return refreshResult{}, ctx.Err()
				}
				return successfulRefresh(), nil
			},
		})
		defer manager.Shutdown()
		oldConfig := &settings.RemoteSettings{
			ID: "r_0123456789abcdef", SSHAlias: "old-host", Enabled: true,
		}
		if err := manager.ApplySettings(oldConfig); err != nil {
			t.Fatal(err)
		}
		<-oldStarted
		newConfig := &settings.RemoteSettings{
			ID: "r_fedcba9876543210", SSHAlias: "new-host", Enabled: true,
		}
		if err := manager.ApplySettings(newConfig); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, func() bool {
			select {
			case <-oldCanceled:
				health := manager.DiagnosticsHealth()
				return health.SourceID == newConfig.ID && health.State == StateOK && !health.Refreshing
			default:
				return false
			}
		})
	})

	t.Run("shutdown", func(t *testing.T) {
		started := make(chan struct{})
		canceled := make(chan struct{})
		manager := NewManager(Options{
			Cache: NewCache(t.TempDir()),
			Refresh: func(ctx context.Context, _ string, _ int64, _ time.Time) (refreshResult, error) {
				close(started)
				<-ctx.Done()
				close(canceled)
				return refreshResult{}, ctx.Err()
			},
		})
		if err := manager.ApplySettings(enabledSettings()); err != nil {
			t.Fatal(err)
		}
		<-started
		manager.Shutdown()
		select {
		case <-canceled:
		case <-time.After(time.Second):
			t.Fatal("shutdown did not cancel refresh")
		}
	})
}

func TestManagerEndToEndThroughFakeSSHAndSFTP(t *testing.T) {
	root := t.TempDir()
	claudeID := "11111111-1111-4111-8111-111111111111"
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "projects", "repo", claudeID+".jsonl"),
		`{"sessionId":"`+claudeID+`","cwd":"/work/claude","timestamp":"2026-08-21T10:00:00Z","type":"user","message":{"content":"hello"}}`+"\n",
	)
	codexID := "22222222-2222-4222-8222-222222222222"
	writeTestFile(
		t,
		filepath.Join(root, ".codex", "sessions", "2026", "08", "21", "rollout-2026-08-21T10-00-00-"+codexID+".jsonl"),
		`{"timestamp":"2026-08-21T10:00:00Z","type":"session_meta","payload":{"id":"`+codexID+`","cwd":"/work/codex"}}`+"\n"+
			`{"timestamp":"2026-08-21T10:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"hello"}}`+"\n",
	)
	var failOpen atomic.Bool
	cache := NewCache(t.TempDir())
	manager := NewManager(Options{
		Cache: cache,
		Open: func(ctx context.Context, alias string, options OpenOptions) (*Session, error) {
			if failOpen.Load() {
				return nil, errors.New("connection reset")
			}
			options.Limits.Deadline = 5 * time.Second
			options.command = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
				command := exec.CommandContext(ctx, os.Args[0], "-test.run=TestSSHHelperProcess")
				command.Env = append(os.Environ(), "COSLASH_SSH_HELPER=serve", "COSLASH_SSH_HELPER_ROOT="+root)
				return command
			}
			return OpenSession(ctx, alias, options)
		},
	})
	preflight, err := manager.TestAlias(context.Background(), "gpu-server")
	if err != nil || preflight.State != StateOK || len(preflight.Coverage) != 2 {
		t.Fatalf("preflight=%+v err=%v", preflight, err)
	}
	config := enabledSettings()
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return manager.DiagnosticsHealth().State == StateOK })
	if sessions := manager.ListView(0).Sessions; len(sessions) != 2 {
		t.Fatalf("sessions=%d, want Claude and Codex", len(sessions))
	}
	if cached, ok, err := cache.Load(config.ID); err != nil || !ok || len(cached.Fingerprints) != 2 {
		t.Fatalf("cache ok=%v fingerprints=%d err=%v", ok, len(cached.Fingerprints), err)
	}

	failOpen.Store(true)
	manager.Retry()
	waitUntil(t, func() bool { return manager.DiagnosticsHealth().State == StateStale })
	if sessions := manager.ListView(0).Sessions; len(sessions) != 2 {
		t.Fatalf("stale last-good sessions=%d", len(sessions))
	}
	if err := manager.ApplySettings(nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := cache.Load(config.ID); err != nil || ok {
		t.Fatalf("removed cache ok=%v err=%v", ok, err)
	}
}
