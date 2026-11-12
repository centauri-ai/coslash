package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

func primaryRuntimeFixture(t *testing.T, credential string) (*hubclient.Client, *syncTestCredentials) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_V4_SYNC", "1")
	t.Setenv("COSLASH_HUB_URL", "")
	store := &syncTestCredentials{value: credential}
	previous := hubCredentialStore
	hubCredentialStore = func(string) hubclient.CredentialStore { return store }
	t.Cleanup(func() { hubCredentialStore = previous })
	const origin = "https://hub.coslash.io"
	if err := writeStoredHubURL(origin); err != nil {
		t.Fatal(err)
	}
	client, err := hubClientFromEnvironment("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	return client, store
}

func primaryRuntimeController(t *testing.T, started, stopped chan struct{}) *syncController {
	t.Helper()
	c, err := newSyncController(func(ctx context.Context, _ *syncv4.Queue, _ *hubclient.Client, _, _ chan struct{}) {
		started <- struct{}{}
		<-ctx.Done()
		stopped <- struct{}{}
	}, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Stop)
	return c
}

func TestPrimaryHubRuntimeMigratesAndRestartsWithPendingWork(t *testing.T) {
	client, _ := primaryRuntimeFixture(t, "legacy-credential")
	started, stopped := make(chan struct{}, 2), make(chan struct{}, 2)
	first := primaryRuntimeController(t, started, stopped)
	binding, err := client.V4Binding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Queue().Rebind(binding); err != nil {
		t.Fatal(err)
	}
	if err := first.Queue().Merge([]syncv4.Entry{{Key: "pending", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1, BundleID: "pending-bundle"}}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := first.Queue().SaveDiscoveryCursor(binding, []byte(`{"legacy":true}`)); err != nil {
		t.Fatal(err)
	}
	spool := filepath.Join(os.Getenv("COSLASH_HOME"), "session-backups", "prepared", "pending-bundle")
	if err := os.MkdirAll(spool, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spool, "fixture"), []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}
	installID := first.Queue().InstallID()
	runtime, selected, err := newPrimaryHubRuntime(client, first, "1.2.3", func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if selected != client {
		t.Fatal("primary client changed")
	}
	if err := runtime.Ensure(selected); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, started)
	slot := runtime.workers.slot(runtime.registry.snapshot().Primary)
	if slot.controller != first || slot.controller.Queue() != first.Queue() {
		t.Fatal("supervisor opened a second primary queue")
	}
	runtime.workers.StopAll()
	waitSignal(t, stopped)
	second := primaryRuntimeController(t, started, stopped)
	restarted, selected, err := newPrimaryHubRuntime(client, second, "1.2.3", func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Ensure(selected); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, started)
	if second.Queue().InstallID() != installID || len(second.Queue().Entries()) != 1 || second.Queue().Entries()[0].BundleID != "pending-bundle" {
		t.Fatal("primary pending queue changed on restart")
	}
	if got, cursor := second.Queue().DiscoveryCursor(); got != binding || string(cursor) != `{"legacy":true}` {
		t.Fatalf("binding/cursor = %q %s", got, cursor)
	}
	if data, err := os.ReadFile(filepath.Join(spool, "fixture")); err != nil || string(data) != "pending" {
		t.Fatalf("primary spool = %q, %v", data, err)
	}
	restarted.workers.StopAll()
	waitSignal(t, stopped)
}

func TestPrimaryHubRuntimeRejectsMismatchedLegacyBinding(t *testing.T) {
	client, store := primaryRuntimeFixture(t, "old")
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	c := primaryRuntimeController(t, started, stopped)
	binding, err := client.V4Binding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Queue().Rebind(binding); err != nil {
		t.Fatal(err)
	}
	if err := c.Queue().Merge([]syncv4.Entry{{Key: "pending", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1}}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), "different"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := newPrimaryHubRuntime(client, c, "1.2.3", func() bool { return false }); !errors.Is(err, errPrimaryHubMismatch) {
		t.Fatalf("mismatch error = %v", err)
	}
	got, _ := c.Queue().DiscoveryCursor()
	if got != binding || len(c.Queue().Entries()) != 1 {
		t.Fatal("mismatch changed pending queue")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("COSLASH_HOME"), "hub-connections", "registry.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mismatch published registry: %v", err)
	}
}

