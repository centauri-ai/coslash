package main

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/inventory"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
)

func TestRunInventoryRecordsForCheckInAndWakesTheSyncLoopOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	fingerprints, err := syncv4.OpenFingerprints(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := syncv4.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake := make(chan struct{}, 1)
	tracker := &inventory.Tracker{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runInventory(ctx, fingerprints, queue, wake, tracker, func(context.Context) bool { return true })
	}()
	select {
	case <-wake:
	case <-time.After(10 * time.Second):
		t.Fatal("inventory did not wake the sync loop")
	}
	if inventory := queue.Inventory(); inventory == nil || inventory.Files != 0 || inventory.ScannedAt == "" {
		t.Fatalf("queue inventory = %+v", inventory)
	}
	if tracker.Running() || tracker.FilesSoFar() != 0 {
		t.Fatalf("tracker = running %v, files %d", tracker.Running(), tracker.FilesSoFar())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("inventory loop did not stop on cancellation")
	}
}

func TestRunInventoryWakesSyncLoopWhenAnOlderSessionChanges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	projects := filepath.Join(claude.ProjectsRoot(home), "p")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(projects, "11111111-2222-3333-4444-555555555555.jsonl")
	oldTurns := "{\"sessionId\":\"11111111-2222-3333-4444-555555555555\",\"uuid\":\"old-user\",\"cwd\":\"/repo\",\"timestamp\":\"2026-10-07T10:00:00Z\",\"type\":\"user\",\"message\":{\"content\":\"old prompt\"}}\n" +
		"{\"sessionId\":\"11111111-2222-3333-4444-555555555555\",\"uuid\":\"old-assistant\",\"parentUuid\":\"old-user\",\"cwd\":\"/repo\",\"timestamp\":\"2026-10-07T10:01:00Z\",\"type\":\"assistant\",\"message\":{\"content\":\"old response\"}}\n"
	if err := os.WriteFile(transcript, []byte(oldTurns), 0o600); err != nil {
		t.Fatal(err)
	}
	discoverEntry := func() syncv4.Entry {
		t.Helper()
		sessions, err := inventory.DiscoverAll(t.Context(), inventory.DiscoverOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range sessions {
			if item.Agent == "claude" && item.ID == "11111111-2222-3333-4444-555555555555" {
				return syncv4.Entry{
					Key:       "local-claude-session",
					Selection: sessionbackupproducer.Selection{Agent: item.Agent, SessionID: item.ID},
					Session:   hubclient.V4Session{Agent: item.Agent, LocalKeyHash: "local-claude-session"},
					Activity:  item.LastActivityTime, SourceRevision: item.DetailRevision,
				}
			}
		}
		t.Fatal("discovery did not return the synthetic Claude session")
		return syncv4.Entry{}
	}
	fingerprints, err := syncv4.OpenFingerprints(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := syncv4.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	initialEntry := discoverEntry()
	if initialEntry.Activity >= time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("fixture activity = %d, want a session older than three days", initialEntry.Activity)
	}
	if err := queue.Merge([]syncv4.Entry{initialEntry}, time.Date(2026, 10, 11, 22, 30, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	synced := queue.Entries()[0]
	synced.SyncedActivity, synced.SyncedSourceRevision, synced.RevisionID = synced.Activity, synced.SourceRevision, "revision-before-resume"
	if err := queue.Update(synced); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	wake := make(chan struct{}, 1)
	ticks := make(chan struct{})
	scanned := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		runInventoryWithTicks(ctx, fingerprints, queue, wake, &inventory.Tracker{}, func(context.Context) bool { return true }, ticks, scanned)
	}()
	<-scanned
	select {
	case <-wake:
	default:
		t.Fatal("initial inventory did not wake the sync loop")
	}
	initial := queue.Inventory()
	if initial == nil || initial.Bytes <= 0 {
		t.Fatalf("initial inventory = %+v", initial)
	}
	newTurns := "{\"sessionId\":\"11111111-2222-3333-4444-555555555555\",\"uuid\":\"new-user\",\"parentUuid\":\"old-assistant\",\"cwd\":\"/repo\",\"timestamp\":\"2026-10-11T22:00:00Z\",\"type\":\"user\",\"message\":{\"content\":\"new prompt\"}}\n" +
		"{\"sessionId\":\"11111111-2222-3333-4444-555555555555\",\"uuid\":\"new-assistant\",\"parentUuid\":\"new-user\",\"cwd\":\"/repo\",\"timestamp\":\"2026-10-11T22:01:00Z\",\"type\":\"assistant\",\"message\":{\"content\":\"new response\"}}\n"
	file, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(newTurns); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	ticks <- struct{}{}
	<-scanned
	select {
	case <-wake:
	default:
		t.Fatal("inventory saw no sync wake after an older session grew")
	}
	if got := queue.Inventory(); got == nil || got.Bytes <= initial.Bytes {
		t.Fatalf("inventory after session growth = %+v, want more than %d bytes", got, initial.Bytes)
	}
	updated := discoverEntry()
	if err := queue.Merge([]syncv4.Entry{updated}, time.Date(2026, 10, 11, 22, 30, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if got := queue.Entries()[0]; got.Activity <= initialEntry.Activity || got.RevisionID != "" || got.SourceRevision == got.SyncedSourceRevision {
		t.Fatalf("grown older session was not re-queued: %+v", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("inventory loop did not stop on cancellation")
	}
}

func TestRunInventoryWaitsForPairing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	fingerprints, err := syncv4.OpenFingerprints(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := syncv4.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var paired atomic.Bool
	wake := make(chan struct{}, 1)
	tracker := &inventory.Tracker{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runInventory(ctx, fingerprints, queue, wake, tracker, func(context.Context) bool { return paired.Load() })
	}()
	select {
	case <-wake:
		t.Fatal("inventory woke before pairing")
	case <-time.After(100 * time.Millisecond):
	}
	if queue.Inventory() != nil {
		t.Fatal("inventory scanned before pairing")
	}
	if tracker.Running() {
		t.Fatal("inventory tracker ran before pairing")
	}
	paired.Store(true)
	select {
	case <-wake:
	case <-time.After(5 * time.Second):
		t.Fatal("inventory did not wake after pairing")
	}
	cancel()
	<-done
}
