package diagnostics

import (
	"strings"
	"testing"
)

func TestRemoteCheckStates(t *testing.T) {
	t.Parallel()
	reason := "collector_missing"
	cases := []struct {
		name   string
		remote Remote
		status Status
		want   string
		fix    string
	}{
		{
			name:   "ok",
			remote: Remote{Label: "gpu-server", State: "ok", Complete: true},
			status: StatusOK,
			want:   "gpu-server is connected.",
		},
		{
			name:   "connecting incomplete",
			remote: Remote{Label: "gpu-server", State: "connecting", Reason: strPtr("broader_history")},
			status: StatusWarn,
			want:   "broader history",
		},
		{
			name:   "limited",
			remote: Remote{Label: "gpu-server", State: "limited", Reason: strPtr("history_truncated"), Error: "history truncated"},
			status: StatusWarn,
			want:   "limited history",
		},
		{
			name:   "setup",
			remote: Remote{Label: "gpu-server", State: "setup_required", Reason: &reason, Error: "collector missing"},
			status: StatusFail,
			want:   "~/.local/bin/coslash",
			fix:    RemoteInstallationGuidePath,
		},
		{
			name:   "upgrade",
			remote: Remote{Label: "gpu-server", State: "upgrade_required", Error: "collector upgrade required"},
			status: StatusFail,
			want:   "newer Linux collector",
			fix:    RemoteInstallationGuidePath,
		},
		{
			name:   "stale",
			remote: Remote{Label: "gpu-server", State: "stale", Error: "connection failed"},
			status: StatusWarn,
			want:   "last good snapshot",
		},
		{
			name:   "error",
			remote: Remote{Label: "gpu-server", State: "error", Error: "invalid remote transport"},
			status: StatusFail,
			want:   "refresh failed",
		},
		{
			name:   "disabled",
			remote: Remote{Label: "gpu-server", State: "disabled"},
			status: StatusOK,
			want:   "disabled",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			check := remoteCheck(&tc.remote)
			if check.Status != tc.status {
				t.Fatalf("status=%s want %s", check.Status, tc.status)
			}
			if !strings.Contains(check.Detail, tc.want) {
				t.Fatalf("detail=%q want substring %q", check.Detail, tc.want)
			}
			if tc.fix != "" && !strings.Contains(check.Fix, tc.fix) {
				t.Fatalf("fix=%q want substring %q", check.Fix, tc.fix)
			}
		})
	}
}

func TestFormatForCopyOmitsSensitiveRemoteValues(t *testing.T) {
	t.Parallel()
	reason := "connection_failed"
	lastSuccess := int64(1_700_000_000_000)
	coverage := int64(1_699_000_000_000)
	offset := int64(1200)
	roundTrip := int64(420)
	nextRetry := int64(1_700_000_180_000)
	snapshot := &Snapshot{
		Version:     "v0.0.0-test",
		GeneratedAt: lastSuccess,
		Platform:    Platform{OS: "darwin", Arch: "arm64", TerminalLaunchSupported: true},
		Storage:     Storage{Home: "~/.coslash", Writable: true},
		Sources: []Source{{
			Agent: "codex", Label: "Codex", Root: "~/.codex", State: SourceOK,
		}},
		Remote: &Remote{
			SourceID:         "r_0123456789abcdef",
			Label:            "gpu-server",
			State:            "stale",
			Complete:         false,
			Reason:           &reason,
			CollectorVersion: "v0.0.0-test",
			SchemaVersion:    "remote-session-view/v1",
			Capabilities:     []string{"remote-session-view/v1", "remote-launch/v1"},
			LaunchableAgents: []string{"claude", "codex"},
			HostOS:           "linux",
			HostArch:         "arm64",
			LastSuccessAtMs:  &lastSuccess,
			CoverageSinceMs:  &coverage,
			ClockOffsetMs:    &offset,
			RoundTripMs:      &roundTrip,
			NextRetryAtMs:    &nextRetry,
			Error:            "connection failed",
			DiagnosticStderr: "Permission denied [redacted]",
		},
		Checks: []Check{remoteCheck(&Remote{Label: "gpu-server", State: "stale", Error: "connection failed"})},
	}
	text := FormatForCopy(snapshot)
	for _, required := range []string{
		"alias=gpu-server",
		"state=stale",
		"capabilities=remote-session-view/v1,remote-launch/v1",
		"launchableAgents=claude,codex",
		"platform=linux/arm64",
		"clockOffsetMs=1200",
		"roundTripMs=420",
		"nextRetryAtMs=1700000180000",
		"error=connection failed",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing %q in:\n%s", required, text)
		}
	}
	for _, forbidden := range []string{
		"ubuntu@",
		"example.com",
		"/home/",
		"/.ssh/",
		"PRIVATE KEY",
		"id_ed25519",
		"/Users/",
		"transcript",
		"handoff body",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("sensitive value %q leaked in:\n%s", forbidden, text)
		}
	}
}

func strPtr(value string) *string { return &value }
