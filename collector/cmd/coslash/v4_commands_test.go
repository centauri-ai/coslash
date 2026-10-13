package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

type commandRelay struct {
	validateErr, applyErr error
	applied               *settings.RemoteSettings
}

func (r *commandRelay) SaveHubSettings(local *settings.Store, config settings.Config) error {
	if r.validateErr != nil {
		return r.validateErr
	}
	before := local.State()
	if err := local.Save(config); err != nil {
		return err
	}
	if r.applyErr != nil {
		if err := local.Restore(before); err != nil {
			return errors.Join(r.applyErr, err)
		}
		return r.applyErr
	}
	r.applied = config.Remote
	return nil
}

func TestSSHInstallPersistsMissingSettings(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	local := settings.Open()
	if local.State().Persisted {
		t.Fatal("settings already exist")
	}
	if err := configureCommandSSHHost(context.Background(), local, &commandRelay{}, "11111111-2222-3333-4444-555555555555", "agent-box"); err != nil {
		t.Fatal(err)
	}
	state := settings.Open().State()
	if !state.Valid || !state.Persisted || state.Config.Remote == nil {
		t.Fatalf("persisted settings=%+v", state)
	}
	if state.Config.Remote.HostID != "11111111-2222-3333-4444-555555555555" || state.Config.Remote.ID == "" || state.Config.Remote.SSHAlias != "agent-box" || !state.Config.Remote.Enabled {
		t.Fatalf("persisted host=%+v", state.Config.Remote)
	}
	state.Config.Remote = nil
	if !reflect.DeepEqual(state.Config, settings.Defaults()) {
		t.Fatalf("defaults changed: %+v", state.Config)
	}
}

