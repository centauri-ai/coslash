package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
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
		if started.Load() == 0 {
			return false
		}
		manager.mu.Lock()
		defer manager.mu.Unlock()
		return !manager.refreshing
	})
}

func TestColdRefreshPublishesSevenDaysBeforeAllHistory(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	secondWave := make(chan struct{})
	type call struct {
		since      int64
		baselineID string
	}
	calls := make(chan call, 2)
	manager := NewManager(Options{
		Cache: NewCache(filepath.Join(home, "remote-cache")),
		Now:   func() time.Time { return now },
		Refresh: func(_ context.Context, _ string, since int64, _ time.Time, baseline CachedSnapshotV2) (refreshOutcome, error) {
			calls <- call{since: since, baselineID: baseline.BaselineID}
			id := "wave-a"
			if baseline.BaselineID != "" {
				<-secondWave
				id = "wave-b"
			}
			return refreshOutcome{
				Snapshot: CachedSnapshotV2{
					Version: cacheV2Version, BaselineID: id, CoverageSinceMs: since,
					Coverage: []AgentCoverage{{Agent: vendors.AgentClaude, CandidateFiles: 1, SelectedFiles: 1}},
				},
				Sessions: []*session.Session{{
					Agent: vendors.AgentClaude, ID: "session-1", WorkingDirectory: "/work",
					LastActivityTime: now.UnixMilli(),
				}},
			}, nil
		},
	})
	t.Cleanup(manager.Shutdown)
	if err := manager.ApplySettings(&settings.RemoteSettings{
		ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	manager.ListView(0)

	first := <-calls
	wantFirst := now.Add(-coldHistoryWave).UnixMilli()
	if first.since != wantFirst || first.baselineID != "" {
		t.Fatalf("first call = %#v, want since=%d with empty baseline", first, wantFirst)
	}
	second := <-calls
	if second.since != 0 || second.baselineID != "wave-a" {
		t.Fatalf("second call = %#v, want all history from wave-a", second)
	}

	intermediate := manager.ListView(0)
	if intermediate.Health.State != StateOK || !intermediate.Health.Refreshing || intermediate.Health.Complete ||
		intermediate.Health.Reason == nil || *intermediate.Health.Reason != ReasonBroaderHistory {
		t.Fatalf("intermediate health = %#v", intermediate.Health)
	}
	if intermediate.Health.PublicationID == "" || intermediate.Health.CoverageSinceMs == nil ||
		*intermediate.Health.CoverageSinceMs != wantFirst {
		t.Fatalf("intermediate publication = %#v", intermediate.Health)
	}
	if len(intermediate.Sessions) != 1 || !intermediate.Sessions[0].Launchable || intermediate.Sessions[0].EligibleForAggregates {
		t.Fatalf("intermediate sessions = %#v", intermediate.Sessions)
	}
	narrowerHealth := manager.ListView(now.Add(-24 * time.Hour).UnixMilli()).Health
	if narrowerHealth.Complete {
		t.Fatalf("narrower view claimed globally incomplete collection was complete: %#v", narrowerHealth)
	}
	launched, _, err := manager.LaunchSession("r_0123456789abcdef", vendors.AgentClaude, "session-1", "")
	if err != nil || launched == nil {
		t.Fatalf("launch during second wave: session=%#v err=%v", launched, err)
	}
	if preview, err := manager.PreviewSession("r_0123456789abcdef", vendors.AgentClaude, "session-1", now.UnixMilli()); err != nil || preview != nil {
		t.Fatalf("preview during incomplete coverage: session=%#v err=%v", preview, err)
	}

	firstPublication := intermediate.Health.PublicationID
	close(secondWave)
	waitUntil(t, func() bool { return !manager.DiagnosticsHealth().Refreshing })
	final := manager.ListView(0)
	if final.Health.State != StateOK || !final.Health.Complete || final.Health.PublicationID == firstPublication {
		t.Fatalf("final health = %#v", final.Health)
	}
	if len(final.Sessions) != 1 || !final.Sessions[0].EligibleForAggregates {
		t.Fatalf("final sessions = %#v", final.Sessions)
	}
}

func TestNextWaveSincePolicy(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	sevenDays := now.Add(-coldHistoryWave).UnixMilli()
	older := now.Add(-30 * 24 * time.Hour).UnixMilli()
	newer := now.Add(-24 * time.Hour).UnixMilli()
	justOutsideBrowserBoundary := sevenDays - time.Second.Milliseconds()
	for _, test := range []struct {
		name      string
		requested int64
		baseline  CachedSnapshotV2
		warm      bool
		want      int64
	}{
		{name: "cold all history", requested: 0, want: sevenDays},
		{name: "cold newer finite", requested: newer, want: newer},
		{name: "cold older finite", requested: older, want: sevenDays},
		{name: "browser seven day boundary", requested: justOutsideBrowserBoundary, want: justOutsideBrowserBoundary},
		{name: "resume after milestone", requested: older, baseline: CachedSnapshotV2{BaselineID: "wave-a", CoverageSinceMs: sevenDays}, want: older},
		{name: "warm covered", requested: older, baseline: CachedSnapshotV2{BaselineID: "warm", CoverageSinceMs: 0}, warm: true, want: older},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := nextWaveSince(now, test.requested, test.baseline, test.warm); got != test.want {
				t.Fatalf("nextWaveSince() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestBroaderRequestExtendsActiveRunAndNarrowerRequestDoesNotRegressIt(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	initial := now.Add(-24 * time.Hour).UnixMilli()
	firstRelease := make(chan struct{})
	type call struct {
		since      int64
		baselineID string
	}
	calls := make(chan call, 4)
	callNumber := 0
	manager := NewManager(Options{
		Cache: NewCache(t.TempDir()), Now: func() time.Time { return now },
		Refresh: func(_ context.Context, _ string, since int64, _ time.Time, baseline CachedSnapshotV2) (refreshOutcome, error) {
			callNumber++
			calls <- call{since: since, baselineID: baseline.BaselineID}
			if callNumber == 1 {
				<-firstRelease
			}
			return refreshOutcome{Snapshot: CachedSnapshotV2{
				Version: cacheV2Version, BaselineID: fmt.Sprintf("wave-%d", callNumber), CoverageSinceMs: since,
				Coverage: []AgentCoverage{{Agent: vendors.AgentClaude, CandidateFiles: 1, SelectedFiles: 1}},
			}}, nil
		},
	})
	t.Cleanup(manager.Shutdown)
	if err := manager.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	manager.ListView(initial)
	if first := <-calls; first.since != initial || first.baselineID != "" {
		t.Fatalf("initial call = %#v", first)
	}
	manager.ListView(now.Add(-2 * time.Hour).UnixMilli()) // narrower: must not replace initial
	manager.ListView(0)                                   // broader: must extend the active run
	close(firstRelease)

	want := []call{
		{since: now.Add(-coldHistoryWave).UnixMilli(), baselineID: "wave-1"},
		{since: 0, baselineID: "wave-2"},
	}
	for _, expected := range want {
		if got := <-calls; got != expected {
			t.Fatalf("continued call = %#v, want %#v", got, expected)
		}
	}
	waitUntil(t, func() bool { return !manager.DiagnosticsHealth().Refreshing })
	select {
	case extra := <-calls:
		t.Fatalf("unexpected extra call: %#v", extra)
	default:
	}
}

func TestLateResultFromReplacedLifecycleCannotPublish(t *testing.T) {
	cache := NewCache(t.TempDir())
	started := make(chan struct{})
	release := make(chan struct{})
	returned := make(chan struct{})
	manager := NewManager(Options{
		Cache: cache,
		Refresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2) (refreshOutcome, error) {
			close(started)
			<-release // deliberately ignore cancellation to exercise the epoch fence
			close(returned)
			return completeRefreshOutcome("late", 0, "late-session"), nil
		},
	})
	first := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "old-host", Enabled: true}
	if err := manager.ApplySettings(first); err != nil {
		t.Fatal(err)
	}
	manager.ListView(time.Now().Add(-time.Hour).UnixMilli())
	<-started
	replacement := *first
	replacement.SSHAlias = "new-host"
	if err := manager.ApplySettings(&replacement); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-returned
	time.Sleep(20 * time.Millisecond)
	if health := manager.DiagnosticsHealth(); health.PublicationID != "" {
		t.Fatalf("late lifecycle result published health: %#v", health)
	}
	if _, ok, err := cache.LoadV2(first.ID); err != nil || ok {
		t.Fatalf("late lifecycle result reached cache: ok=%v err=%v", ok, err)
	}
}

func TestLaterCacheWriteFailureRetainsPublishedGeneration(t *testing.T) {
	home := t.TempDir()
	cacheRoot := filepath.Join(home, "remote-cache")
	cache := NewCache(cacheRoot)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	secondStarted := make(chan struct{})
	secondRelease := make(chan struct{})
	calls := 0
	manager := NewManager(Options{
		Cache: cache, Now: func() time.Time { return now },
		Refresh: func(_ context.Context, _ string, since int64, _ time.Time, _ CachedSnapshotV2) (refreshOutcome, error) {
			calls++
			if calls == 2 {
				close(secondStarted)
				<-secondRelease
			}
			return completeRefreshOutcome(fmt.Sprintf("wave-%d", calls), since, fmt.Sprintf("session-%d", calls)), nil
		},
	})
	t.Cleanup(manager.Shutdown)
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	manager.ListView(0)
	<-secondStarted
	before := manager.DiagnosticsHealth()
	invalidRoot := filepath.Join(home, "not-a-directory")
	if err := os.WriteFile(invalidRoot, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	cache.Root = invalidRoot
	close(secondRelease)
	waitUntil(t, func() bool { return !manager.DiagnosticsHealth().Refreshing })
	after := manager.ListView(0)
	if after.Health.State != StateStale || after.Health.Reason == nil || *after.Health.Reason != ReasonLocalCacheFailed {
		t.Fatalf("cache failure health = %#v", after.Health)
	}
	if after.Health.PublicationID != before.PublicationID || len(after.Sessions) != 1 || after.Sessions[0].Session.ID != "session-1" {
		t.Fatalf("cache failure replaced published generation: before=%#v after=%#v", before, after)
	}
	stored, ok, err := NewCache(cacheRoot).LoadV2(config.ID)
	if err != nil || !ok || stored.BaselineID != "wave-1" {
		t.Fatalf("durable generation after failed store = %#v, ok=%v err=%v", stored, ok, err)
	}
}

func TestLimitedLaterWavePublishesFactsWithoutAdvancingCoverage(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	secondStarted := make(chan struct{})
	secondRelease := make(chan struct{})
	calls := 0
	manager := NewManager(Options{
		Cache: NewCache(t.TempDir()), Now: func() time.Time { return now },
		Refresh: func(_ context.Context, _ string, since int64, _ time.Time, _ CachedSnapshotV2) (refreshOutcome, error) {
			calls++
			if calls == 1 {
				return completeRefreshOutcome("wave-1", since, "recent"), nil
			}
			close(secondStarted)
			<-secondRelease
			limited := completeRefreshOutcome("wave-2", 0, "safe-partial")
			limited.Failures = []error{context.DeadlineExceeded}
			return limited, nil
		},
	})
	t.Cleanup(manager.Shutdown)
	if err := manager.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	manager.ListView(0)
	<-secondStarted
	before := manager.DiagnosticsHealth()
	close(secondRelease)
	waitUntil(t, func() bool { return !manager.DiagnosticsHealth().Refreshing })
	after := manager.ListView(0)
	if after.Health.State != StateLimited || after.Health.PublicationID == before.PublicationID {
		t.Fatalf("limited publication health = %#v", after.Health)
	}
	if before.CoverageSinceMs == nil || after.Health.CoverageSinceMs == nil || *after.Health.CoverageSinceMs != *before.CoverageSinceMs {
		t.Fatalf("limited wave advanced coverage: before=%#v after=%#v", before.CoverageSinceMs, after.Health.CoverageSinceMs)
	}
	if len(after.Sessions) != 1 || after.Sessions[0].Session.ID != "safe-partial" {
		t.Fatalf("limited safe facts were not published: %#v", after.Sessions)
	}
}

func TestRestartAfterFirstWaveResumesFromDurableCoverage(t *testing.T) {
	cache := NewCache(t.TempDir())
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	secondStarted := make(chan struct{})
	firstCalls := 0
	first := NewManager(Options{
		Cache: cache, Now: func() time.Time { return now },
		Refresh: func(ctx context.Context, _ string, since int64, _ time.Time, _ CachedSnapshotV2) (refreshOutcome, error) {
			firstCalls++
			if firstCalls == 2 {
				close(secondStarted)
				<-ctx.Done()
				return refreshOutcome{}, ctx.Err()
			}
			return completeRefreshOutcome("wave-1", since, "recent"), nil
		},
	})
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	if err := first.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	first.ListView(0)
	<-secondStarted
	firstPublication := first.DiagnosticsHealth().PublicationID
	first.Shutdown()

	type call struct {
		since      int64
		baselineID string
	}
	called := make(chan call, 1)
	restarted := NewManager(Options{
		Cache: cache, Now: func() time.Time { return now },
		Refresh: func(_ context.Context, _ string, since int64, _ time.Time, baseline CachedSnapshotV2) (refreshOutcome, error) {
			called <- call{since: since, baselineID: baseline.BaselineID}
			return completeRefreshOutcome("wave-2", since, "all"), nil
		},
	})
	t.Cleanup(restarted.Shutdown)
	if err := restarted.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	loaded := restarted.DiagnosticsHealth()
	if loaded.PublicationID == "" || loaded.PublicationID == firstPublication {
		t.Fatalf("restart did not assign a fresh cache publication: before=%q after=%q", firstPublication, loaded.PublicationID)
	}
	restarted.ListView(0)
	if got := <-called; got.since != 0 || got.baselineID != "wave-1" {
		t.Fatalf("restart call = %#v, want all history from wave-1", got)
	}
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

func TestRetryBackoffUsesRemoteRetryInterval(t *testing.T) {
	if got := retryBackoff(1); got != RemoteRetryInterval {
		t.Fatalf("retryBackoff(1) = %v, want %v", got, RemoteRetryInterval)
	}
	if got := retryBackoff(8); got != RemoteRetryInterval {
		t.Fatalf("retryBackoff(8) = %v, want %v", got, RemoteRetryInterval)
	}
}

func TestRetryAllowsOneRefreshAndThrottlesRapidManualRequests(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	release := make(chan struct{})
	manager := NewManager(Options{
		Cache: NewCache(filepath.Join(home, "remote-cache")),
		Now:   func() time.Time { return now },
		Refresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2) (refreshOutcome, error) {
			<-release
			return refreshOutcome{}, context.DeadlineExceeded
		},
	})
	if err := manager.ApplySettings(&settings.RemoteSettings{
		ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, started := manager.Retry(); !started {
		t.Fatal("first manual retry did not start")
	}
	if _, started := manager.Retry(); started {
		t.Fatal("manual retry started while another refresh was running")
	}
	close(release)
	waitUntil(t, func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		return !manager.refreshing
	})
	if _, started := manager.Retry(); started {
		t.Fatal("manual retry ignored its cooldown")
	}
	now = now.Add(ManualRetryCooldown)
	if _, started := manager.Retry(); !started {
		t.Fatal("manual retry did not start after cooldown")
	}
}

func TestOfflineHelperDiscoveryDoesNotStartSecondRefresh(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	var refreshes atomic.Int32
	manager := NewManager(Options{
		Cache:                       NewCache(filepath.Join(home, "remote-cache")),
		Now:                         func() time.Time { return now },
		ReleaseProvider:             deadlineHelperRelease{},
		LifecycleFactory:            func(string) (Lifecycle, error) { return Lifecycle{}, nil },
		HelperInstallationAvailable: true,
		Refresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2) (refreshOutcome, error) {
			refreshes.Add(1)
			return refreshOutcome{}, nil
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
		return manager.helperProbe == helperProbeFallback && !manager.refreshing
	})
	if refreshes.Load() != 0 {
		t.Fatalf("offline discovery started %d collection refreshes", refreshes.Load())
	}
	health := manager.DiagnosticsHealth()
	if health.State != StateConnecting || health.LastCheckedAtMs == nil {
		t.Fatalf("offline discovery health = %#v", health)
	}
}

type fixedHelperRelease struct {
	document SignedReleaseMetadata
	content  []byte
}

type deadlineHelperRelease struct{}

func (deadlineHelperRelease) LoadMetadata(context.Context) (SignedReleaseMetadata, error) {
	return SignedReleaseMetadata{}, context.DeadlineExceeded
}

func (deadlineHelperRelease) LoadArtifact(context.Context, Artifact) ([]byte, error) {
	return nil, context.DeadlineExceeded
}

type blockingHelperRelease struct {
	document SignedReleaseMetadata
	content  []byte
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (provider *blockingHelperRelease) LoadMetadata(context.Context) (SignedReleaseMetadata, error) {
	return provider.document, nil
}

func (provider *blockingHelperRelease) LoadArtifact(context.Context, Artifact) ([]byte, error) {
	provider.once.Do(func() { close(provider.started) })
	<-provider.release
	return provider.content, nil
}

type architectureRelease struct {
	document  SignedReleaseMetadata
	content   map[string][]byte
	requested string
	loads     int
}

func (release *architectureRelease) LoadMetadata(context.Context) (SignedReleaseMetadata, error) {
	return release.document, nil
}

func (release *architectureRelease) LoadArtifact(_ context.Context, artifact Artifact) ([]byte, error) {
	release.loads++
	release.requested = artifact.Arch
	return release.content[artifact.Arch], nil
}

func (release fixedHelperRelease) LoadMetadata(context.Context) (SignedReleaseMetadata, error) {
	return release.document, nil
}

func (release fixedHelperRelease) LoadArtifact(context.Context, Artifact) ([]byte, error) {
	return release.content, nil
}

func TestManagerUsesOnlyLifecycleVerifiedHelperAndDoesNotSilentlyFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	remote, artifact, content := lifecycleFixture(t)
	var sftpCalls atomic.Int32
	var helperCalls atomic.Int32
	manager := NewManager(Options{
		Cache: NewCache(filepath.Join(home, "remote-cache")),
		Now:   time.Now,
		Refresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2) (refreshOutcome, error) {
			sftpCalls.Add(1)
			return refreshOutcome{}, nil
		},
		HelperRefresh: func(_ context.Context, _ string, _ int64, _ time.Time, _ CachedSnapshotV2, target helperTarget) (refreshOutcome, error) {
			helperCalls.Add(1)
			if target.path == "" || target.version != artifact.Version {
				t.Fatalf("unverified helper target = %#v", target)
			}
			return refreshOutcome{Metrics: CollectionMetrics{RequestBytes: 5, ResponseBytes: 9, Records: 2}}, ErrHelperFailed
		},
		ReleaseProvider:             fixedHelperRelease{document: remote.document, content: content},
		LifecycleFactory:            func(string) (Lifecycle, error) { return lifecycleFor(remote), nil },
		HelperInstallationAvailable: true,
	})
	if err := manager.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	health, _ := manager.setupHelper(context.Background(), "", Consent{Install: true})
	if health.Helper == nil || !health.Helper.Compatible || health.Helper.Version != artifact.Version {
		t.Fatalf("setup health = %#v", health)
	}
	manager.ListView(time.Now().Add(-time.Hour).UnixMilli())
	waitUntil(t, func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		return !manager.refreshing
	})
	health = manager.DiagnosticsHealth()
	if helperCalls.Load() != 1 || sftpCalls.Load() != 0 {
		t.Fatalf("helper calls=%d sftp calls=%d", helperCalls.Load(), sftpCalls.Load())
	}
	if health.Transport != TransportHelper || health.Reason == nil || *health.Reason != ReasonHelperFailed {
		t.Fatalf("failure health = %#v", health)
	}
	if health.Metrics.RequestBytes != 5 || health.Metrics.ResponseBytes != 9 || health.Metrics.Records != 2 {
		t.Fatalf("failure metrics = %#v", health.Metrics)
	}
}

func TestManagerReverifiesHelperBeforeEveryRefresh(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	remote, artifact, content := lifecycleFixture(t)
	var helperCalls atomic.Int32
	manager := NewManager(Options{
		Cache: NewCache(filepath.Join(home, "remote-cache")), Now: time.Now,
		HelperRefresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2, helperTarget) (refreshOutcome, error) {
			helperCalls.Add(1)
			return refreshOutcome{}, nil
		},
		ReleaseProvider:             fixedHelperRelease{document: remote.document, content: content},
		LifecycleFactory:            func(string) (Lifecycle, error) { return lifecycleFor(remote), nil },
		HelperInstallationAvailable: true,
	})
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	if health, _ := manager.setupHelper(context.Background(), "", Consent{Install: true}); health.Helper == nil || !health.Helper.Compatible {
		t.Fatalf("setup health = %#v", health)
	}

	targetPath, _ := helperPath(artifact.Version)
	modified := remote.files[targetPath]
	modified.SHA256 = "modified"
	remote.files[targetPath] = modified
	manager.ListView(time.Now().Add(-time.Hour).UnixMilli())
	waitUntil(t, func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		return !manager.refreshing
	})

	if helperCalls.Load() != 0 {
		t.Fatalf("modified helper executed %d times", helperCalls.Load())
	}
	health := manager.DiagnosticsHealth()
	if health.Reason == nil || *health.Reason != ReasonHelperVerification {
		t.Fatalf("modified helper health = %#v", health)
	}
}

func TestPartialRefreshMarksOnlyRetainedFailedFamilyStale(t *testing.T) {
	manager := NewManager(Options{})
	manager.cfg = &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	manager.state = StateLimited
	manager.complete = false
	manager.sessions = []*session.Session{
		{Agent: vendors.AgentClaude, ID: "fresh", Status: strPtr("working")},
		{Agent: vendors.AgentClaude, ID: "retained", Status: strPtr("idle")},
	}
	manager.familyStale = map[remoteSessionKey]bool{{Agent: vendors.AgentClaude, ID: "retained"}: true}
	view := manager.ListView(0)
	if len(view.Sessions) != 2 || view.Sessions[0].DisplayStale || !view.Sessions[1].DisplayStale {
		t.Fatalf("partial stale provenance = %#v", view.Sessions)
	}
	if view.Sessions[1].LastSeenStatus == nil || *view.Sessions[1].LastSeenStatus != "idle" {
		t.Fatalf("retained status = %#v", view.Sessions[1].LastSeenStatus)
	}
}

func TestRestartDiscoversAndReusesVerifiedHelperWithoutInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	remote, artifact, content := lifecycleFixture(t)
	cache := NewCache(filepath.Join(home, "remote-cache"))
	options := Options{
		Cache: cache, Now: time.Now,
		ReleaseProvider:             fixedHelperRelease{document: remote.document, content: content},
		LifecycleFactory:            func(string) (Lifecycle, error) { return lifecycleFor(remote), nil },
		HelperInstallationAvailable: true,
		HelperRefresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2, helperTarget) (refreshOutcome, error) {
			return refreshOutcome{Snapshot: CachedSnapshotV2{Version: cacheV2Version}}, nil
		},
	}
	first := NewManager(options)
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	if err := first.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	if health, _ := first.setupHelper(context.Background(), "", Consent{Install: true}); health.Helper == nil || !health.Helper.Compatible {
		t.Fatalf("initial setup health = %#v", health)
	}
	if remote.installs != 1 {
		t.Fatalf("initial installs = %d, want 1", remote.installs)
	}

	restarted := NewManager(options)
	if err := restarted.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	restarted.ListView(time.Now().Add(-time.Hour).UnixMilli())
	waitUntil(t, func() bool {
		restarted.mu.Lock()
		defer restarted.mu.Unlock()
		return restarted.helperProbe == helperProbeReady && !restarted.refreshing
	})
	if remote.installs != 1 {
		t.Fatalf("restart installed again: installs=%d", remote.installs)
	}
	if health := restarted.DiagnosticsHealth(); health.Transport != TransportHelper || health.Helper == nil || !health.Helper.Compatible || health.Helper.Version != artifact.Version {
		t.Fatalf("restart health = %#v", health)
	}
}

