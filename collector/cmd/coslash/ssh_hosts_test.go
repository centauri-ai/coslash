package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

func TestSSHHostsCheckInStopsRemovedHost(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	local := settings.Open()
	config := settings.Defaults()
	config.Remote = &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", HostID: "host-one", Enabled: true}
	if err := local.Save(config); err != nil {
		t.Fatal(err)
	}
	opened, cancelled := make(chan struct{}), make(chan struct{})
	relay := remote.NewManager(remote.Options{Cache: remote.NewCache(t.TempDir()), Open: func(ctx context.Context, _ string, _ remote.OpenOptions) (*remote.Session, error) {
		close(opened)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}})
	if err := relay.ApplySettings(config.Remote); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(relay.Shutdown)
	relay.ListView(0)
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("remote collection did not start")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"configVersion":1,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"commands":[],"sshHosts":[],"minVersion":"0.0.0","nextCheckInSeconds":300}`))
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := hubclient.Client{BaseURL: base, Credentials: hubTransportTestCredentials{}, CollectorVersion: "0.0.5"}
	queue, err := syncv4.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runner := syncv4.Runner{
		Queue: queue, Hub: &client,
		Backup:            sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir()}),
		Discover:          func(context.Context) ([]*session.Session, error) { return nil, nil },
		Command:           v4CommandRunner(queue, local, relay),
		ReconcileSSHHosts: reconcileSSHHosts(local, relay),
	}
	for range 2 {
		if err := runner.SyncOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("remove did not cancel remote collection")
	}
	if got := settings.Open().State().Config.Remote; got != nil {
		t.Fatalf("persisted remote remains: %+v", got)
	}
	if health := relay.DiagnosticsHealth(); health.State != remote.StateDisabled || health.SourceID != "" {
		t.Fatalf("remote remains active: %+v", health)
	}
}

func TestSSHHostsCheckInReconcilesAndRetries(t *testing.T) {
	for _, tc := range []struct {
		name, field, hostID, failure string
		remove, notRunning           bool
	}{
		{name: "manager not running", field: `,"sshHosts":[]`, hostID: "host-one", remove: true, notRunning: true},
		{name: "removed", field: `,"sshHosts":[]`, hostID: "host-one", remove: true},
		{name: "same ID", field: `,"sshHosts":[{"id":"host-one","alias":"different-box"}]`, hostID: "host-one"},
		{name: "replacement pending", field: `,"sshHosts":[{"id":"host-two","alias":"agent-box"}]`, hostID: "host-one"},
		{name: "older server", hostID: "host-one"},
		{name: "Local UI host", field: `,"sshHosts":[]`},
		{name: "cache failure", field: `,"sshHosts":[]`, hostID: "host-one", failure: "cache", remove: true},
		{name: "permission denied", field: `,"sshHosts":[]`, hostID: "host-one", failure: "permissions", remove: true},
		{name: "settings failure", field: `,"sshHosts":[]`, hostID: "host-one", failure: "settings", remove: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.failure == "permissions" && (runtime.GOOS == "windows" || os.Geteuid() == 0) {
				t.Skip("Unix directory permission failure requires an unprivileged account")
			}
			home := t.TempDir()
			t.Setenv("COSLASH_HOME", home)
			local := settings.Open()
			config := settings.Defaults()
			config.Remote = &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", HostID: tc.hostID, Enabled: true}
			if err := local.Save(config); err != nil {
				t.Fatal(err)
			}
			cacheRoot := t.TempDir()
			cache := remote.NewCache(cacheRoot)
			if err := cache.StoreHelperVersion(config.Remote.ID, "v1", "agent-box"); err != nil {
				t.Fatal(err)
			}
			relay := remote.NewManager(remote.Options{Cache: cache})
			if !tc.notRunning {
				if err := relay.ApplySettings(config.Remote); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(relay.Shutdown)
			before := relay.DiagnosticsHealth()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"configVersion":1,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"commands":[],"minVersion":"0.0.0","nextCheckInSeconds":300` + tc.field + `}`))
			}))
			defer server.Close()
			base, _ := url.Parse(server.URL)
			client := hubclient.Client{BaseURL: base, Credentials: hubTransportTestCredentials{}, CollectorVersion: "0.0.5"}
			queue, err := syncv4.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			runner := syncv4.Runner{ReconcileSSHHosts: reconcileSSHHosts(local, relay), Queue: queue, Hub: &client, Backup: sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir()}), Discover: func(context.Context) ([]*session.Session, error) { return nil, nil }}
			if tc.failure == "permissions" {
				parent := filepath.Join(cacheRoot, "remotes")
				if err := os.Chmod(parent, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
			}
			if tc.failure == "cache" {
				cache.Root = filepath.Join(t.TempDir(), strings.Repeat("x", 256))
			}
			if tc.failure == "settings" {
				t.Setenv("COSLASH_HOME", filepath.Join(t.TempDir(), strings.Repeat("x", 256)))
			}
			err = runner.SyncOnce(t.Context())
			if tc.failure != "" {
				if err == nil {
					t.Fatal("reconciliation failure not reported")
				}
				t.Setenv("COSLASH_HOME", home)
				cache.Root = cacheRoot
				if tc.failure == "permissions" {
					if err := os.Chmod(filepath.Join(cacheRoot, "remotes"), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				if got := settings.Open().State().Config; !reflect.DeepEqual(got, config) {
					t.Fatalf("failure changed settings: %+v", got.Remote)
				}
				if got := relay.DiagnosticsHealth(); !reflect.DeepEqual(got, before) {
					t.Fatalf("failure changed live remote: %+v", got)
				}
				_, owned, ownershipErr := cache.LoadHelperOwnership(config.Remote.ID)
				if ownershipErr != nil || !owned {
					t.Fatalf("failure lost helper ownership: owned=%v err=%v", owned, ownershipErr)
				}
				err = runner.SyncOnce(t.Context())
			}
			if err != nil {
				t.Fatal(err)
			}
			got := settings.Open().State().Config
			if tc.remove {
				if _, owned, err := cache.LoadHelperOwnership(config.Remote.ID); err != nil || owned {
					t.Fatalf("removed host still owns helper: owned=%v err=%v", owned, err)
				}
				if got.Remote != nil || relay.DiagnosticsHealth().SourceID != "" {
					t.Fatal("removed host remains configured")
				}
			} else if !reflect.DeepEqual(got, config) || !reflect.DeepEqual(relay.DiagnosticsHealth(), before) {
				t.Fatal("retained host changed")
			}
		})
	}
}