func TestSSHInstallRejectsInvalidSettings(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	const invalid = "invalid settings"
	if err := os.WriteFile(settings.Path(), []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := configureCommandSSHHost(context.Background(), settings.Open(), &commandRelay{}, "11111111-2222-3333-4444-555555555555", "agent-box"); err == nil {
		t.Fatal("invalid settings accepted")
	}
	data, err := os.ReadFile(settings.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != invalid {
		t.Fatal("invalid settings overwritten")
	}
}

func TestSSHInstallConfiguresOnlyConfirmedLocalHost(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	local := settings.Open()
	if err := local.Save(settings.Defaults()); err != nil {
		t.Fatal(err)
	}
	relay := &commandRelay{}
	if err := configureCommandSSHHost(context.Background(), local, relay, "11111111-2222-3333-4444-555555555555", "agent-box"); err != nil {
		t.Fatal(err)
	}
	if relay.applied == nil || relay.applied.SSHAlias != "agent-box" || !relay.applied.Enabled || local.State().Config.Remote == nil {
		t.Fatalf("configured host=%+v", relay.applied)
	}
	relay.validateErr = errors.New("replacement rejected")
	if err := configureCommandSSHHost(context.Background(), local, relay, "22222222-2222-3333-4444-555555555555", "different-box"); err == nil {
		t.Fatal("replacement bypassed failed validation")
	}
	if local.State().Config.Remote.SSHAlias != "agent-box" {
		t.Fatal("rejected host replaced local settings")
	}
	relay.validateErr = nil
	if err := configureCommandSSHHost(context.Background(), local, relay, "22222222-2222-3333-4444-555555555555", "different-box"); err != nil {
		t.Fatal(err)
	}
	if local.State().Config.Remote.SSHAlias != "different-box" {
		t.Fatalf("replacement state=%+v", local.State().Config.Remote)
	}
}

func TestSSHConfirmationWithSameAliasReplacesSource(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	local := settings.Open()
	config := settings.Defaults()
	config.Remote = &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", HostID: "old-host", Enabled: true}
	if err := local.Save(config); err != nil {
		t.Fatal(err)
	}
	cacheRoot := t.TempDir()
	cache := remote.NewCache(cacheRoot)
	if err := cache.StoreHelperVersion(config.Remote.ID, "v1", config.Remote.SSHAlias); err != nil {
		t.Fatal(err)
	}
	relay := remote.NewManager(remote.Options{Cache: cache})
	if err := relay.ApplySettings(config.Remote); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(relay.Shutdown)

	if err := configureCommandSSHHost(context.Background(), local, relay, "new-host", config.Remote.SSHAlias); err != nil {
		t.Fatal(err)
	}
	confirmed := local.State().Config.Remote
	if confirmed == nil || confirmed.HostID != "new-host" || confirmed.ID != remoteSourceID("new-host") || confirmed.ID == config.Remote.ID {
		t.Fatalf("same-alias host replacement kept old source identity: %+v", confirmed)
	}
	if relay.DiagnosticsHealth().SourceID != confirmed.ID {
		t.Fatalf("live source id = %q, want %q", relay.DiagnosticsHealth().SourceID, confirmed.ID)
	}
	if _, owned, err := cache.LoadHelperOwnership(config.Remote.ID); err != nil || owned {
		t.Fatalf("previous source cache was retained: owned=%v err=%v", owned, err)
	}
}

func TestSSHInstallRestoresSettingsWhenRelayApplyFails(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	local := settings.Open()
	if err := local.Save(settings.Defaults()); err != nil {
		t.Fatal(err)
	}
	relay := &commandRelay{applyErr: errors.New("apply failed")}
	if err := configureCommandSSHHost(context.Background(), local, relay, "11111111-2222-3333-4444-555555555555", "agent-box"); err == nil {
		t.Fatal("missing relay failure")
	}
	if local.State().Config.Remote != nil {
		t.Fatal("failed relay changed persisted settings")
	}
}

func TestSSHInstallDoesNotPersistMissingSettingsWhenRelayApplyFails(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	local := settings.Open()
	if state := local.State(); !state.Valid || state.Persisted {
		t.Fatalf("initial settings=%+v", state)
	}

	relay := &commandRelay{applyErr: errors.New("apply failed")}
	if err := configureCommandSSHHost(context.Background(), local, relay, "11111111-2222-3333-4444-555555555555", "agent-box"); err == nil {
		t.Fatal("missing relay failure")
	}
	state := local.State()
	if !state.Valid || state.Persisted || !reflect.DeepEqual(state.Config, settings.Defaults()) {
		t.Fatalf("restored settings=%+v", state)
	}
	if _, err := os.Stat(settings.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("settings file exists after rollback: %v", err)
	}
}

func TestLocalUpdateRouteUsesDurablePrompt(t *testing.T) {
	queue, err := syncv4.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.ApplyPolicy(hubclient.V4CheckIn{ConfigVersion: 1, MinVersion: "0.0.5", RecommendedVersion: "0.0.6", RecommendedDownloadURL: "https://download.example/app", UpdateRequired: true}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	routesWithOnboarding(nil, nil, nil, nil, nil, newOnboardingManager(version), serverServices{queue: queue}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/hub/v4-update", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	var prompt syncv4.UpdatePrompt
	if err := json.Unmarshal(response.Body.Bytes(), &prompt); err != nil {
		t.Fatal(err)
	}
	if !prompt.Available || !prompt.Required || prompt.DownloadURL != "https://download.example/app" {
		t.Fatalf("prompt=%+v", prompt)
	}
}

func TestSSHCommandPreservesAuthenticationFailure(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	relay := remote.NewManager(remote.Options{Cache: remote.NewCache(t.TempDir()), Open: func(context.Context, string, remote.OpenOptions) (*remote.Session, error) {
		return nil, errors.New("Permission denied (publickey). /private/key user@host\n\x1b[31m")
	}})
	err := v4CommandRunner(nil, nil, relay)(context.Background(), hubclient.V4Command{Type: "ssh.test", Payload: json.RawMessage(`{"hostId":"host-one","alias":"agent-box"}`)})
	var failure *syncv4.SSHCommandError
	if !errors.As(err, &failure) || failure.Reason == nil || *failure.Reason != remote.ReasonAuthentication {
		t.Fatalf("SSH command error = %v, want classified authentication failure", err)
	}
	if !strings.Contains(output.String(), "Permission denied (publickey)") || strings.Contains(output.String(), "/private/key") || strings.Contains(output.String(), "user@host") || strings.Contains(output.String(), "\x1b") {
		t.Fatalf("unsafe or missing SSH diagnostic: %q", output.String())
	}
	if strings.Count(output.String(), "ssh.test failed:") != 1 || strings.Contains(output.String(), "SSH command failed") {
		t.Fatalf("unexpected duplicate SSH diagnostic: %q", output.String())
	}
}

func TestSSHInstallPreservesHelperFailure(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	local := settings.Open()
	if err := local.Save(settings.Defaults()); err != nil {
		t.Fatal(err)
	}
	relay := remote.NewManager(remote.Options{Cache: remote.NewCache(t.TempDir())})
	err := v4CommandRunner(nil, local, relay)(context.Background(), hubclient.V4Command{Type: "ssh.install", Payload: json.RawMessage(`{"hostId":"host-one","alias":"agent-box"}`)})
	var failure *syncv4.SSHCommandError
	if !errors.As(err, &failure) || !failure.Install || failure.Reason == nil || *failure.Reason != remote.ReasonHelperMissing {
		t.Fatalf("SSH install error = %v, want helper missing", err)
	}
	if host := settings.Open().State().Config.Remote; host == nil || host.HostID != "" {
		t.Fatalf("failed install confirmed host: %+v", host)
	}

}

func TestSSHTestFailureKeepsWorkingHost(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	local := settings.Open()
	config := settings.Defaults()
	config.Remote = &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "working-box", HostID: "working-host", Enabled: true}
	if err := local.Save(config); err != nil {
		t.Fatal(err)
	}
	relay := remote.NewManager(remote.Options{Cache: remote.NewCache(t.TempDir()), Open: func(context.Context, string, remote.OpenOptions) (*remote.Session, error) {
		return nil, errors.New("offline")
	}})
	if err := relay.ApplySettings(config.Remote); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(relay.Shutdown)
	before := relay.DiagnosticsHealth()
	if err := v4CommandRunner(nil, local, relay)(t.Context(), hubclient.V4Command{Type: "ssh.test", Payload: json.RawMessage(`{"hostId":"candidate-host","alias":"offline-box"}`)}); err == nil {
		t.Fatal("offline test succeeded")
	}
	if got := settings.Open().State().Config; !reflect.DeepEqual(got, config) {
		t.Fatalf("failed test replaced working host: %+v", got.Remote)
	}
	if got := relay.DiagnosticsHealth(); !reflect.DeepEqual(got, before) {
		t.Fatalf("failed test changed live host: %+v", got)
	}
}

func TestSSHConfirmationSaveFailureKeepsOldHelper(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	local := settings.Open()
	config := settings.Defaults()
	config.Remote = &settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "working-box", HostID: "working-host", Enabled: true}
	if err := local.Save(config); err != nil {
		t.Fatal(err)
	}
	blockedHome := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blockedHome, []byte("block save"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COSLASH_HOME", blockedHome)
	cache := remote.NewCache(t.TempDir())
	if err := cache.StoreHelperVersion(config.Remote.ID, "v1", config.Remote.SSHAlias); err != nil {
		t.Fatal(err)
	}
	relay := remote.NewManager(remote.Options{Cache: cache})
	if err := relay.ApplySettings(config.Remote); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(relay.Shutdown)
	before := relay.DiagnosticsHealth()
	if err := configureCommandSSHHost(t.Context(), local, relay, "replacement-host", "replacement-box"); err == nil || !strings.Contains(err.Error(), "create coSlash directory") {
		t.Fatalf("expected save failure, got %v", err)
	}
	if !reflect.DeepEqual(relay.DiagnosticsHealth(), before) {
		t.Fatal("failed save changed live helper state")
	}
	if _, owned, err := cache.LoadHelperOwnership(config.Remote.ID); err != nil || !owned {
		t.Fatalf("failed save lost helper ownership: %v %v", owned, err)
	}
	t.Setenv("COSLASH_HOME", home)
	if !reflect.DeepEqual(local.State().Config, config) || !reflect.DeepEqual(settings.Open().State().Config, config) {
		t.Fatal("failed save changed old host settings")
	}
}
