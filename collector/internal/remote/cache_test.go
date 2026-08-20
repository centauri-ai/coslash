package remote

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

func sampleView(since, now int64) remoteviewv1.View {
	if now < 10_000 {
		now = 10_000
	}
	return remoteviewv1.View{
		SchemaVersion:    remoteviewv1.SchemaVersion,
		CollectorVersion: "dev",
		Capabilities:     []string{remoteviewv1.CapabilityRemoteView},
		LaunchableAgents: []string{remoteviewv1.AgentClaude},
		RequestedSinceMs: since,
		RequestNowMs:     now,
		HostNowMs:        now + 200,
		CollectedAtMs:    now + 100,
		CoverageSinceMs:  since,
		Host:             remoteviewv1.Host{OS: "linux", Arch: "arm64"},
		Sessions: []remoteviewv1.Session{
			{
				Agent:              remoteviewv1.AgentClaude,
				SourceSessionID:    "11111111-1111-1111-1111-111111111111",
				SessionStartedAtMs: now - 1000,
				LastActivityAtMs:   now - 100,
				Counts:             remoteviewv1.Counts{Turns: 1},
				Usage:              remoteviewv1.Usage{Models: []remoteviewv1.ModelUsage{}, UnpricedModels: []string{}},
				Digest:             []remoteviewv1.Digest{},
				Todos:              []remoteviewv1.Todo{},
				FileEdits:          []remoteviewv1.FileEdit{},
				Commits:            []string{},
				Subagents:          []remoteviewv1.Subagent{},
			},
		},
	}
}

func sampleProbe() remoteviewv1.Probe {
	return remoteviewv1.Probe{
		SchemaVersion:    remoteviewv1.SchemaVersion,
		CollectorVersion: "dev",
		Capabilities:     []string{remoteviewv1.CapabilityRemoteView, remoteviewv1.CapabilityRemoteLaunch},
		LaunchableAgents: []string{remoteviewv1.AgentClaude, remoteviewv1.AgentCodex},
		HostNowMs:        1000,
		Host:             remoteviewv1.Host{OS: "linux", Arch: "arm64"},
	}
}

func mustFramePayload(t *testing.T, payload []byte) []byte {
	t.Helper()
	framed, err := remoteviewv1.EncodeFrame(payload)
	if err != nil {
		t.Fatal(err)
	}
	return framed
}

func TestCacheStoreLoadRemove(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	id := "r_0123456789abcdef"
	cached := CachedSnapshot{
		View:             sampleView(0, 1000),
		FetchedAtMs:      1000,
		ClockOffsetMs:    50,
		RoundTripMs:      20,
		CollectorVersion: "dev",
		SchemaVersion:    remoteviewv1.SchemaVersion,
		Capabilities:     []string{remoteviewv1.CapabilityRemoteView},
		LaunchableAgents: []string{remoteviewv1.AgentClaude},
		HostOS:           "linux",
		HostArch:         "arm64",
	}
	if err := cache.Store(id, cached); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := cache.Load(id)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if loaded.View.CoverageSinceMs != 0 || loaded.ClockOffsetMs != 50 {
		t.Fatalf("loaded %+v", loaded)
	}
	if err := cache.RemoveSource(id); err != nil {
		t.Fatal(err)
	}
	_, ok, err = cache.Load(id)
	if err != nil || ok {
		t.Fatalf("expected missing after remove")
	}
}

func TestCacheIgnoresCorrupt(t *testing.T) {
	root := t.TempDir()
	cache := NewCache(root)
	id := "r_0123456789abcdef"
	dir := filepath.Join(root, "remotes", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "snapshot.json"), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, ok, err := cache.Load(id)
	if err != nil || ok {
		t.Fatalf("expected corrupt ignored, ok=%v err=%v", ok, err)
	}
}

func TestClockAdjustAndFilter(t *testing.T) {
	now := time.UnixMilli(10_000)
	started := now.Add(-100 * time.Millisecond)
	offset := clockOffsetMs(10_200, started, now)
	if offset != 250 {
		t.Fatalf("offset=%d", offset)
	}
	view := sampleView(0, 10_000)
	view.Sessions[0].LastActivityAtMs = 9_500
	second := view.Sessions[0]
	second.SourceSessionID = "22222222-2222-2222-2222-222222222222"
	second.LastActivityAtMs = 100
	view.Sessions = append(view.Sessions, second)
	adjusted := adjustView(view, 200, 10_000)
	if adjusted.Sessions[0].LastActivityAtMs != 9_300 {
		t.Fatalf("adjusted activity=%d", adjusted.Sessions[0].LastActivityAtMs)
	}
	filtered := filterSessions(adjusted.Sessions, 1_000, 10_000)
	if len(filtered) != 1 || filtered[0].SourceSessionID != view.Sessions[0].SourceSessionID {
		t.Fatalf("filtered=%+v", filtered)
	}
}

func TestClampFuture(t *testing.T) {
	if clampFuture(2_000, 1_000) != 1_000 {
		t.Fatal("expected clamp")
	}
}
