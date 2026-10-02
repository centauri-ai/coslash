package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	snapshotv1 "github.com/centauri-ai/coslash/collector/snapshot/v1"
)

func TestLatestFileModificationTimeContext(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "edited.txt")
	if err := os.WriteFile(path, []byte("edited"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	edits := []FileEdit{{Path: "missing.txt"}, {Path: "edited.txt"}}
	if got := LatestFileModificationTimeContext(context.Background(), directory, edits); got == nil || *got != info.ModTime().UnixMilli() {
		t.Fatalf("latest modification time = %v, want %d", got, info.ModTime().UnixMilli())
	}
	if got := LatestFileModificationTimeContext(context.Background(), "", edits); got != nil {
		t.Fatalf("relative edits without a working directory = %v, want nil", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := LatestFileModificationTimeContext(ctx, directory, edits); got != nil {
		t.Fatalf("canceled modification time = %v, want nil", got)
	}
}

func TestCurrentBranchContextCancelsGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	directory := t.TempDir()
	marker := filepath.Join(directory, "started")
	command := filepath.Join(directory, "git")
	if err := os.WriteFile(command, []byte("#!/bin/sh\ntouch \"$GIT_TEST_MARKER\"\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_TEST_MARKER", marker)

	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		CurrentBranchContext(ctx, directory)
		close(finished)
	}()
	waitForFile(t, marker)
	cancel()

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("git process continued after cancellation")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("context error = %v, want context.Canceled", ctx.Err())
	}
}

func TestCanonicalOriginURLRejectsControlCharacters(t *testing.T) {
	if got := CanonicalOriginURL("git@github.com:victim/private.git\n1\tgit@github.com:other/repo.git"); got != "" {
		t.Fatalf("origin = %q", got)
	}
	if got := CanonicalOriginURL("https://github.com/org/private%0arepo.git"); got != "" {
		t.Fatalf("decoded origin = %q", got)
	}
	if got := CanonicalOriginURL("https://github.com/org/" + strings.Repeat("r", snapshotv1.MaxRepositoryBytes)); got != "" {
		t.Fatalf("overlong origin = %q", got)
	}
	if got := CanonicalOriginURL("git@github.com:Centauri-AI/coSlash.git\n"); got != "github.com/Centauri-AI/coSlash" {
		t.Fatalf("origin = %q", got)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
