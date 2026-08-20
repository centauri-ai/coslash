package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

func TestRunSnapshotProbeWritesFramedCapabilities(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runSnapshot(&stdout, &stderr, []string{"--probe"})
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	payload, err := remoteviewv1.DecodeExactFrame(stdout.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	probe, err := remoteviewv1.DecodeProbe(payload)
	if err != nil {
		t.Fatal(err)
	}
	if probe.SchemaVersion != remoteviewv1.SchemaVersion {
		t.Fatalf("schema = %q", probe.SchemaVersion)
	}
	if !contains(probe.Capabilities, remoteviewv1.CapabilityRemoteView) {
		t.Fatalf("capabilities = %#v", probe.Capabilities)
	}
	for _, capability := range probe.Capabilities {
		if capability == remoteviewv1.CapabilityRemoteLaunch {
			t.Fatal("P1 must not advertise remote-launch before P2")
		}
	}
	if probe.HostNowMs <= 0 || probe.Host.OS == "" || probe.Host.Arch == "" {
		t.Fatalf("probe host facts missing: %#v", probe)
	}
	if strings.Contains(stdout.String(), "/") && strings.Contains(strings.ToLower(stdout.String()), "bin") {
		// Probe must not return executable paths; launchableAgents are bare names.
		for _, agent := range probe.LaunchableAgents {
			if strings.Contains(agent, "/") {
				t.Fatalf("launchable agent leaked a path: %q", agent)
			}
		}
	}
}

func TestRunSnapshotRejectsInvalidArguments(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "since after request-now", args: []string{"--since", "200", "--request-now", "100", "--agents", "claude,codex"}},
		{name: "wrong agents", args: []string{"--since", "0", "--request-now", "100", "--agents", "claude"}},
		{name: "extra arg", args: []string{"--probe", "extra"}},
		{name: "probe with since", args: []string{"--probe", "--since", "0"}},
		{name: "missing request-now", args: []string{"--since", "0", "--agents", "claude,codex"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runSnapshot(&stdout, &stderr, test.args)
			if code != 2 {
				t.Fatalf("exit=%d stderr=%s", code, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q", stdout.String())
			}
		})
	}
}

func TestRejectMixedServerFlags(t *testing.T) {
	if err := rejectMixedServerFlags([]string{"--probe", "--port", "9"}); err == nil {
		t.Fatal("mixed server flag accepted")
	}
	if err := rejectMixedServerFlags([]string{"--since", "0", "--request-now", "1", "--agents", "claude,codex"}); err != nil {
		t.Fatal(err)
	}
}

func TestRunSnapshotEmitsSkewAwareCoverage(t *testing.T) {
	// Empty homes keep collection fast and deterministic in CI sandboxes.
	t.Setenv("HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	since := time.Now().Add(-time.Hour).UnixMilli()
	requestNow := time.Now().UnixMilli()
	code := runSnapshot(&stdout, &stderr, []string{
		"--since", itoa(since),
		"--request-now", itoa(requestNow),
		"--agents", "claude,codex",
	})
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	payload, err := remoteviewv1.DecodeExactFrame(stdout.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	view, err := remoteviewv1.Decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	if view.RequestedSinceMs != since || view.RequestNowMs != requestNow || view.CoverageSinceMs != since {
		t.Fatalf("mac clock fields = since=%d requestNow=%d coverage=%d",
			view.RequestedSinceMs, view.RequestNowMs, view.CoverageSinceMs)
	}
	if view.HostNowMs <= 0 || view.CollectedAtMs <= 0 {
		t.Fatalf("host clocks missing: %#v", view)
	}
	if view.Truncated {
		t.Fatalf("empty home should not truncate: %#v", view.TruncationReason)
	}
}

func TestParseEpochMillis(t *testing.T) {
	got, err := parseEpochMillis("since", "0")
	if err != nil || got != 0 {
		t.Fatalf("got=%d err=%v", got, err)
	}
	if _, err := parseEpochMillis("since", "01"); err == nil {
		t.Fatal("leading zero accepted")
	}
	if _, err := parseEpochMillis("since", "-1"); err == nil {
		t.Fatal("negative accepted")
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func itoa(value int64) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}