func TestRestartAutomaticallyUpdatesAnOwnedHelper(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	remote, current, content := lifecycleFixture(t)
	cache := NewCache(filepath.Join(home, "remote-cache"))
	removeStarted := make(chan struct{}, 1)
	removeRelease := make(chan struct{})
	var refreshCalls atomic.Int32
	remote.removeStarted = removeStarted
	remote.removeRelease = removeRelease
	options := Options{
		Cache: cache, Now: time.Now,
		ReleaseProvider:             fixedHelperRelease{document: remote.document, content: content},
		LifecycleFactory:            func(string) (Lifecycle, error) { return lifecycleFor(remote), nil },
		HelperInstallationAvailable: true,
		HelperRefresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2, helperTarget) (refreshOutcome, error) {
			refreshCalls.Add(1)
			return refreshOutcome{Snapshot: CachedSnapshotV2{Version: cacheV2Version}}, nil
		},
	}
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	first := NewManager(options)
	if err := first.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	if health, _ := first.setupHelper(context.Background(), "", Consent{Install: true}); health.Helper == nil || !health.Helper.Compatible {
		t.Fatalf("initial setup health = %#v", health)
	}

	previous := current
	previous.Current = false
	updated := current
	updated.Version = "v2"
	remote.document = signDocument(t, ReleaseMetadata{
		Sequence: 2, ExpiresAtUnix: time.Now().Add(time.Hour).Unix(), Artifacts: []Artifact{updated, previous},
	})
	options.ReleaseProvider = fixedHelperRelease{document: remote.document, content: content}
	restarted := NewManager(options)
	if err := restarted.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	restarted.ListView(time.Now().Add(-time.Hour).UnixMilli())
	<-removeStarted
	restarted.ListView(time.Now().Add(-time.Hour).UnixMilli())
	if _, started := restarted.Retry(); started {
		t.Fatal("manual refresh started before previous helper removal completed")
	}
	if refreshCalls.Load() != 0 {
		t.Fatal("helper refresh started before previous helper removal completed")
	}
	close(removeRelease)
	waitUntil(t, func() bool {
		restarted.mu.Lock()
		defer restarted.mu.Unlock()
		return restarted.helperProbe == helperProbeReady && !restarted.helperSetup
	})
	waitUntil(t, func() bool { return refreshCalls.Load() > 0 })
	if remote.installs != 2 || remote.removed != "~/.coslash/helpers/v1/coslash-helper" {
		t.Fatalf("installs=%d removed=%q", remote.installs, remote.removed)
	}
	if health := restarted.DiagnosticsHealth(); health.Helper == nil || !health.Helper.Compatible || health.Helper.Version != "v2" {
		t.Fatalf("updated health = %#v", health)
	}
	ownership, owned, err := cache.LoadHelperOwnership(config.ID)
	if err != nil || !owned || ownership.Version != "v2" {
		t.Fatalf("ownership = %#v, owned=%v, err=%v", ownership, owned, err)
	}
}

