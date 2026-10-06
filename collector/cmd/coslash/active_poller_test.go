package main

import (
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

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}
