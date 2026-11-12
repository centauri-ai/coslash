package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHubRegistryLegacyMigrationAndRestart(t *testing.T) {
	home := t.TempDir()
	legacy := "https://Hub.CoSlash.io:443"
	files := map[string]string{"hub-url": legacy, "hub-credentials/hub.coslash.io": "device-secret", "sync-v4/queue.json": "pending-queue", "session-backups/prepared/bundle": "pending-spool"}
	for name, content := range files {
		path := filepath.Join(home, filepath.FromSlash(name))
		requireHubTestOK(t, os.MkdirAll(filepath.Dir(path), 0o700))
		requireHubTestOK(t, os.WriteFile(path, []byte(content), 0o600))
	}
	verified := 0
	r, err := openHubRegistry(home, legacy, func(origin string) (bool, error) {
		verified++
		if origin != legacy {
			t.Fatalf("verified %q", origin)
		}
		return true, nil
	})
	requireHubTestOK(t, err)
	want := hubRegistryState{Version: hubRegistryVersion, Primary: "https://hub.coslash.io", Connections: []hubConnection{{Origin: "https://hub.coslash.io", Enabled: true, LegacyOrigin: legacy}}}
	if got := r.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("state = %+v", got)
	}
	if verified != 1 {
		t.Fatalf("verification count = %d", verified)
	}
	reopened, err := openHubRegistry(home, "https://other.example", func(string) (bool, error) { t.Fatal("existing registry must not migrate again"); return false, nil })
	if err != nil || !reflect.DeepEqual(reopened.snapshot(), want) {
		t.Fatalf("reopen: %+v, %v", reopened, err)
	}
	for name, content := range files {
		data, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(name)))
		if err != nil || string(data) != content {
			t.Fatalf("legacy %s = %q, %v", name, data, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(home, "hub-connections", "registry.json"))
	if err != nil || bytes.Contains(data, []byte("device-secret")) || bytes.Contains(data, []byte("pending-queue")) {
		t.Fatalf("registry contents %q, %v", data, err)
	}
}

