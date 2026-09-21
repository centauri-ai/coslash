package opencode

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRootContextCancelsOpenCodeLookup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	directory := t.TempDir()
	command := filepath.Join(directory, "opencode")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_DATA_HOME", "")
	originalCommandContext := commandContext
	started := make(chan struct{})
	commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		close(started)
		return exec.CommandContext(ctx, name, args...)
	}
	t.Cleanup(func() { commandContext = originalCommandContext })

	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		_, err := RootContext(ctx)
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("OpenCode lookup did not start")
	}
	cancel()

	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opencode process continued after cancellation")
	}
}

func TestRootContextFallsBackAfterLookupTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	directory := t.TempDir()
	command := filepath.Join(directory, "opencode")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", directory)
	t.Setenv("XDG_DATA_HOME", "")
	originalTimeout := databasePathLookupTimeout
	databasePathLookupTimeout = 10 * time.Millisecond
	t.Cleanup(func() { databasePathLookupTimeout = originalTimeout })

	got, err := RootContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(directory, ".local", "share", "opencode", "opencode.db")
	if got != want {
		t.Fatalf("root = %q, want %q", got, want)
	}
}