func TestLaunchSessionRequiresCurrentHealthyRemote(t *testing.T) {
	manager := NewManager(Options{})
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.state = StateOK
	manager.complete = true
	manager.sessions = []*session.Session{{Agent: vendors.AgentCodex, ID: "session", WorkingDirectory: "/workspace"}}
	manager.mu.Unlock()
	found, alias, err := manager.LaunchSession(config.ID, vendors.AgentCodex, "session", "resume")
	if err != nil || alias != config.SSHAlias || found == nil || found.WorkingDirectory != "/workspace" {
		t.Fatalf("launch session = %#v, %q, %v", found, alias, err)
	}
	preview, err := manager.PreviewSession(config.ID, vendors.AgentCodex, "session", 1)
	if err != nil || preview == nil || preview.WorkingDirectory != "/workspace" {
		t.Fatalf("preview session = %#v, %v", preview, err)
	}
	if found, _, err := manager.LaunchSession(config.ID, vendors.AgentCodex, "missing", "resume"); found != nil || err != nil {
		t.Fatal("missing session was launchable")
	}
	manager.mu.Lock()
	manager.sessions = append(manager.sessions, &session.Session{Agent: vendors.AgentCodex, ID: "compacted"})
	manager.mu.Unlock()
	if _, _, err := manager.LaunchSession(config.ID, vendors.AgentCodex, "compacted", "resume"); !errors.Is(err, ErrRemoteSessionUnavailable) {
		t.Fatalf("compacted session error = %v", err)
	}
	if _, err := manager.PreviewSession(config.ID, vendors.AgentCodex, "compacted", 1); !errors.Is(err, ErrRemoteSessionUnavailable) {
		t.Fatalf("compacted preview error = %v", err)
	}
	manager.mu.Lock()
	manager.state = StateLimited
	manager.mu.Unlock()
	if found, _, err := manager.LaunchSession(config.ID, vendors.AgentCodex, "session", "resume"); found == nil || err != nil {
		t.Fatal("connected limited remote was not launchable")
	}
	if found, err := manager.PreviewSession(config.ID, vendors.AgentCodex, "session", 1); found != nil || err != nil {
		t.Fatal("limited remote was previewable")
	}
	manager.mu.Lock()
	manager.state = StateStale
	manager.mu.Unlock()
	if found, _, err := manager.LaunchSession(config.ID, vendors.AgentCodex, "session", "resume"); found != nil || err != nil {
		t.Fatal("stale remote was launchable")
	}
	busy := "busy"
	manager.mu.Lock()
	manager.state = StateOK
	manager.sessions[0].Status = &busy
	manager.mu.Unlock()
	if _, _, err := manager.LaunchSession(config.ID, vendors.AgentCodex, "session", "resume"); !errors.Is(err, ErrRemoteSessionActive) {
		t.Fatalf("active session error = %v", err)
	}
	if found, _, err := manager.LaunchSession(config.ID, vendors.AgentCodex, "session", "new"); found == nil || err != nil {
		t.Fatalf("busy session could not start fresh: %#v, %v", found, err)
	}
}

