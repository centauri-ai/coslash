package remote

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

type fixedHelperRelease struct {
	document SignedReleaseMetadata
	content  []byte
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
	health := manager.SetupHelper(context.Background(), Consent{Install: true})
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
	if health := manager.SetupHelper(context.Background(), Consent{Install: true}); health.Helper == nil || !health.Helper.Compatible {
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
	if health := first.SetupHelper(context.Background(), Consent{Install: true}); health.Helper == nil || !health.Helper.Compatible {
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
	config := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}
	if err := manager.ApplySettings(config); err != nil {
		t.Fatal(err)
	}
	if health := manager.SetupHelper(context.Background(), Consent{Install: true}); health.Helper == nil || !health.Helper.Compatible {
		t.Fatalf("setup health = %#v", health)
	}
	manager.mu.Lock()
	manager.snapshot = &CachedSnapshotV2{Version: cacheV2Version, CoverageSinceMs: now.UnixMilli()}
	manager.sessions = []*session.Session{{Agent: vendors.AgentClaude, ID: "cached"}}
	manager.state = StateStale
	manager.reason = reasonPtr(ReasonInitialRefresh)
	manager.complete = false
	manager.errorCopy = "prior refresh is stale"
	manager.transport = TransportSFTP
	manager.lastRequestedMs = 0
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
	if health := manager.SetupHelper(context.Background(), Consent{Install: true}); health.Helper == nil || !health.Helper.Compatible {
		t.Fatalf("setup health = %#v", health)
	}
	manager.mu.Lock()
	manager.snapshot = &CachedSnapshotV2{Version: cacheV2Version, CoverageSinceMs: now.UnixMilli()}
	manager.state = StateOK
	manager.complete = true
	manager.transport = TransportSFTP
	manager.failures = 0
	manager.nextRetryAt = time.Time{}
	manager.lastRequestedMs = 0
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
	if health := manager.SetupHelper(context.Background(), Consent{Install: true}); health.Helper == nil || !health.Helper.Compatible {
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
			artifact := Artifact{Version: "v1", OS: "linux", Arch: arch, Size: int64(len(content)), SHA256: digest(content), Protocol: remoteprotocol.VersionRange{Min: 1, Max: 1}, Schema: remoteprotocol.VersionRange{Min: 1, Max: 1}, Current: true}
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
			if health := manager.SetupHelper(context.Background(), Consent{Install: true}); health.Helper == nil || !health.Helper.Compatible {
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
		Refresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2) (refreshOutcome, error) {
			select {
			case refreshed <- struct{}{}:
			default:
			}
			return refreshOutcome{
				Snapshot: CachedSnapshotV2{
					Version:         cacheV2Version,
					CoverageSinceMs: now.UnixMilli(),
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
	manager.lastRequestedMs = now.Add(-time.Hour).UnixMilli()
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
