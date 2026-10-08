package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

type commandRelay struct {
	validateErr, applyErr error
	applied               *settings.RemoteSettings
	uninstallErr          error
	uninstalls            int
}

func (r *commandRelay) ValidateSettingsChange(*settings.RemoteSettings) error { return r.validateErr }
func (r *commandRelay) ApplySettings(next *settings.RemoteSettings) error {
	r.applied = next
	return r.applyErr
}
func (r *commandRelay) UninstallHelper(context.Context) error { r.uninstalls++; return r.uninstallErr }

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
	if state.Config.Remote.ID == "" || state.Config.Remote.SSHAlias != "agent-box" || !state.Config.Remote.Enabled {
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
	relay.uninstallErr = errors.New("uninstall failed")
	if err := configureCommandSSHHost(context.Background(), local, relay, "22222222-2222-3333-4444-555555555555", "different-box"); err == nil {
		t.Fatal("replacement bypassed failed old-host cleanup")
	}
	if local.State().Config.Remote.SSHAlias != "agent-box" {
		t.Fatal("rejected host replaced local settings")
	}
	relay.uninstallErr = nil
	if err := configureCommandSSHHost(context.Background(), local, relay, "22222222-2222-3333-4444-555555555555", "different-box"); err != nil {
		t.Fatal(err)
	}
	if relay.uninstalls != 2 || local.State().Config.Remote.SSHAlias != "different-box" {
		t.Fatalf("replacement state=%+v cleanups=%d", local.State().Config.Remote, relay.uninstalls)
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
	routes(nil, nil, nil, nil, nil, serverServices{queue: queue}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/hub/v4-update", nil))
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
