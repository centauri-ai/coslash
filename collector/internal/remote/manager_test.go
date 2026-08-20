package remote

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func (c *testClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func framedProbe(t *testing.T) []byte {
	t.Helper()
	payload, err := remoteviewv1.MarshalProbe(sampleProbe())
	if err != nil {
		t.Fatal(err)
	}
	return mustFramePayload(t, payload)
}

func framedView(t *testing.T, since, now int64) []byte {
	t.Helper()
	payload, err := remoteviewv1.Marshal(sampleView(since, now))
	if err != nil {
		t.Fatal(err)
	}
	return mustFramePayload(t, payload)
}

func framedViewWithBanner(t *testing.T, since, now int64) []byte {
	return append([]byte("welcome\n"), framedView(t, since, now)...)
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

func TestManagerHappyPathRefreshAndCache(t *testing.T) {
	clock := &testClock{t: time.UnixMilli(1_000_000)}
	var calls atomic.Int32
	fake := &FakeRunner{Hook: func(call FakeCall) (RunResult, error) {
		n := calls.Add(1)
		now := clock.Now()
		result := RunResult{ExitCode: 0, StartedAt: now, FinishedAt: now.Add(20 * time.Millisecond)}
		if n == 1 {
			result.Stdout = framedProbe(t)
			return result, nil
		}
		result.Stdout = framedViewWithBanner(t, 0, now.UnixMilli())
		return result, nil
	}}
	mgr := NewManager(Options{Runner: fake, Cache: NewCache(t.TempDir()), Now: clock.Now})
	cfg := &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "gpu-server", Enabled: true}
	if err := mgr.ApplySettings(cfg); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		h := mgr.DiagnosticsHealth()
		return h.State == StateOK && !h.Refreshing
	})
	list := mgr.ListView(0)
	if list.Health.State != StateOK || len(list.Sessions) != 1 {
		t.Fatalf("list=%+v health=%+v", list.Sessions, list.Health)
	}
	if list.Sessions[0].Key.SourceID != cfg.ID {
		t.Fatal("source id missing")
	}
	if !list.Sessions[0].EligibleForAggregates {
		t.Fatal("expected eligible")
	}
}

func TestManagerOneFlightAndManualBypass(t *testing.T) {
	clock := &testClock{t: time.UnixMilli(2_000_000)}
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	var calls atomic.Int32
	fake := &FakeRunner{Hook: func(call FakeCall) (RunResult, error) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-release:
		case <-time.After(2 * time.Second):
			t.Error("release timeout")
		}
		now := clock.Now()
		if call.RemoteCommand == ProbeCommand() {
			return RunResult{Stdout: framedProbe(t), StartedAt: now, FinishedAt: now}, nil
		}
		return RunResult{Stdout: framedView(t, 0, now.UnixMilli()), StartedAt: now, FinishedAt: now}, nil
	}}
	mgr := NewManager(Options{Runner: fake, Cache: NewCache(t.TempDir()), Now: clock.Now})
	if err := mgr.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "gpu", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	<-started // probe or snapshot start
	mgr.Retry()
	mgr.Retry()
	close(release)
	waitUntil(t, 2*time.Second, func() bool {
		return !mgr.DiagnosticsHealth().Refreshing && mgr.DiagnosticsHealth().State == StateOK
	})
	if calls.Load() < 2 {
		t.Fatalf("expected probe+snapshot, got %d", calls.Load())
	}
}