func TestPrimaryHubRuntimeFreshAndUnpairedThenPaired(t *testing.T) {
	client, store := primaryRuntimeFixture(t, "")
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	c := primaryRuntimeController(t, started, stopped)
	runtime, selected, err := newPrimaryHubRuntime(client, c, "1.2.3", func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if runtime.registry.snapshot().Primary != "" {
		t.Fatal("unpaired selector migrated")
	}
	if err := runtime.Ensure(selected); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), "approved"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Ensure(selected); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, started)
	if runtime.registry.snapshot().Primary != "https://hub.coslash.io" {
		t.Fatal("approved primary not registered")
	}
	runtime.workers.StopAll()
	waitSignal(t, stopped)
}

func TestPrimaryHubRuntimeEnvironmentAndKillSwitch(t *testing.T) {
	client, _ := primaryRuntimeFixture(t, "credential")
	if err := os.Remove(filepath.Join(os.Getenv("COSLASH_HOME"), storedHubURLFilename)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COSLASH_HUB_URL", "https://hub.coslash.io")
	client, err := hubClientFromEnvironment("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	c := primaryRuntimeController(t, started, stopped)
	runtime, selected, err := newPrimaryHubRuntime(client, c, "1.2.3", func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Ensure(selected); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, started)
	if runtime.registry.snapshot().Primary != "https://hub.coslash.io" {
		t.Fatal("environment primary not registered")
	}
	runtime.workers.StopAll()
	waitSignal(t, stopped)
	if _, err := os.Stat(filepath.Join(os.Getenv("COSLASH_HOME"), storedHubURLFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("environment created selector: %v", err)
	}
	t.Setenv("COSLASH_V4_SYNC", "0")
	killed := primaryRuntimeController(t, started, stopped)
	if killed.Queue() != nil {
		t.Fatal("kill switch opened a queue")
	}
	disabled, selected, err := newPrimaryHubRuntime(client, killed, "1.2.3", func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if err := disabled.Ensure(selected); err != nil {
		t.Fatal(err)
	}
	if killed.Status().State != "auto_sync_off" {
		t.Fatalf("kill switch status = %s", killed.Status().State)
	}
	disabled.workers.StopAll()
}

func TestPrimaryHubRuntimeEmptyInstallDoesNotCommit(t *testing.T) {
	_, _ = primaryRuntimeFixture(t, "")
	if err := os.Remove(filepath.Join(os.Getenv("COSLASH_HOME"), storedHubURLFilename)); err != nil {
		t.Fatal(err)
	}
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	c := primaryRuntimeController(t, started, stopped)
	runtime, selected, err := newPrimaryHubRuntime(nil, c, "1.2.3", func() bool { return false })
	if err != nil || selected != nil {
		t.Fatalf("empty runtime = %v, %v", selected, err)
	}
	if err := runtime.Ensure(nil); err != nil {
		t.Fatal(err)
	}
	if c.Status().State != "not_connected" || runtime.registry.snapshot().Primary != "" {
		t.Fatal("empty install became paired")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("COSLASH_HOME"), "hub-connections", "registry.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty registry persisted: %v", err)
	}
	runtime.workers.StopAll()
}

func TestPrimaryHubRuntimeDoesNotStartSecondary(t *testing.T) {
	client, _ := primaryRuntimeFixture(t, "credential")
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	c := primaryRuntimeController(t, started, stopped)
	runtime, selected, err := newPrimaryHubRuntime(client, c, "1.2.3", func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Ensure(selected); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, started)
	if err := runtime.registry.addSecondary("https://beta.coslash.io"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.registry.setEnabled("https://beta.coslash.io", true); err != nil {
		t.Fatal(err)
	}
	if err := runtime.workers.StartEnabled("https://beta.coslash.io"); err == nil {
		t.Fatal("primary-only runtime started secondary")
	}
	if !c.RetryNow() {
		t.Fatal("primary stopped after secondary request")
	}
	runtime.workers.StopAll()
	waitSignal(t, stopped)
}

func TestPrimaryHubRuntimeStopsChangedCredentialWithoutRebinding(t *testing.T) {
	client, store := primaryRuntimeFixture(t, "original")
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	c := primaryRuntimeController(t, started, stopped)
	runtime, selected, err := newPrimaryHubRuntime(client, c, "1.2.3", func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Ensure(selected); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, started)
	old, _ := c.Queue().DiscoveryCursor()
	if err := store.Save(context.Background(), "replacement"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Ensure(selected); !errors.Is(err, errPrimaryHubMismatch) {
		t.Fatalf("replacement error = %v", err)
	}
	waitSignal(t, stopped)
	if binding, _ := c.Queue().DiscoveryCursor(); binding != old {
		t.Fatal("replacement rebound the primary queue")
	}
	if got, err := store.Load(context.Background()); err != nil || got != "replacement" {
		t.Fatalf("replacement credential = %q, %v", got, err)
	}
	runtime.workers.StopAll()
}

func TestPrimaryHubRuntimeRefusesForeignLegacySwitch(t *testing.T) {
	client, _ := primaryRuntimeFixture(t, "credential")
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	c := primaryRuntimeController(t, started, stopped)
	runtime, selected, err := newPrimaryHubRuntime(client, c, "1.2.3", func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	manager := newOnboardingManager("1.2.3")
	t.Cleanup(manager.Close)
	manager.setHubClient(selected)
	manager.setSyncHooks(runtime)
	if err := runtime.Ensure(selected); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, started)
	if err := manager.StartPairing("https://beta.coslash.io", switchAttemptID, strings.Repeat("A", 43)); !errors.Is(err, errHubSwitchPending) {
		t.Fatalf("foreign request = %v", err)
	}
	if err := manager.ResolveSwitch("https://beta.coslash.io", true); !errors.Is(err, errPrimaryHubMismatch) {
		t.Fatalf("foreign switch = %v", err)
	}
	if got, err := readStoredHubURL(); err != nil || got != "https://hub.coslash.io" {
		t.Fatalf("saved Hub = %q, %v", got, err)
	}
	if !c.RetryNow() {
		t.Fatal("foreign request stopped primary")
	}
	runtime.workers.StopAll()
	waitSignal(t, stopped)
}

func TestPrimaryHubRuntimeUnpairedBoundQueueCannotChangeSelector(t *testing.T) {
	client, _ := primaryRuntimeFixture(t, "")
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	c := primaryRuntimeController(t, started, stopped)
	if err := c.Queue().Rebind(strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	runtime, _, err := newPrimaryHubRuntime(client, c, "1.2.3", func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	manager := newOnboardingManager("1.2.3")
	t.Cleanup(manager.Close)
	manager.setSyncHooks(runtime)
	if err := manager.startPairing("https://beta.coslash.io", switchAttemptID, strings.Repeat("A", 43)); !errors.Is(err, errPrimaryHubMismatch) {
		t.Fatalf("pairing error = %v", err)
	}
	if got, err := readStoredHubURL(); err != nil || got != "https://hub.coslash.io" {
		t.Fatalf("saved Hub = %q, %v", got, err)
	}
	if got, _ := c.Queue().DiscoveryCursor(); got != strings.Repeat("a", 64) {
		t.Fatal("unpaired queue rebound")
	}
	runtime.workers.StopAll()
}
