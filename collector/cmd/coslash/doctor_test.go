package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

func TestRunDoctorIncludesConfiguredRemote(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("HOME", home)

	id := "r_0123456789abcdef"
	store := settings.Open()
	config := store.State().Config
	config.Remote = &settings.RemoteSettings{ID: id, SSHAlias: "gpu-server", Enabled: true}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	if err := remote.NewCache(home).Store(id, remote.CachedSnapshot{
		View: remoteviewv1.View{
			SchemaVersion:    remoteviewv1.SchemaVersion,
			CollectorVersion: "dev",
			Capabilities:     []string{remoteviewv1.CapabilityRemoteView},
			LaunchableAgents: []string{remoteviewv1.AgentClaude},
			RequestNowMs:     1000,
			HostNowMs:        1200,
			CollectedAtMs:    1100,
			Host:             remoteviewv1.Host{OS: "linux", Arch: "arm64"},
			Sessions:         []remoteviewv1.Session{},
		},
		FetchedAtMs:      1000,
		ClockOffsetMs:    50,
		RoundTripMs:      20,
		CollectorVersion: "dev",
		SchemaVersion:    remoteviewv1.SchemaVersion,
		Capabilities:     []string{remoteviewv1.CapabilityRemoteView},
		LaunchableAgents: []string{remoteviewv1.AgentClaude},
		HostOS:           "linux",
		HostArch:         "arm64",
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runDoctor(&stdout, &stderr, []string{"--json"})
	if code != 0 && code != 1 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var body map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, stdout.String())
	}
	remoteBlock, ok := body["remote"].(map[string]any)
	if !ok {
		t.Fatalf("missing remote block: %s", stdout.String())
	}
	if remoteBlock["label"] != "gpu-server" || remoteBlock["state"] != "ok" {
		t.Fatalf("remote=%v", remoteBlock)
	}
	checks, _ := body["checks"].([]any)
	found := false
	for _, raw := range checks {
		check, _ := raw.(map[string]any)
		if check["id"] == "remote" {
			found = true
			if !strings.Contains(check["detail"].(string), "gpu-server") {
				t.Fatalf("remote check=%v", check)
			}
		}
	}
	if !found {
		t.Fatalf("missing remote check in %v", checks)
	}
	if _, err := os.Stat(filepath.Join(home, "remotes", id, "snapshot.json")); err != nil {
		t.Fatalf("doctor must not delete cache: %v", err)
	}
}