func TestManagerBackoffAndStaleFallback(t *testing.T) {
	clock := &testClock{t: time.UnixMilli(3_000_000)}
	var calls atomic.Int32
	fake := &FakeRunner{Hook: func(call FakeCall) (RunResult, error) {
		n := calls.Add(1)
		now := clock.Now()
		if n == 1 {
			return RunResult{Stdout: framedProbe(t), StartedAt: now, FinishedAt: now}, nil
		}
		if n == 2 {
			return RunResult{Stdout: framedView(t, 0, now.UnixMilli()), StartedAt: now, FinishedAt: now}, nil
		}
		return RunResult{ExitCode: 255, Stderr: []byte("ssh: connect failed"), StartedAt: now, FinishedAt: now}, nil
	}}
	mgr := NewManager(Options{Runner: fake, Cache: NewCache(t.TempDir()), Now: clock.Now})
	if err := mgr.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "gpu", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 2*time.Second, func() bool { return mgr.DiagnosticsHealth().State == StateOK })

	clock.Advance(FreshnessInterval + time.Second)
	before := calls.Load()
	_ = mgr.ListView(0)
	waitUntil(t, 2*time.Second, func() bool {
		h := mgr.DiagnosticsHealth()
		return h.State == StateStale && !h.Refreshing
	})
	if mgr.DiagnosticsHealth().Reason == nil || *mgr.DiagnosticsHealth().Reason != ReasonConnectionFailed {
		t.Fatalf("reason=%v", mgr.DiagnosticsHealth().Reason)
	}
	mid := calls.Load()
	if mid <= before {
		t.Fatal("expected failed refresh attempt")
	}
	_ = mgr.ListView(0)
	if calls.Load() != mid {
		t.Fatal("automatic retry should respect backoff")
	}
	mgr.Retry()
	waitUntil(t, 2*time.Second, func() bool {
		return calls.Load() > mid && !mgr.DiagnosticsHealth().Refreshing
	})
	list := mgr.ListView(0)
	if len(list.Sessions) != 1 || list.Sessions[0].EligibleForAggregates {
		t.Fatalf("stale sessions should remain visible but ineligible: %+v", list.Sessions)
	}
}

func TestManagerSetupRequiredAndUpgradeRequired(t *testing.T) {
	t.Run("exit127", func(t *testing.T) {
		clock := &testClock{t: time.UnixMilli(4_000_000)}
		fake := &FakeRunner{Hook: func(call FakeCall) (RunResult, error) {
			now := clock.Now()
			return RunResult{ExitCode: 127, Stderr: []byte("not found"), StartedAt: now, FinishedAt: now}, nil
		}}
		mgr := NewManager(Options{Runner: fake, Cache: NewCache(t.TempDir()), Now: clock.Now})
		_ = mgr.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "gpu", Enabled: true})
		waitUntil(t, 2*time.Second, func() bool { return mgr.DiagnosticsHealth().State == StateSetupRequired })
	})
	t.Run("bad framed probe", func(t *testing.T) {
		clock := &testClock{t: time.UnixMilli(5_000_000)}
		fake := &FakeRunner{Hook: func(call FakeCall) (RunResult, error) {
			now := clock.Now()
			return RunResult{ExitCode: 0, Stdout: []byte("hello\n"), StartedAt: now, FinishedAt: now}, nil
		}}
		mgr := NewManager(Options{Runner: fake, Cache: NewCache(t.TempDir()), Now: clock.Now})
		_ = mgr.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "gpu", Enabled: true})
		waitUntil(t, 2*time.Second, func() bool { return mgr.DiagnosticsHealth().State == StateUpgradeRequired })
	})
}

func TestManagerReEnableReprobes(t *testing.T) {
	clock := &testClock{t: time.UnixMilli(12_000_000)}
	var probes atomic.Int32
	var phase atomic.Int32 // 0=ok, 1=bad probe after re-enable
	fake := &FakeRunner{Hook: func(call FakeCall) (RunResult, error) {
		now := clock.Now()
		if call.RemoteCommand == ProbeCommand() {
			probes.Add(1)
			if phase.Load() == 1 {
				return RunResult{ExitCode: 0, Stdout: []byte("no frame\n"), StartedAt: now, FinishedAt: now}, nil
			}
			return RunResult{Stdout: framedProbe(t), StartedAt: now, FinishedAt: now}, nil
		}
		return RunResult{Stdout: framedView(t, 0, now.UnixMilli()), StartedAt: now, FinishedAt: now}, nil
	}}
	mgr := NewManager(Options{Runner: fake, Cache: NewCache(t.TempDir()), Now: clock.Now})
	id := "r_0123456789abcdef"
	_ = mgr.ApplySettings(&settings.RemoteSettings{ID: id, SSHAlias: "gpu", Enabled: true})
	waitUntil(t, 2*time.Second, func() bool { return mgr.DiagnosticsHealth().State == StateOK })
	if probes.Load() != 1 {
		t.Fatalf("probes=%d", probes.Load())
	}

	_ = mgr.ApplySettings(&settings.RemoteSettings{ID: id, SSHAlias: "gpu", Enabled: false})
	phase.Store(1)
	_ = mgr.ApplySettings(&settings.RemoteSettings{ID: id, SSHAlias: "gpu", Enabled: true})
	waitUntil(t, 2*time.Second, func() bool {
		return mgr.DiagnosticsHealth().State == StateUpgradeRequired && !mgr.DiagnosticsHealth().Refreshing
	})
	if probes.Load() != 2 {
		t.Fatalf("expected second probe after re-enable, probes=%d", probes.Load())
	}
	if reason := mgr.DiagnosticsHealth().Reason; reason == nil || *reason != ReasonCollectorOutdated {
		t.Fatalf("reason=%v", mgr.DiagnosticsHealth().Reason)
	}
}