func TestHubRegistryUnpairedLegacyDoesNotCommitOrBlockLaterMigration(t *testing.T) {
	home := t.TempDir()
	legacy := "https://hub.coslash.io"
	r, err := openHubRegistry(home, legacy, func(string) (bool, error) { return false, nil })
	if err != nil || r.snapshot().Primary != "" {
		t.Fatalf("unpaired registry = %+v, %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(home, "hub-connections", "registry.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending registry file: %v", err)
	}
	r, err = openHubRegistry(home, legacy, func(string) (bool, error) { return true, nil })
	if err != nil || r.snapshot().Primary != legacy {
		t.Fatalf("later migration = %+v, %v", r, err)
	}
}

func TestHubRegistrySecondaryRequiresExplicitEnableAndKeepsPrimaryAuthority(t *testing.T) {
	home := t.TempDir()
	r, err := openHubRegistry(home, "", nil)
	requireHubTestOK(t, err)
	if err := r.addSecondary("https://beta.coslash.io"); err == nil {
		t.Fatal("secondary without primary accepted")
	}
	requireHubTestOK(t, r.adoptLegacyPrimary("https://Hub.CoSlash.io:443"))
	if err := r.addSecondary("https://hub.coslash.io"); err == nil {
		t.Fatal("duplicate accepted")
	}
	if err := r.addSecondary("http://localhost:8787"); err == nil {
		t.Fatal("insecure secondary accepted")
	}
	requireHubTestOK(t, r.addSecondary("https://beta.coslash.io"))
	state := r.snapshot()
	if state.Primary != "https://hub.coslash.io" || state.Connections[1].Enabled {
		t.Fatalf("unexpected consent or authority: %+v", state)
	}
	state.Connections[1].Enabled = true
	if r.snapshot().Connections[1].Enabled {
		t.Fatal("snapshot allowed state mutation")
	}
	reopened, err := openHubRegistry(home, "", nil)
	if err != nil || reopened.snapshot().Connections[1].Enabled {
		t.Fatalf("secondary enabled on restart: %+v %v", reopened, err)
	}
	requireHubTestOK(t, reopened.setEnabled("https://beta.coslash.io", true))
	if err := reopened.setEnabled("https://unknown.example", true); err == nil {
		t.Fatal("unknown Hub enabled")
	}
	if err := reopened.adoptLegacyPrimary("https://other.example"); err == nil {
		t.Fatal("primary replaced")
	}
	reopened, err = openHubRegistry(home, "", nil)
	if err != nil || !reopened.snapshot().Connections[1].Enabled || reopened.snapshot().Primary != "https://hub.coslash.io" {
		t.Fatalf("reopened = %+v %v", reopened, err)
	}
}

func TestHubRegistryWriteFailureDoesNotPublish(t *testing.T) {
	home := t.TempDir()
	fail := errors.New("write failed")
	legacy := "https://hub.coslash.io"
	r, err := openHubRegistry(home, legacy, func(string) (bool, error) { return true, nil })
	requireHubTestOK(t, err)
	before := r.snapshot()
	r.write = func(string, []byte) (bool, error) { return false, fail }
	if err := r.addSecondary("https://beta.coslash.io"); !errors.Is(err, fail) {
		t.Fatalf("add error = %v", err)
	}
	if !reflect.DeepEqual(before, r.snapshot()) {
		t.Fatal("failed write changed memory")
	}
	reopened, err := openHubRegistry(home, "", nil)
	if err != nil || !reflect.DeepEqual(before, reopened.snapshot()) {
		t.Fatalf("failed write changed disk: %+v, %v", reopened, err)
	}
}

func TestHubRegistryReportsCommittedStateWhenDirectorySyncFails(t *testing.T) {
	home := t.TempDir()
	r, err := openHubRegistry(home, "", nil)
	requireHubTestOK(t, err)
	requireHubTestOK(t, r.adoptLegacyPrimary("https://hub.coslash.io"))
	fail := errors.New("directory sync failed")
	r.write = func(dir string, data []byte) (bool, error) {
		committed, err := writeHubRegistryFile(dir, data)
		if err != nil {
			return committed, err
		}
		return true, fail
	}
	if err := r.addSecondary("https://beta.coslash.io"); !errors.Is(err, fail) {
		t.Fatalf("error = %v", err)
	}
	reopened, err := openHubRegistry(home, "", nil)
	requireHubTestOK(t, err)
	if !reflect.DeepEqual(r.snapshot(), reopened.snapshot()) || r.snapshot().Connections[1].Enabled {
		t.Fatal("committed state was not retained safely")
	}
}

func TestHubRegistryFailedEnableDoesNotGrantConsent(t *testing.T) {
	home := t.TempDir()
	r, err := openHubRegistry(home, "", nil)
	requireHubTestOK(t, err)
	requireHubTestOK(t, r.adoptLegacyPrimary("https://hub.coslash.io"))
	requireHubTestOK(t, r.addSecondary("https://beta.coslash.io"))
	r.write = func(string, []byte) (bool, error) { return false, errors.New("write failed") }
	if err := r.setEnabled("https://beta.coslash.io", true); err == nil {
		t.Fatal("failed enable succeeded")
	}
	reopened, err := openHubRegistry(home, "", nil)
	requireHubTestOK(t, err)
	if r.snapshot().Connections[1].Enabled || reopened.snapshot().Connections[1].Enabled {
		t.Fatal("failed enable granted consent")
	}
}

func TestHubRegistryRealWriteFailureLeavesStateEmpty(t *testing.T) {
	home := t.TempDir()
	r, err := openHubRegistry(home, "", nil)
	requireHubTestOK(t, err)
	requireHubTestOK(t, os.Mkdir(filepath.Join(home, "hub-connections", "registry.json"), 0o700))
	if err := r.adoptLegacyPrimary("https://hub.coslash.io"); err == nil {
		t.Fatal("write over directory succeeded")
	}
	if state := r.snapshot(); state.Primary != "" || len(state.Connections) != 0 {
		t.Fatalf("failed write published %+v", state)
	}
}

func TestHubRegistryMigrationRequiresVerification(t *testing.T) {
	home := t.TempDir()
	legacy := "https://hub.coslash.io"
	fail := errors.New("credential store unavailable")
	if _, err := openHubRegistry(home, legacy, func(string) (bool, error) { return false, fail }); !errors.Is(err, fail) {
		t.Fatalf("verification error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "hub-connections", "registry.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed verification persisted registry: %v", err)
	}
}

func TestCanonicalRegistryOrigin(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"https://HUB.Coslash.io.:443", "https://hub.coslash.io"},
		{"https://hub.coslash.io:8443", "https://hub.coslash.io:8443"},
		{"https://[2001:DB8::1]:443", "https://[2001:db8::1]"},
		{"http://LOCALHOST:80", "http://localhost"},
	} {
		if got, err := canonicalRegistryOrigin(test.input); err != nil || got != test.want {
			t.Errorf("%q = %q, %v; want %q", test.input, got, err, test.want)
		}
	}
	for _, input := range []string{"https://hub.coslash.io/path", "https://user@hub.coslash.io", "http://example.com"} {
		if _, err := canonicalRegistryOrigin(input); err == nil {
			t.Errorf("accepted invalid origin %q", input)
		}
	}
}