func TestAutomaticUpdateRetriesWhenTheRemoteReturns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	remote, current, content := lifecycleFixture(t)
	cache := NewCache(filepath.Join(home, "remote-cache"))
	options := Options{
		Cache: cache, Now: func() time.Time { return now },
		ReleaseProvider:             fixedHelperRelease{document: remote.document, content: content},
		LifecycleFactory:            func(string) (Lifecycle, error) { return lifecycleFor(remote), nil },
		HelperInstallationAvailable: true,
		Refresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2) (refreshOutcome, error) {
			return refreshOutcome{}, context.DeadlineExceeded
		},
	}
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	first := NewManager(options)
	if err := first.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	if health, _ := first.setupHelper(context.Background(), "", Consent{Install: true}); health.Helper == nil || !health.Helper.Compatible {
		t.Fatalf("initial setup health = %#v", health)
	}

	previous := current
	previous.Current = false
	updated := current
	updated.Version = "v2"
	remote.document = signDocument(t, ReleaseMetadata{
		Sequence: 2, ExpiresAtUnix: time.Now().Add(time.Hour).Unix(), Artifacts: []Artifact{updated, previous},
	})
	remote.probeErr = context.DeadlineExceeded
	options.ReleaseProvider = fixedHelperRelease{document: remote.document, content: content}
	restarted := NewManager(options)
	if err := restarted.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	restarted.ListView(0)
	waitUntil(t, func() bool {
		restarted.mu.Lock()
		defer restarted.mu.Unlock()
		return restarted.helperProbe == helperProbeFallback && !restarted.refreshing
	})
	if remote.installs != 1 {
		t.Fatalf("offline update installs = %d", remote.installs)
	}

	now = now.Add(RemoteRetryInterval)
	remote.probeErr = nil
	restarted.ListView(0)
	waitUntil(t, func() bool {
		restarted.mu.Lock()
		defer restarted.mu.Unlock()
		return restarted.helperProbe == helperProbeReady && !restarted.helperSetup
	})
	if remote.installs != 2 {
		t.Fatalf("returned remote installs = %d", remote.installs)
	}
}

