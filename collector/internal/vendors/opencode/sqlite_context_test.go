package opencode

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestOpenV2DatabaseFromDataHome(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("XDG_DATA_HOME", dataHome)
	path := filepath.Join(dataHome, "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE session_v2 (id TEXT, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_created INTEGER, time_updated INTEGER, time_archived INTEGER)`,
		`CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`,
		`INSERT INTO session_v2 VALUES ('s', NULL, '/work', 'session', NULL, NULL, NULL, NULL, 0, 0, 1, NULL)`,
	} {
		if _, err := writer.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := openContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_v2`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("v2 rows = %d, error %v", count, err)
	}
}

func TestOpenV2DatabaseOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	t.Setenv("OPENCODE_DB", path)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE session_v2 (id TEXT, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_created INTEGER, time_updated INTEGER, time_archived INTEGER)`,
		`CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`,
		`INSERT INTO session_v2 VALUES ('ses_v2', NULL, '/work', 'v2', NULL, NULL, NULL, NULL, 0, 50, 50, NULL)`,
		`INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES ('u', 'ses_v2', 'user', 1, 100, 100, '{"text":"hello"}')`,
		`INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES ('a', 'ses_v2', 'assistant', 2, 150, 300, '{"content":[{"type":"text","text":"done"}],"finish":"stop","time":{"created":150,"completed":300}}')`,
	} {
		if _, err := writer.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := openContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, skipped, err := load(db, activeFamiliesQuery)
	if err != nil || len(skipped) != 0 || len(got) != 1 || got[0].Session.ID != "ses_v2" {
		t.Fatalf("v2 override sessions = %#v, skipped = %#v, error = %v", got, skipped, err)
	}
	if got[0].Session.LastActivityTime != 300 {
		t.Fatalf("v2 last activity = %d, want 300", got[0].Session.LastActivityTime)
	}
	got, skipped, err = load(db, activeFamiliesQuery+" AND selected_roots.family_updated >= ?", 200)
	if err != nil || len(skipped) != 0 || len(got) != 1 {
		t.Fatalf("v2 incremental sessions = %#v, skipped = %#v, error = %v", got, skipped, err)
	}
}

func TestRootV2RelativeDatabaseOverride(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("OPENCODE_DB", "custom.db")
	got, err := RootContext(context.Background())
	if err != nil || got != filepath.Join(dataHome, "opencode", "custom.db") {
		t.Fatalf("relative v2 database = %q, error = %v", got, err)
	}
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", t.TempDir())
	got, err = RootContext(context.Background())
	want := filepath.Join(os.Getenv("HOME"), ".local", "share", "opencode", "custom.db")
	if err != nil || got != want {
		t.Fatalf("relative v2 database without XDG = %q, want %q, error = %v", got, want, err)
	}
}

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
	t.Setenv("OPENCODE_DB", "")
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
	t.Setenv("OPENCODE_DB", "")
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
