package remote

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCodexLiveParsesOpenRollouts(t *testing.T) {
	const id = "019f4dde-db5b-7100-bdc0-09b5aaaac56f"
	var command string
	options := OpenOptions{command: func(_ context.Context, _ string, args ...string) *exec.Cmd {
		command = args[len(args)-1]
		script := "printf 'nrollout-2026-07-10T14-11-18-" + id + ".jsonl\\n'"
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

func TestCodexLivenessCommandFiltersPathsBeforeSSH(t *testing.T) {
	const (
		live     = "n/home/dev/.codex/sessions/2026/07/10/rollout-2026-07-10T14-11-18-019f4dde-db5b-7100-bdc0-09b5aaaac56f.jsonl"
		archived = "n/home/dev/.codex/archived_sessions/rollout-2026-07-09T14-11-18-019f4dde-db5b-7100-bdc0-09b5aaaac570.jsonl"
	)
	bin := t.TempDir()
	contents := "#!/bin/sh\nprintf '%s\\n' 'p123' 'n/home/dev/private.txt' '" + live + "' '" + archived + "' " +
		"'n/home/dev/.codex/sessions/2026/07/10/notes.jsonl' 'n/home/dev/repo/rollout-x.jsonl'\n"
	if err := os.WriteFile(filepath.Join(bin, "lsof"), []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	options := OpenOptions{command: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "sh", "-c", args[len(args)-1])
		cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		return cmd
	}}
	output, err := (sshTarget{"host", options}).run(context.Background(), codexLivenessCommand, maxCodexLivenessBytes, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := live + "\n" + archived + "\n"; output != want {
		t.Fatalf("output = %q, want %q", output, want)
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

func TestRefreshIncrementalRunsProbesOnSessionContext(t *testing.T) {
	sessionCtx, cancel := context.WithCancel(context.Background())
	cancel()
	source := newFakeSource(newFakeFS(), Limits{})
	source.ssh = sshTarget{"host", OpenOptions{command: func(context.Context, string, ...string) *exec.Cmd {
		t.Error("probe ran after the SSH session ended")
		return exec.Command("sh", "-c", "exit 1")
	}}}
	connection := &Session{source: source, ctx: sessionCtx, stderr: &cappedStderr{}}
	connection.closeOnce.Do(func() {})
	open := func(context.Context, string, OpenOptions) (*Session, error) { return connection, nil }
	if _, err := refreshIncrementalWithOpen(context.Background(), "host", 0, time.Unix(2000, 0), CachedSnapshotV2{}, open); err != nil {
		t.Fatal(err)
	}
}

func TestCollectCodexVendorMarksLiveSessions(t *testing.T) {
	outcome, _ := collectCodexVendor(newFakeSource(newFakeFS(), Limits{}), "r_0123456789abcdef", fakeHome, 0, nil, nil, map[string]struct{}{"session-1": {}})
	if outcome.Err != nil || outcome.Metadata.LiveSessions()["session-1"] != "interactive" {
		t.Fatalf("outcome = %#v", outcome)
	}
}