func TestHelperTestAcceptsEmptySuccessfulCollectionWithoutRewritingBoardState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	lifecycleRemote, _, content := lifecycleFixture(t)
	manager := NewManager(Options{
		Cache: NewCache(filepath.Join(home, "remote-cache")), Now: func() time.Time { return now },
		ReleaseProvider:             fixedHelperRelease{document: lifecycleRemote.document, content: content},
		LifecycleFactory:            func(string) (Lifecycle, error) { return lifecycleFor(lifecycleRemote), nil },
		HelperInstallationAvailable: true,
		HelperRefresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2, helperTarget) (refreshOutcome, error) {
			return refreshOutcome{Snapshot: CachedSnapshotV2{}}, nil
		},
	})
	t.Cleanup(manager.Shutdown)
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	if health, _ := manager.setupHelper(context.Background(), "", Consent{Install: true}); health.Helper == nil || !health.Helper.Compatible {
		t.Fatalf("setup health = %#v", health)
	}
	manager.mu.Lock()
	manager.snapshot = &CachedSnapshotV2{
		Version: cacheV2Version, CoverageSinceMs: now.UnixMilli(), FetchedAtMs: now.UnixMilli(),
	}
	manager.sessions = []*session.Session{{Agent: vendors.AgentClaude, ID: "cached"}}
	manager.state = StateStale
	manager.reason = reasonPtr(ReasonInitialRefresh)
	manager.complete = false
	manager.errorCopy = "prior refresh is stale"
	manager.transport = TransportSFTP
	manager.requestedSinceMs = 0
	manager.requestedSinceSet = true
	manager.mu.Unlock()
	result := manager.TestHelper(context.Background())
	if !result.Succeeded {
		t.Fatalf("empty helper test was not successful: %#v", result)
	}
	if result.Health.State != StateStale || result.Health.Complete || result.Health.Reason == nil || *result.Health.Reason != ReasonInitialRefresh {
		t.Fatalf("helper test rewrote board health = %#v", result.Health)
	}
	if result.Health.Transport != TransportHelper {
		t.Fatalf("successful helper test transport = %q, want helper", result.Health.Transport)
	}
	if view := manager.ListView(0); len(view.Sessions) != 1 || view.Sessions[0].EligibleForAggregates {
		t.Fatalf("stale cached session became aggregate eligible: %#v", view.Sessions)
	}
}

