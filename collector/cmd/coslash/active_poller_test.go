package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestPollerWakesOnlyWhenIdle(t *testing.T) {
	control := newSyncLoopControl(nil)
	control.beginPass()
	if signalActiveChange(control, true) {
		t.Fatal("active poller interrupted an in-progress sync pass")
	}
	select {
	case <-control.wake:
		t.Fatal("busy-loop poller queued a wake")
	default:
	}
	control.setIdle(true)
	if !signalActiveChange(control, true) {
		t.Fatal("idle loop did not accept a changed-session wake")
	}
	select {
	case <-control.wake:
	default:
		t.Fatal("idle wake signal was not queued")
	}
}

func TestPollerSilentWhilePaused(t *testing.T) {
	control := newSyncLoopControl(func() bool { return true })
	if signalActiveChange(control, true) {
		t.Fatal("paused device was woken by the active poller")
	}
	select {
	case <-control.wake:
		t.Fatal("paused poller queued a wake")
	default:
	}
}

func TestActivePollSnapshotDetectsRecentChanges(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	old := now.Add(-time.Minute).UnixMilli()
	previous := map[string]string{"session:codex\x00a.jsonl": "" + itoa(old) + ":12", "root:/codex": "1:0"}
	modified := map[string]string{"session:codex\x00a.jsonl": "" + itoa(now.UnixMilli()) + ":20", "root:/codex": "2:0"}
	if !activeSnapshotChanged(previous, modified, now) {
		t.Fatal("active session change was not detected")
	}
	agedOut := map[string]string{"root:/codex": "1:0"}
	if activeSnapshotChanged(previous, agedOut, now.Add(31*time.Minute)) {
		t.Fatal("session older than 30 minutes caused an active wake")
	}
}

func TestActivePollTracksOpenCodeDatabaseOverride(t *testing.T) {
	home := t.TempDir()
	database := filepath.Join(t.TempDir(), "custom.db")
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("OPENCODE_DB", database)
	if err := os.WriteFile(database, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := os.Chtimes(database, now, now); err != nil {
		t.Fatal(err)
	}
	previous, err := activeSnapshot(context.Background(), home, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := previous["root:"+database]; !ok {
		t.Fatalf("custom OpenCode database was not tracked: %v", previous)
	}

	if err := os.WriteFile(database, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	changedAt := now.Add(time.Minute)
	if err := os.Chtimes(database, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}
	current, err := activeSnapshot(context.Background(), home, changedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !activeSnapshotChanged(previous, current, changedAt) {
		t.Fatal("custom OpenCode database change did not trigger a sync wake")
	}
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}