func TestManagerDisableKeepsCacheRemoveDeletes(t *testing.T) {
	clock := &testClock{t: time.UnixMilli(6_000_000)}
	var calls atomic.Int32
	fake := &FakeRunner{Hook: func(call FakeCall) (RunResult, error) {
		n := calls.Add(1)
		now := clock.Now()
		if n == 1 {
			return RunResult{Stdout: framedProbe(t), StartedAt: now, FinishedAt: now}, nil
		}
		return RunResult{Stdout: framedView(t, 0, now.UnixMilli()), StartedAt: now, FinishedAt: now}, nil
	}}
	root := t.TempDir()
	mgr := NewManager(Options{Runner: fake, Cache: NewCache(root), Now: clock.Now})
	id := "r_0123456789abcdef"
	_ = mgr.ApplySettings(&settings.RemoteSettings{ID: id, SSHAlias: "gpu", Enabled: true})
	waitUntil(t, 2*time.Second, func() bool { return mgr.DiagnosticsHealth().State == StateOK })

	_ = mgr.ApplySettings(&settings.RemoteSettings{ID: id, SSHAlias: "gpu", Enabled: false})
	list := mgr.ListView(0)
	if list.Health.State != StateDisabled || len(list.Sessions) != 0 {
		t.Fatalf("disabled list=%+v", list)
	}
	if _, ok, _ := NewCache(root).Load(id); !ok {
		t.Fatal("disable should retain cache")
	}

	_ = mgr.ApplySettings(nil)
	if _, ok, _ := NewCache(root).Load(id); ok {
		t.Fatal("remove should delete cache")
	}
}

func TestManagerAliasReplacementNewIdentity(t *testing.T) {
	clock := &testClock{t: time.UnixMilli(7_000_000)}
	fake := &FakeRunner{Hook: func(call FakeCall) (RunResult, error) {
		now := clock.Now()
		if call.RemoteCommand == ProbeCommand() {
			return RunResult{Stdout: framedProbe(t), StartedAt: now, FinishedAt: now}, nil
		}
		return RunResult{Stdout: framedView(t, 0, now.UnixMilli()), StartedAt: now, FinishedAt: now}, nil
	}}
	root := t.TempDir()
	mgr := NewManager(Options{Runner: fake, Cache: NewCache(root), Now: clock.Now})
	oldID := "r_0123456789abcdef"
	newID := "r_fedcba9876543210"
	_ = mgr.ApplySettings(&settings.RemoteSettings{ID: oldID, SSHAlias: "old", Enabled: true})
	waitUntil(t, 2*time.Second, func() bool { return mgr.DiagnosticsHealth().State == StateOK })
	_ = mgr.ApplySettings(&settings.RemoteSettings{ID: newID, SSHAlias: "new", Enabled: true})
	waitUntil(t, 2*time.Second, func() bool {
		h := mgr.DiagnosticsHealth()
		return h.SourceID == newID && h.State == StateOK
	})
	if _, ok, _ := NewCache(root).Load(oldID); ok {
		t.Fatal("old cache should be removed")
	}
	if _, ok, _ := NewCache(root).Load(newID); !ok {
		t.Fatal("new cache should exist")
	}
}

