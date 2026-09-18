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

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestFullRecordRevisionsBuildsSessionIndex(t *testing.T) {
	snapshot := &CachedSnapshotV2{FullRecords: []remoteprotocol.FullRecord{
		{Record: fullsessionv1.Record{Agent: vendors.AgentCodex, SessionID: "one", RevisionID: "rev-one"}},
		{Record: fullsessionv1.Record{Agent: vendors.AgentCodex, SessionID: "two", RevisionID: "rev-two"}},
	}}
	revisions := fullRecordRevisions(snapshot)
	if revisions[remoteSessionKey{Agent: vendors.AgentCodex, ID: "one"}] != "rev-one" ||
		revisions[remoteSessionKey{Agent: vendors.AgentCodex, ID: "two"}] != "rev-two" {
		t.Fatalf("revision index = %#v", revisions)
	}
}

func TestPrunedRefreshPublishesLimitedCoverage(t *testing.T) {
	const sourceID = "r_0123456789abcdef"
	manager := NewManager(Options{Cache: NewCache(t.TempDir())})
	manager.cfg = &settings.RemoteSettings{ID: sourceID, SSHAlias: "agent-box", Enabled: true}
	result := refreshOutcome{
		Snapshot: completeCodexSnapshot(t, "generation", "body\n"),
		Sessions: []*session.Session{{Agent: vendors.AgentCodex, ID: "root-1"}},
	}
	result.Snapshot.RequestComplete = true
	result.Snapshot.Coverage = []AgentCoverage{{Agent: vendors.AgentCodex, CandidateFiles: 1, SelectedFiles: 1}}
	prepared, err := manager.prepareRefreshSnapshot(sourceID, &result, 4_000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !prepared.pruned || result.Reason == nil || *result.Reason != ReasonHistoryTruncated {
		t.Fatalf("prepared refresh: pruned=%v reason=%v", prepared.pruned, result.Reason)
	}
	manager.mu.Lock()
	manager.applyLimitedLocked(result, prepared, *result.Reason, 4_000)
	state, complete := manager.state, manager.complete
	manager.mu.Unlock()
	if state != StateLimited || complete {
		t.Fatalf("pruned refresh health: state=%s complete=%v", state, complete)
	}
}

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

func TestIncompleteRefreshRetainsLastCompleteGenerationAcrossRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	cache := NewCache(filepath.Join(home, "remote-cache"))
	const sourceID = "r_0123456789abcdef"
	prior := completeCodexSnapshot(t, "complete-generation", "last good body\n")
	if err := cache.StoreV2(sourceID, prior); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Options{
		Cache:        cache,
		Now:          func() time.Time { return now },
		HelperVerify: func(context.Context, string, helperTarget) error { return nil },
		HelperRefresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2, helperTarget) (refreshOutcome, error) {
			return refreshOutcome{
				Sessions: []*session.Session{{Agent: vendors.AgentCodex, ID: "partial"}},
				Snapshot: CachedSnapshotV2{
					Version: cacheV2Version, SourceID: sourceID, BaselineID: "incomplete-generation",
					Families: prior.Families, FullRecords: prior.FullRecords,
					Coverage: []AgentCoverage{{
						Agent: vendors.AgentCodex, CandidateFiles: 2, SelectedFiles: 1,
					}},
				},
			}, ErrHelperFailed
		},
	})
	if err := manager.ApplySettings(&settings.RemoteSettings{ID: sourceID, SSHAlias: "agent-box", Enabled: true}); err != nil {
		t.Fatalf("ApplySettings: %v", err)
	}
	manager.mu.Lock()
	manager.helperTarget = &helperTarget{path: "/helper"}
	manager.mu.Unlock()
	manager.ListView(0)
	waitUntil(t, func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		return !manager.refreshing && manager.state == StateStale
	})

	if view := manager.ListView(0); view.Health.State != StateStale {
		t.Fatalf("state=%s, want stale", view.Health.State)
	}
	loaded, ok, err := cache.LoadV2(sourceID)
	if err != nil || !ok {
		t.Fatalf("LoadV2 after incomplete refresh: ok=%v err=%v", ok, err)
	}
	if loaded.BaselineID != "complete-generation" {
		t.Fatalf("published incomplete generation: %#v", loaded)
	}
	manager.Shutdown()

	restarted := NewManager(Options{Cache: cache})
	t.Cleanup(restarted.Shutdown)
	if err := restarted.ApplySettings(&settings.RemoteSettings{ID: sourceID, SSHAlias: "agent-box", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	revision := prior.FullRecords[0].Record.RevisionID
	record, err := restarted.ReadFullSession(sourceID, vendors.AgentCodex, "root-1", revision)
	if err != nil || record == nil || record.RevisionID != revision {
		t.Fatalf("restart exact read: record=%#v err=%v", record, err)
	}
}

func TestReadFullSessionReportsCorruptCurrentRecord(t *testing.T) {
	const sourceID = "r_0123456789abcdef"
	snapshot := completeCodexSnapshot(t, "generation-1", "original body\n")
	revision := snapshot.FullRecords[0].Record.RevisionID
	snapshot.FullRecords[0].Record.Session.FileEdits[0].Changes[0].Text = "tampered body\n"
	manager := NewManager(Options{Cache: NewCache(t.TempDir())})
	manager.cfg = &settings.RemoteSettings{ID: sourceID, SSHAlias: "agent-box", Enabled: true}
	manager.snapshot = &snapshot

	if _, err := manager.ReadFullSession(sourceID, vendors.AgentCodex, "root-1", revision); !errors.Is(err, ErrRemoteRecordCorrupt) {
		t.Fatalf("ReadFullSession error = %v, want %v", err, ErrRemoteRecordCorrupt)
	}
}

func TestFullSessionShareUsesDisclosedLocalOnlyRepositoryFallback(t *testing.T) {
	const sourceID = "r_0123456789abcdef"
	snapshot := completeCodexSnapshot(t, "complete-generation", "body\n")
	record := snapshot.FullRecords[0].Record
	record.Session.WorkingDirectory = "/workspace/coslash"
	frozen, err := fullsessionv1.Freeze(record)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.FullRecords[0].Record = frozen
	cost := 1.0
	manager := &Manager{
		cfg:      &settings.RemoteSettings{ID: sourceID, Enabled: true},
		state:    StateOK,
		complete: true,
		snapshot: &snapshot,
		sessions: []*session.Session{{Agent: vendors.AgentCodex, ID: frozen.SessionID, Cost: &cost}},
	}
	got, repository, localOnly, err := manager.ReadFullSessionForShare(sourceID, vendors.AgentCodex, frozen.SessionID, frozen.RevisionID)
	if err != nil || got == nil || repository != "coslash" || !localOnly {
		t.Fatalf("record=%#v repository=%q localOnly=%t err=%v", got, repository, localOnly, err)
	}
}

func TestFullSessionShareRequiresCurrentEligibleRow(t *testing.T) {
	const sourceID = "r_0123456789abcdef"
	snapshot := completeCodexSnapshot(t, "complete-generation", "body\n")
	record := snapshot.FullRecords[0].Record
	cost := 1.0
	running := "busy"
	for _, test := range []struct {
		name        string
		state       State
		complete    bool
		private     bool
		status      *string
		cost        *float64
		familyStale bool
		want        bool
	}{
		{name: "eligible", state: StateOK, complete: true, cost: &cost, want: true},
		{name: "unhealthy", state: StateStale, complete: true, cost: &cost},
		{name: "incomplete", state: StateOK, cost: &cost},
		{name: "private", state: StateOK, complete: true, private: true, cost: &cost},
		{name: "running", state: StateOK, complete: true, status: &running, cost: &cost},
		{name: "failed", state: StateOK, complete: true},
		{name: "stale row", state: StateOK, complete: true, cost: &cost, familyStale: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := &Manager{
				cfg:         &settings.RemoteSettings{ID: sourceID, Enabled: true},
				state:       test.state,
				complete:    test.complete,
				snapshot:    &snapshot,
				sessions:    []*session.Session{{Agent: record.Agent, ID: record.SessionID, RepositoryLocalOnly: test.private, Status: test.status, Cost: test.cost}},
				familyStale: map[remoteSessionKey]bool{{Agent: record.Agent, ID: record.SessionID}: test.familyStale},
			}
			got, _, _, err := manager.ReadFullSessionForShare(sourceID, record.Agent, record.SessionID, record.RevisionID)
			if err != nil || (got != nil) != test.want {
				t.Fatalf("record present=%t err=%v, want present=%t", got != nil, err, test.want)
			}
		})
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
	options := Options{
		Cache: cache, Now: time.Now,
		ReleaseProvider:             fixedHelperRelease{document: remote.document, content: content},
		LifecycleFactory:            func(string) (Lifecycle, error) { return lifecycleFor(remote), nil },
		HelperInstallationAvailable: true,
		HelperRefresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2, helperTarget) (refreshOutcome, error) {
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
	waitUntil(t, func() bool {
		restarted.mu.Lock()
		defer restarted.mu.Unlock()
		return restarted.helperProbe == helperProbeReady && !restarted.helperAutoSetup
	})
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
	t.Setenv("COSLASH_HOME", t.TempDir())
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
	remote, _, content := lifecycleFixture(t)
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
	if health.Helper == nil || !health.Helper.Compatible {
		t.Fatalf("setup health = %#v", health)
	}
	if remote.removed != "~/.coslash/helpers/v0/coslash-helper" {
		t.Fatalf("removed = %q", remote.removed)
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
		Refresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2) (refreshOutcome, error) {
			select {
			case refreshed <- struct{}{}:
			default:
			}
			return refreshOutcome{
				Snapshot: CachedSnapshotV2{
					Version:         cacheV2Version,
					CoverageSinceMs: now.UnixMilli(),
					RequestComplete: true,
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