func TestDiscoveryClearsInMemoryOwnershipWhenOwnershipWriteFails(t *testing.T) {
	home := t.TempDir()
	remote, artifact, content := lifecycleFixture(t)
	cache := NewCache(filepath.Join(home, "remote-cache"))
	path, err := helperPath(artifact.Version)
	if err != nil {
		t.Fatal(err)
	}
	remote.files[path] = remoteFile(path, artifact, remote.platform.UID)
	manager := NewManager(Options{
		Cache: cache, ReleaseProvider: fixedHelperRelease{document: remote.document, content: content},
		LifecycleFactory:            func(string) (Lifecycle, error) { return lifecycleFor(remote), nil },
		HelperInstallationAvailable: true,
	})
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	ownershipPath, err := cache.helperOwnershipPath(config.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ownershipPath, 0o700); err != nil {
		t.Fatal(err)
	}
	manager.ListView(0)
	waitUntil(t, func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		return manager.helperProbe == helperProbeFallback
	})
	health := manager.DiagnosticsHealth()
	if health.HelperOwnershipRecorded || manager.helperVersion != "" || manager.helperTarget != nil {
		t.Fatalf("failed ownership write left in-memory ownership: health=%#v version=%q target=%#v", health, manager.helperVersion, manager.helperTarget)
	}
}

func TestHelperTestFailureDoesNotPoisonRefreshOrTransport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	lifecycleRemote, _, content := lifecycleFixture(t)
	manager := NewManager(Options{
		Cache: NewCache(filepath.Join(home, "remote-cache")), Now: func() time.Time { return now },
		ReleaseProvider:             fixedHelperRelease{document: lifecycleRemote.document, content: content},
		LifecycleFactory:            func(string) (Lifecycle, error) { return lifecycleFor(lifecycleRemote), nil },
		HelperInstallationAvailable: true,
		HelperRefresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2, helperTarget) (refreshOutcome, error) {
			return refreshOutcome{Stderr: "probe failed"}, context.DeadlineExceeded
		},
	})
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	if health, _ := manager.setupHelper(context.Background(), "", Consent{Install: true}); health.Helper == nil || !health.Helper.Compatible {
		t.Fatalf("setup health = %#v", health)
	}
	manager.mu.Lock()
	manager.snapshot = &CachedSnapshotV2{Version: cacheV2Version, CoverageSinceMs: now.UnixMilli()}
	manager.state = StateOK
	manager.complete = true
	manager.transport = TransportSFTP
	manager.failures = 0
	manager.nextRetryAt = time.Time{}
	manager.requestedSinceMs = 0
	manager.requestedSinceSet = true
	manager.mu.Unlock()

	result := manager.TestHelper(context.Background())
	if result.Succeeded || result.Reason == nil {
		t.Fatalf("failed helper test = %#v", result)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.failures != 0 || !manager.nextRetryAt.IsZero() {
		t.Fatalf("setup probe poisoned retry state: failures=%d nextRetryAt=%v", manager.failures, manager.nextRetryAt)
	}
	if manager.transport != TransportSFTP {
		t.Fatalf("failed probe transport = %q, want prior sftp", manager.transport)
	}
	if manager.state != StateOK || !manager.complete {
		t.Fatalf("failed probe rewrote board state: state=%s complete=%v", manager.state, manager.complete)
	}
	if result.Health.Transport != TransportSFTP || result.Health.State != StateOK {
		t.Fatalf("failed probe health = %#v", result.Health)
	}
}

func TestHelperOwnershipBlocksAliasChangeUntilExplicitRelease(t *testing.T) {
	cache := NewCache(t.TempDir())
	manager := NewManager(Options{Cache: cache})
	first := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "old-host", Enabled: true}
	if err := cache.StoreHelperVersion(first.ID, "v1", first.SSHAlias); err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplySettings(first); err != nil {
		t.Fatal(err)
	}
	changed := *first
	changed.SSHAlias = "new-host"
	if err := manager.ApplySettings(&changed); !errors.Is(err, ErrHelperOwnershipConflict) {
		t.Fatalf("alias replacement error = %v", err)
	}
	if err := manager.ReleaseHelperOwnership(); err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplySettings(&changed); err != nil {
		t.Fatalf("explicit release should allow alias replacement: %v", err)
	}
}

func TestSetupBlocksAliasChangeUntilItRecordsOwnership(t *testing.T) {
	remote, _, content := lifecycleFixture(t)
	provider := &blockingHelperRelease{
		document: remote.document, content: content,
		started: make(chan struct{}), release: make(chan struct{}),
	}
	cache := NewCache(t.TempDir())
	manager := NewManager(Options{
		Cache: cache, ReleaseProvider: provider,
		LifecycleFactory:            func(string) (Lifecycle, error) { return lifecycleFor(remote), nil },
		HelperInstallationAvailable: true,
	})
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "old-host", Enabled: true}
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	setupDone := make(chan error, 1)
	go func() {
		_, err := manager.SetupHelperForAlias(context.Background(), config.SSHAlias, Consent{Install: true})
		setupDone <- err
	}()
	<-provider.started

	changed := *config
	changed.SSHAlias = "new-host"
	if err := manager.ValidateSettingsChange(&changed); !errors.Is(err, ErrHelperSetupInProgress) {
		t.Fatalf("validate alias change during setup = %v, want ErrHelperSetupInProgress", err)
	}
	if err := manager.ApplySettings(&changed); !errors.Is(err, ErrHelperSetupInProgress) {
		t.Fatalf("apply alias change during setup = %v, want ErrHelperSetupInProgress", err)
	}

	close(provider.release)
	if err := <-setupDone; err != nil {
		t.Fatalf("SetupHelperForAlias: %v", err)
	}
	ownership, owned, err := cache.LoadHelperOwnership(config.ID)
	if err != nil || !owned || ownership.Alias != config.SSHAlias {
		t.Fatalf("ownership after setup = %#v, owned=%v, err=%v", ownership, owned, err)
	}
	if err := manager.ApplySettings(&changed); !errors.Is(err, ErrHelperOwnershipConflict) {
		t.Fatalf("alias change after setup = %v, want ErrHelperOwnershipConflict", err)
	}
}

func TestSetupRetriesTransportFailureWithFreshControlMaster(t *testing.T) {
	first, _, content := lifecycleFixture(t)
	first.installErr = wrapSSHError(errors.New("install failed"), "Connection reset by peer")
	second, _, _ := lifecycleFixture(t)
	var factories, resets int
	manager := NewManager(Options{
		Cache: NewCache(t.TempDir()), ReleaseProvider: fixedHelperRelease{document: first.document, content: content},
		LifecycleFactory: func(string) (Lifecycle, error) {
			factories++
			if factories == 1 {
				return lifecycleFor(first), nil
			}
			return lifecycleFor(second), nil
		},
		ResetControlMaster:          func(string) { resets++ },
		HelperInstallationAvailable: true,
	})
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	health, err := manager.SetupHelperForAlias(context.Background(), config.SSHAlias, Consent{Install: true})
	if err != nil {
		t.Fatal(err)
	}
	if resets != 1 || factories != 2 || health.Helper == nil || !health.Helper.Compatible {
		t.Fatalf("resets=%d factories=%d health=%#v", resets, factories, health)
	}
}

