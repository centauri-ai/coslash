package remote

import (
	"context"
	"os/exec"
	"sync"
	"testing"
	"time"
)

func TestCodexLiveParsesOpenRollouts(t *testing.T) {
	const id = "019f4dde-db5b-7100-bdc0-09b5aaaac56f"
	var command string
	options := OpenOptions{command: func(_ context.Context, _ string, args ...string) *exec.Cmd {
		command = args[len(args)-1]
		script := "printf 'n/home/dev/.codex/sessions/rollout-2026-07-10T14-11-18-" + id + ".jsonl\\n'"
		return exec.Command("sh", "-c", script)
	}}
	found := sshTarget{"host", options}.codexLive(context.Background())
	if _, ok := found[id]; !ok || len(found) != 1 {
		t.Fatalf("live = %#v", found)
	}
	if command != codexLivenessCommand {
		t.Fatalf("command = %q", command)
	}
}

func TestCodexLiveTreatsLsofMissAsInactive(t *testing.T) {
	options := OpenOptions{command: func(context.Context, string, ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "exit 1")
	}}
	found := sshTarget{"host", options}.codexLive(context.Background())
	if len(found) != 0 {
		t.Fatalf("live = %#v", found)
	}
}

func TestCollectIncrementalProbesCodexLivenessOverSSH(t *testing.T) {
	var mu sync.Mutex
	var commands []string
	source := newFakeSource(newFakeFS(), Limits{})
	source.ssh = sshTarget{"host", OpenOptions{command: func(_ context.Context, _ string, args ...string) *exec.Cmd {
		mu.Lock()
		commands = append(commands, args[len(args)-1])
		mu.Unlock()
		return exec.Command("sh", "-c", "exit 1")
	}}}
	if _, _, _, err := collectIncremental(context.Background(), source, 0, time.Unix(2000, 0), CachedSnapshotV2{}); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 || commands[0] != codexLivenessCommand {
		t.Fatalf("commands = %q", commands)
	}
}

func TestCollectCodexVendorMarksLiveSessions(t *testing.T) {
	outcome, _ := collectCodexVendor(newFakeSource(newFakeFS(), Limits{}), "r_0123456789abcdef", fakeHome, 0, nil, nil, map[string]struct{}{"session-1": {}})
	if outcome.Err != nil || outcome.Metadata.LiveSessions()["session-1"] != "interactive" {
		t.Fatalf("outcome = %#v", outcome)
	}
}