func TestHubRegistrySerializesConcurrentChanges(t *testing.T) {
	home := t.TempDir()
	r, err := openHubRegistry(home, "", nil)
	requireHubTestOK(t, err)
	requireHubTestOK(t, r.adoptLegacyPrimary("https://hub.coslash.io"))
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	for _, origin := range []string{"https://beta.coslash.io", "https://other.example"} {
		wait.Add(1)
		go func() { defer wait.Done(); errs <- r.addSecondary(origin) }()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		requireHubTestOK(t, err)
	}
	reopened, err := openHubRegistry(home, "", nil)
	if err != nil || len(reopened.snapshot().Connections) != 3 {
		t.Fatalf("reopened = %+v, %v", reopened, err)
	}
}

func TestHubRegistrySnapshotDoesNotWaitForWrite(t *testing.T) {
	home := t.TempDir()
	r, err := openHubRegistry(home, "", nil)
	requireHubTestOK(t, err)
	requireHubTestOK(t, r.adoptLegacyPrimary("https://hub.coslash.io"))
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	r.write = func(dir string, data []byte) (bool, error) {
		close(started)
		<-release
		return writeHubRegistryFile(dir, data)
	}
	go func() { finished <- r.addSecondary("https://beta.coslash.io") }()
	waitSignal(t, started)
	snapshotDone := make(chan hubRegistryState, 1)
	go func() { snapshotDone <- r.snapshot() }()
	select {
	case state := <-snapshotDone:
		if len(state.Connections) != 1 {
			t.Fatalf("uncommitted state visible: %+v", state)
		}
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("snapshot waited for disk write")
	}
	close(release)
	requireHubTestOK(t, <-finished)
}

func TestHubRegistryRejectsCorruptOrUnknownData(t *testing.T) {
	inputs := []string{
		`{}`, `null`, `{"version":2,"primary":"https://hub.coslash.io","connections":[]}`,
		`{"version":1,"primary":"https://hub.coslash.io","connections":[{"origin":"https://hub.coslash.io","enabled":true,"extra":1}]}`,
		`{"version":1,"primary":"https://hub.coslash.io","connections":[{"origin":"https://hub.coslash.io","enabled":true},{"origin":"https://hub.coslash.io","enabled":false}]}`,
		`{"version":1,"primary":"https://hub.coslash.io","connections":[{"origin":"https://HUB.coslash.io","enabled":true}]}`,
		`{"version":1,"primary":"https://hub.coslash.io","connections":[{"origin":"https://hub.coslash.io","enabled":true},{"origin":"http://localhost:8787","enabled":false}]}`,
		strings.Repeat(" ", (1<<20)+1),
	}
	for i, input := range inputs {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, "hub-connections")
			requireHubTestOK(t, os.MkdirAll(dir, 0o700))
			requireHubTestOK(t, os.WriteFile(filepath.Join(dir, "registry.json"), []byte(input), 0o600))
			if _, err := openHubRegistry(home, "https://hub.coslash.io", func(string) (bool, error) { t.Fatal("corruption triggered migration"); return true, nil }); err == nil {
				t.Fatal("corrupt registry accepted")
			}
		})
	}
}