func TestSetupRemovesPriorOwnedHelperAfterVerification(t *testing.T) {
	remote, artifact, content := lifecycleFixture(t)
	remote.removeStarted = make(chan struct{}, 1)
	remote.removeRelease = make(chan struct{})
	cache := NewCache(t.TempDir())
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	if err := cache.StoreHelperVersion(config.ID, "v0", config.SSHAlias); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Options{
		Cache: cache, ReleaseProvider: fixedHelperRelease{document: remote.document, content: content},
		LifecycleFactory:            func(string) (Lifecycle, error) { return lifecycleFor(remote), nil },
		HelperInstallationAvailable: true,
	})
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	type setupResult struct {
		health Health
		err    error
	}
	setupDone := make(chan setupResult, 1)
	go func() {
		health, err := manager.SetupHelperForAlias(context.Background(), config.SSHAlias, Consent{Install: true})
		setupDone <- setupResult{health: health, err: err}
	}()
	<-remote.removeStarted

	manager.ListView(0)
	if _, started := manager.Retry(); started {
		t.Fatal("refresh started before prior helper cleanup completed")
	}
	manager.mu.Lock()
	target, probe, version := manager.helperTarget, manager.helperProbe, manager.helperVersion
	manager.mu.Unlock()
	if target != nil || probe != helperProbeProbing || version != "v0" {
		t.Fatalf("upgrade was published during cleanup: target=%#v probe=%q version=%q", target, probe, version)
	}
	if ownership, owned, err := cache.LoadHelperOwnership(config.ID); err != nil || !owned || ownership.Version != "v0" {
		t.Fatalf("ownership during cleanup = %#v, owned=%v, err=%v", ownership, owned, err)
	}

	close(remote.removeRelease)
	result := <-setupDone
	if result.err != nil {
		t.Fatal(result.err)
	}
	health := result.health
	if health.Helper == nil || !health.Helper.Compatible {
		t.Fatalf("setup health = %#v", health)
	}
	if remote.removed != "~/.coslash/helpers/v0/coslash-helper" {
		t.Fatalf("removed = %q", remote.removed)
	}
	if ownership, owned, err := cache.LoadHelperOwnership(config.ID); err != nil || !owned || ownership.Version != artifact.Version {
		t.Fatalf("ownership after cleanup = %#v, owned=%v, err=%v", ownership, owned, err)
	}
}

func TestSetupCleanupFailureDoesNotPublishUpgrade(t *testing.T) {
	remote, _, content := lifecycleFixture(t)
	remote.removeErr = context.DeadlineExceeded
	cache := NewCache(t.TempDir())
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	if err := cache.StoreHelperVersion(config.ID, "v0", config.SSHAlias); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Options{
		Cache: cache, ReleaseProvider: fixedHelperRelease{document: remote.document, content: content},
		LifecycleFactory:            func(string) (Lifecycle, error) { return lifecycleFor(remote), nil },
		HelperInstallationAvailable: true,
	})
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	health, err := manager.SetupHelperForAlias(context.Background(), config.SSHAlias, Consent{Install: true})
	if err != nil {
		t.Fatal(err)
	}
	if health.Helper == nil || health.Helper.Compatible || health.Helper.Reason == nil || *health.Helper.Reason != ReasonHelperInstallation {
		t.Fatalf("cleanup failure health = %#v", health)
	}
	manager.mu.Lock()
	target, probe, version := manager.helperTarget, manager.helperProbe, manager.helperVersion
	manager.mu.Unlock()
	if target != nil || probe != helperProbeFallback || version != "v0" {
		t.Fatalf("failed cleanup published upgrade: target=%#v probe=%q version=%q", target, probe, version)
	}
	if ownership, owned, err := cache.LoadHelperOwnership(config.ID); err != nil || !owned || ownership.Version != "v0" {
		t.Fatalf("ownership after failed cleanup = %#v, owned=%v, err=%v", ownership, owned, err)
	}
}

func TestHealthReportsRecordedOwnershipEvenWhenNoHelperVersionIsInspectable(t *testing.T) {
	cache := NewCache(t.TempDir())
	manager := NewManager(Options{Cache: cache})
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	if err := cache.StoreHelperVersion(config.ID, "v1", config.SSHAlias); err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	health := manager.DiagnosticsHealth()
	if !health.HelperOwnershipRecorded || health.Helper != nil {
		t.Fatalf("ownership must not be inferred from an inspectable helper version: %#v", health)
	}
}

func TestCorruptOwnershipBlocksAliasReplacement(t *testing.T) {
	cache := NewCache(t.TempDir())
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	path, err := cache.helperOwnershipPath(config.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":"?"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Options{Cache: cache})
	changed := *config
	changed.SSHAlias = "new-host"
	if err := manager.ValidateSettingsChange(&changed); !errors.Is(err, ErrHelperOwnershipCorrupt) {
		t.Fatalf("corrupt ownership replacement error = %v", err)
	}
}

func TestFailedUninstallRetainsHostAndOwnership(t *testing.T) {
	cache := NewCache(t.TempDir())
	manager := NewManager(Options{Cache: cache})
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	if err := cache.StoreHelperVersion(config.ID, "v1", config.SSHAlias); err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	if err := manager.UninstallHelper(context.Background()); err == nil {
		t.Fatal("uninstall without a release provider unexpectedly succeeded")
	}
	if manager.cfg == nil || manager.cfg.SSHAlias != config.SSHAlias || manager.helperVersion != "v1" {
		t.Fatalf("failed uninstall lost ownership: cfg=%#v version=%q", manager.cfg, manager.helperVersion)
	}
}

func TestInterruptedUninstallRetainsHostOwnershipAndHelper(t *testing.T) {
	home := t.TempDir()
	remote, artifact, content := lifecycleFixture(t)
	cache := NewCache(home)
	manager := NewManager(Options{
		Cache: cache, ReleaseProvider: fixedHelperRelease{document: remote.document, content: content},
		LifecycleFactory:            func(string) (Lifecycle, error) { return lifecycleFor(remote), nil },
		HelperInstallationAvailable: true,
	})
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	if health, _ := manager.setupHelper(context.Background(), "", Consent{Install: true}); health.Helper == nil || !health.Helper.Compatible {
		t.Fatalf("setup health = %#v", health)
	}
	remote.removeErr = context.DeadlineExceeded
	if err := manager.UninstallHelper(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("UninstallHelper error = %v, want deadline", err)
	}
	path, _ := helperPath(artifact.Version)
	if manager.cfg == nil || manager.helperVersion != artifact.Version || remote.files[path].Path == "" {
		t.Fatalf("interrupted uninstall lost recovery state: cfg=%#v version=%q files=%#v", manager.cfg, manager.helperVersion, remote.files)
	}
	if ownership, ok, err := cache.LoadHelperOwnership(config.ID); err != nil || !ok || ownership.Version != artifact.Version {
		t.Fatalf("interrupted uninstall lost ownership: %#v, ok=%v, err=%v", ownership, ok, err)
	}
}

func TestProductionManagerHasPrivateSequenceStoreAndFeatureGatedProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	manager, err := NewProductionManager()
	if err != nil {
		t.Fatal(err)
	}
	if manager.releaseProvider == nil || manager.lifecycleFactory == nil || manager.helperInstallationAvailable {
		t.Fatalf("production manager is not safely feature gated: %#v", manager)
	}
	store, ok := manager.trust.Sequences.(*FileMetadataSequenceStore)
	if !ok || store.Path != filepath.Join(home, "helper-release", "sequence") {
		t.Fatalf("sequence store = %#v", manager.trust.Sequences)
	}
}

