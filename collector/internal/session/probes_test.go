package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

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
