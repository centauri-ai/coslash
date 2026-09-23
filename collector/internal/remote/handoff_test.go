//go:build !windows

package remote

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testHandoffName = "0123456789abcdef0123456789abcdef"

func TestStageHandoffTransfersBoundaryPayloadOutsideCommandArguments(t *testing.T) {
	home := t.TempDir()
	special := []byte("🦖\n'\"\\$();&|<>\n")
	payload := append(special, bytes.Repeat([]byte("<"), 64*1024-len(special))...)
	var arguments []string
	options := OpenOptions{
		Limits: Limits{Deadline: 10 * time.Second, MaxStderrBytes: 1024},
		command: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			arguments = append([]string(nil), args...)
			command := exec.CommandContext(ctx, "/bin/sh", "-c", args[len(args)-1])
			command.Env = append(os.Environ(), "HOME="+home)
			return command
		},
	}

	name, err := stageHandoff(context.Background(), "agent-box", payload, options)
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(filepath.Join(home, ".coslash", "handoffs", name))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, payload) {
		t.Fatalf("written payload changed: got %d bytes, want %d", len(written), len(payload))
	}
	joined := strings.Join(arguments, " ")
	if len(joined) > 2048 || strings.Contains(joined, string(payload[:64])) {
		t.Fatalf("SSH arguments contain handoff data: %d bytes", len(joined))
	}
	dirInfo, err := os.Stat(filepath.Join(home, ".coslash", "handoffs"))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("handoff directory mode = %v", dirInfo.Mode().Perm())
	}
	fileInfo, err := os.Stat(filepath.Join(home, ".coslash", "handoffs", name))
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("handoff file mode = %v", fileInfo.Mode().Perm())
	}
}

func TestStageHandoffRemovesPartialFileOnTransferFailure(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(bin, "cat"),
		[]byte("#!/bin/sh\nhead -c 8\nexit 9\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	options := OpenOptions{
		Limits: Limits{Deadline: 10 * time.Second, MaxStderrBytes: 1024},
		command: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			command := exec.CommandContext(ctx, "/bin/sh", "-c", args[len(args)-1])
			command.Env = append(os.Environ(), "HOME="+home, "PATH="+bin+":"+os.Getenv("PATH"))
			return command
		},
	}

	if _, err := stageHandoff(context.Background(), "agent-box", []byte("private handoff"), options); err == nil {
		t.Fatal("stageHandoff succeeded after a partial write")
	}
	entries, err := os.ReadDir(filepath.Join(home, ".coslash", "handoffs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial handoff was not removed: %v", entries)
	}
}

func TestStageHandoffUsesCapabilityTimeoutForTransferAndCleanup(t *testing.T) {
	started := time.Now()
	var deadlines []time.Duration
	options := OpenOptions{
		command: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("handoff command has no deadline")
			}
			deadlines = append(deadlines, deadline.Sub(started))
			if len(deadlines) == 1 {
				return exec.CommandContext(ctx, "/bin/sh", "-c", "exit 1")
			}
			return exec.CommandContext(ctx, "/bin/sh", "-c", "exit 0")
		},
	}

	if _, err := stageHandoff(context.Background(), "agent-box", []byte("private handoff"), options); err == nil {
		t.Fatal("stageHandoff ignored the transfer failure")
	}
	if len(deadlines) != 2 {
		t.Fatalf("handoff command count = %d, want transfer and cleanup", len(deadlines))
	}
	for _, deadline := range deadlines {
		if deadline < DefaultCapabilityTimeout-time.Second || deadline > DefaultCapabilityTimeout+time.Second {
			t.Fatalf("handoff deadline = %v, want about %v", deadline, DefaultCapabilityTimeout)
		}
	}
}

func TestStageHandoffCommandRejectsEarlyEOF(t *testing.T) {
	home := t.TempDir()
	command := stageHandoffCommand(testHandoffName, len("complete payload"))
	process := exec.Command("/bin/sh", "-c", command)
	process.Env = append(os.Environ(), "HOME="+home)
	process.Stdin = strings.NewReader("partial")
	if err := process.Run(); err == nil {
		t.Fatal("stage command accepted a truncated payload")
	}
	path := filepath.Join(home, ".coslash", "handoffs", testHandoffName)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("truncated handoff still exists: %v", err)
	}
}

func TestCleanupHandoffsRemovesExpiredFilesAfterRestart(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".coslash", "handoffs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(dir, "old")
	freshPath := filepath.Join(dir, "fresh")
	for _, path := range []string{oldPath, freshPath} {
		if err := os.WriteFile(path, []byte("private handoff"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	options := OpenOptions{
		command: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			command := exec.CommandContext(ctx, "/bin/sh", "-c", args[len(args)-1])
			command.Env = append(os.Environ(), "HOME="+home)
			return command
		},
	}

	if err := cleanupHandoffs(context.Background(), "agent-box", options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expired handoff still exists: %v", err)
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Fatalf("fresh handoff was removed: %v", err)
	}
}

func TestStageHandoffRemovesCommittedFileWhenSSHReportsFailure(t *testing.T) {
	home := t.TempDir()
	options := OpenOptions{
		Limits: Limits{Deadline: 10 * time.Second, MaxStderrBytes: 1024},
		command: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			command := exec.CommandContext(ctx, "/bin/sh", "-c", args[len(args)-1]+"; exit 255")
			command.Env = append(os.Environ(), "HOME="+home)
			return command
		},
	}
	if _, err := stageHandoff(context.Background(), "agent-box", []byte("complete payload"), options); err == nil {
		t.Fatal("stageHandoff ignored the SSH failure")
	}
	entries, err := os.ReadDir(filepath.Join(home, ".coslash", "handoffs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("committed handoff was not removed: %v", entries)
	}
}

func TestRemoveHandoffDeletesStagedFile(t *testing.T) {
	home := t.TempDir()
	options := OpenOptions{
		Limits: Limits{Deadline: 10 * time.Second, MaxStderrBytes: 1024},
		command: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			command := exec.CommandContext(ctx, "/bin/sh", "-c", args[len(args)-1])
			command.Env = append(os.Environ(), "HOME="+home)
			return command
		},
	}
	name, err := stageHandoff(context.Background(), "agent-box", []byte("private handoff"), options)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".coslash", "handoffs", name)
	if err := removeHandoff(context.Background(), "agent-box", name, options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("staged handoff still exists: %v", err)
	}
}