func TestSetupFetchesOnlyTheSelectedArchitectureArtifact(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			remote, _, _ := lifecycleFixture(t)
			remote.platform.Arch = arch
			remote.capabilities.Arch = arch
			content := syntheticELF(arch)
			artifact := Artifact{Version: "v1", OS: "linux", Arch: arch, Size: int64(len(content)), SHA256: digest(content), Protocol: remoteprotocol.VersionRange{Min: 1, Max: 1}, Schema: remoteprotocol.VersionRange{Min: remotefacts.SchemaVersion, Max: remotefacts.SchemaVersion}, Current: true}
			remote.document = signDocument(t, ReleaseMetadata{Sequence: 1, ExpiresAtUnix: time.Now().Add(time.Hour).Unix(), Artifacts: []Artifact{artifact}})
			provider := &architectureRelease{document: remote.document, content: map[string][]byte{arch: content}}
			manager := NewManager(Options{
				Cache: NewCache(t.TempDir()), ReleaseProvider: provider,
				LifecycleFactory:            func(string) (Lifecycle, error) { return lifecycleFor(remote), nil },
				HelperInstallationAvailable: true,
			})
			if err := manager.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}); err != nil {
				t.Fatal(err)
			}
			if health, _ := manager.setupHelper(context.Background(), "", Consent{Install: true}); health.Helper == nil || !health.Helper.Compatible {
				t.Fatalf("setup health = %#v", health)
			}
			if provider.requested != arch {
				t.Fatalf("requested artifact = %q, want %q", provider.requested, arch)
			}
		})
	}
}

func TestHelperFallbackPolicy(t *testing.T) {
	for _, test := range []struct {
		name   string
		result LifecycleResult
		want   bool
	}{
		{"missing", LifecycleResult{State: LifecycleSFTP}, true},
		{"unsupported", LifecycleResult{State: LifecycleUnsupported}, true},
		{"blocked", LifecycleResult{State: LifecycleSFTP, Reason: ErrHelperNoExec}, true},
		{"incompatible", LifecycleResult{State: LifecycleUpgradeRequired, Reason: ErrHelperIncompatible}, true},
		{"revoked", LifecycleResult{State: LifecycleRevoked, Reason: ErrHelperRevoked}, true},
		{"verification", LifecycleResult{State: LifecycleVerificationError, Reason: ErrHelperVerification}, true},
		{"installation", LifecycleResult{State: LifecycleSFTP, Reason: ErrHelperInstallation}, true},
		{"verified", LifecycleResult{State: LifecycleReady, CanExecute: true}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := useSFTPFallbackAfterDiscovery(test.result); got != test.want {
				t.Fatalf("useSFTPFallbackAfterDiscovery(%#v) = %v, want %v", test.result, got, test.want)
			}
		})
	}
}

func TestReadOnlyDiscoveryDoesNotFetchAnArtifact(t *testing.T) {
	remote, artifact, content := lifecycleFixture(t)
	provider := &architectureRelease{document: remote.document, content: map[string][]byte{artifact.Arch: content}}
	manager := NewManager(Options{
		Cache: NewCache(t.TempDir()), ReleaseProvider: provider,
		LifecycleFactory:            func(string) (Lifecycle, error) { return lifecycleFor(remote), nil },
		HelperInstallationAvailable: true,
	})
	if err := manager.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	manager.ListView(time.Now().Add(-time.Hour).UnixMilli())
	waitUntil(t, func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		return manager.helperProbe == helperProbeFallback
	})
	if provider.loads != 0 {
		t.Fatalf("read-only discovery downloaded %d artifacts", provider.loads)
	}
}

func TestSuccessfulAliasTestClearsBackoffAndKicksRefresh(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	refreshed := make(chan struct{}, 1)
	manager := NewManager(Options{
		Cache: NewCache(filepath.Join(home, "remote-cache")),
		Now:   func() time.Time { return now },
		Test: func(context.Context, string) (probeResult, error) {
			return probeResult{RoundTrip: 12 * time.Millisecond}, nil
		},
		Refresh: func(_ context.Context, _ string, since int64, _ time.Time, _ CachedSnapshotV2) (refreshOutcome, error) {
			select {
			case refreshed <- struct{}{}:
			default:
			}
			return refreshOutcome{
				Snapshot: CachedSnapshotV2{
					Version:         cacheV2Version,
					CoverageSinceMs: since,
					// Non-empty coverage avoids ReasonNoSupportedData, which would
					// re-arm limited-state backoff and hide the recovery under test.
					Coverage: []AgentCoverage{{Agent: "claude", CandidateFiles: 1, SelectedFiles: 1}},
				},
				RoundTrip: 20 * time.Millisecond,
			}, nil
		},
	})
	if err := manager.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.state = StateError
	manager.reason = reasonPtr(ReasonConnectionFailed)
	manager.failures = 2
	manager.nextRetryAt = now.Add(30 * time.Minute)
	manager.requestedSinceMs = now.Add(-time.Hour).UnixMilli()
	manager.requestedSinceSet = true
	manager.mu.Unlock()

	health, err := manager.TestAlias(context.Background(), "agent-box")
	if err != nil {
		t.Fatal(err)
	}
	if health.State != StateOK {
		t.Fatalf("test health = %#v", health)
	}
	select {
	case <-refreshed:
	case <-time.After(2 * time.Second):
		t.Fatal("successful test did not kick a board refresh")
	}
	waitUntil(t, func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		return manager.state == StateOK && manager.failures == 0 && manager.nextRetryAt.IsZero() && !manager.refreshing
	})
}

func strPtr(value string) *string { return &value }

func completeRefreshOutcome(baselineID string, since int64, sessionID string) refreshOutcome {
	return refreshOutcome{
		Snapshot: CachedSnapshotV2{
			Version: cacheV2Version, BaselineID: baselineID, CoverageSinceMs: since,
			Coverage: []AgentCoverage{{Agent: vendors.AgentClaude, CandidateFiles: 1, SelectedFiles: 1}},
		},
		Sessions: []*session.Session{{
			Agent: vendors.AgentClaude, ID: sessionID, WorkingDirectory: "/work", LastActivityTime: time.Now().UnixMilli(),
		}},
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