func TestManagerBroaderAndNarrowerCoverage(t *testing.T) {
	clock := &testClock{t: time.UnixMilli(8_000_000)}
	var mu sync.Mutex
	coverage := int64(5_000)
	fake := &FakeRunner{Hook: func(call FakeCall) (RunResult, error) {
		now := clock.Now()
		if call.RemoteCommand == ProbeCommand() {
			return RunResult{Stdout: framedProbe(t), StartedAt: now, FinishedAt: now}, nil
		}
		mu.Lock()
		since := coverage
		mu.Unlock()
		return RunResult{Stdout: framedView(t, since, now.UnixMilli()), StartedAt: now, FinishedAt: now}, nil
	}}
	mgr := NewManager(Options{Runner: fake, Cache: NewCache(t.TempDir()), Now: clock.Now})
	_ = mgr.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "gpu", Enabled: true})
	waitUntil(t, 2*time.Second, func() bool { return mgr.DiagnosticsHealth().State == StateOK })

	list := mgr.ListView(7_000)
	if !list.Health.Complete || len(list.Sessions) != 1 {
		t.Fatalf("narrower should filter from cache: complete=%v sessions=%d", list.Health.Complete, len(list.Sessions))
	}

	mu.Lock()
	coverage = 0
	mu.Unlock()
	clock.Advance(time.Millisecond)
	list = mgr.ListView(0)
	if list.Health.State != StateConnecting || list.Health.Complete {
		t.Fatalf("broader should be incomplete connecting: %+v", list.Health)
	}
	waitUntil(t, 2*time.Second, func() bool {
		h := mgr.ListView(0).Health
		return h.State == StateOK && h.Complete
	})
}

func TestManagerTruncatedLimited(t *testing.T) {
	clock := &testClock{t: time.UnixMilli(9_000_000)}
	fake := &FakeRunner{Hook: func(call FakeCall) (RunResult, error) {
		now := clock.Now()
		if call.RemoteCommand == ProbeCommand() {
			return RunResult{Stdout: framedProbe(t), StartedAt: now, FinishedAt: now}, nil
		}
		view := sampleView(0, now.UnixMilli())
		view.Truncated = true
		reason := remoteviewv1.TruncationReasonSession
		view.TruncationReason = &reason
		payload, err := remoteviewv1.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		return RunResult{Stdout: mustFramePayload(t, payload), StartedAt: now, FinishedAt: now}, nil
	}}
	mgr := NewManager(Options{Runner: fake, Cache: NewCache(t.TempDir()), Now: clock.Now})
	_ = mgr.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "gpu", Enabled: true})
	waitUntil(t, 2*time.Second, func() bool { return mgr.DiagnosticsHealth().State == StateLimited })
	list := mgr.ListView(0)
	if list.Health.Complete || list.Sessions[0].EligibleForAggregates {
		t.Fatalf("limited should be incomplete/ineligible: %+v", list)
	}
}

func TestManagerSameRemoteAndLocalIDsDistinguishable(t *testing.T) {
	clock := &testClock{t: time.UnixMilli(10_000_000)}
	fake := &FakeRunner{Hook: func(call FakeCall) (RunResult, error) {
		now := clock.Now()
		if call.RemoteCommand == ProbeCommand() {
			return RunResult{Stdout: framedProbe(t), StartedAt: now, FinishedAt: now}, nil
		}
		return RunResult{Stdout: framedView(t, 0, now.UnixMilli()), StartedAt: now, FinishedAt: now}, nil
	}}
	mgr := NewManager(Options{Runner: fake, Cache: NewCache(t.TempDir()), Now: clock.Now})
	_ = mgr.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "gpu", Enabled: true})
	waitUntil(t, 2*time.Second, func() bool { return mgr.DiagnosticsHealth().State == StateOK })
	list := mgr.ListView(0)
	key := list.Sessions[0].Key
	local := SessionKey{SourceID: "local", Agent: key.Agent, SourceSessionID: key.SourceSessionID}
	if key == local {
		t.Fatal("remote key collided with local")
	}
}

func TestTestAlias(t *testing.T) {
	fake := &FakeRunner{Hook: func(call FakeCall) (RunResult, error) {
		return RunResult{Stdout: framedProbe(t), StartedAt: time.Now(), FinishedAt: time.Now()}, nil
	}}
	mgr := NewManager(Options{Runner: fake, Cache: NewCache(t.TempDir())})
	health, err := mgr.TestAlias(context.Background(), "gpu-server")
	if err != nil {
		t.Fatal(err)
	}
	if health.State != StateOK || health.CollectorVersion != "dev" {
		t.Fatalf("%+v", health)
	}
}
