package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/inventory"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
)

type inventorySyncHub struct{}

func (inventorySyncHub) V4Binding(context.Context) (string, error) {
	return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil
}
func (inventorySyncHub) V4CheckIn(context.Context, hubclient.V4Queue, int64, []hubclient.V4CommandResult, []string, []hubclient.V4LogEntry) (hubclient.V4CheckIn, error) {
	return hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{LeaveOut: []string{}}, Capabilities: []string{"scale-import/v1"}, NextCheckInSeconds: 300}, nil
}
func (inventorySyncHub) V4Create(context.Context, hubclient.V4Create) (hubclient.V4Status, error) {
	return hubclient.V4Status{}, errors.New("unexpected create")
}
func (inventorySyncHub) V4Status(context.Context, string) (hubclient.V4Status, error) {
	return hubclient.V4Status{}, errors.New("unexpected status")
}
func (inventorySyncHub) V4PutChunk(context.Context, string, hubclient.V4Missing, io.Reader) error {
	return errors.New("unexpected chunk upload")
}
func (inventorySyncHub) V4Confirm(context.Context, string, ...hubclient.V4Missing) (hubclient.V4Status, error) {
	return hubclient.V4Status{}, errors.New("unexpected confirm")
}
func (inventorySyncHub) V4Finalize(context.Context, string) (hubclient.V4Status, error) {
	return hubclient.V4Status{}, errors.New("unexpected finalize")
}

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

func TestRunInventorySchedulesShortRetryForFreshSessionFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	transcript := filepath.Join(claude.ProjectsRoot(home), "synthetic", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte("synthetic session still being written\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprints, err := syncv4.OpenFingerprints(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := syncv4.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var scheduledRetry time.Duration
	wake := make(chan struct{}, 2)
	scanned := make(chan struct{}, 2)
	waits := 0
	runInventoryWithSchedule(t.Context(), fingerprints, queue, wake, &inventory.Tracker{}, func(context.Context) bool { return true }, func(_ context.Context, retryAfter time.Duration, _ time.Time) (bool, bool, bool) {
		if waits == 0 {
			scheduledRetry = retryAfter
			waits++
			return true, true, false
		}
		return false, false, false
	}, scanned)
	if scheduledRetry != inventorySettleRetryDelay {
		t.Fatalf("retry after fresh source = %s, want %s", scheduledRetry, inventorySettleRetryDelay)
	}
	if waits != 1 {
		t.Fatalf("inventory retries = %d, want one short retry", waits)
	}
	for range 2 {
		select {
		case <-scanned:
		default:
			t.Fatal("settle retry did not run a second inventory")
		}
		select {
		case <-wake:
		default:
			t.Fatal("settle retry did not wake discovery")
		}
	}
}

func TestInventorySettleRetryUsesScanWindow(t *testing.T) {
	scannedAt := time.UnixMilli(1_800_000_000_000)
	scanDuration := time.Second
	tests := []struct {
		name      string
		modTimeMs int64
		wantRetry bool
	}{
		{name: "recent before scan", modTimeMs: scannedAt.Add(-time.Second).UnixMilli(), wantRetry: true},
		{name: "modified during scan", modTimeMs: scannedAt.Add(500 * time.Millisecond).UnixMilli(), wantRetry: true},
		{name: "future after scan", modTimeMs: scannedAt.Add(2 * time.Second).UnixMilli()},
		{name: "older than settle window", modTimeMs: scannedAt.Add(-inventorySettleRetryDelay).UnixMilli()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := &inventory.Snapshot{
				ScannedAt: scannedAt,
				Duration:  scanDuration,
				Files:     []inventory.File{{ModTimeMs: test.modTimeMs}},
			}
			gotRetry := inventorySettleRetry(snapshot) > 0
			if gotRetry != test.wantRetry {
				t.Fatalf("retry = %t, want %t", gotRetry, test.wantRetry)
			}
		})
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

func TestInventoryWakeRefreshesOlderSessionInOneScan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("COSLASH_SCALE_IMPORT", "1")
	projects := filepath.Join(claude.ProjectsRoot(home), "p")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	const id = "11111111-2222-3333-4444-555555555555"
	transcript := filepath.Join(projects, id+".jsonl")
	oldTurns := "{\"sessionId\":\"" + id + "\",\"uuid\":\"old-user\",\"cwd\":\"/repo\",\"timestamp\":\"2026-10-11T10:00:00Z\",\"type\":\"user\",\"message\":{\"content\":\"old prompt\"}}\n" +
		"{\"sessionId\":\"" + id + "\",\"uuid\":\"old-assistant\",\"parentUuid\":\"old-user\",\"cwd\":\"/repo\",\"timestamp\":\"2026-10-11T10:01:00Z\",\"type\":\"assistant\",\"message\":{\"content\":\"old response\"}}\n"
	if err := os.WriteFile(transcript, []byte(oldTurns), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprints, err := syncv4.OpenFingerprints(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vendors.SetParseCache(fingerprints)
	t.Cleanup(func() { vendors.SetParseCache(nil) })
	queue, err := syncv4.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	discoveredActivity := make(chan int64, 8)
	runner := &syncv4.Runner{
		Queue:    queue,
		Backup:   sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir()}),
		Hub:      inventorySyncHub{},
		Discover: func(context.Context) ([]*session.Session, error) { return nil, nil },
	}
	runner.DiscoverBatches = func(ctx context.Context, visit func(syncv4.DiscoveryBatch) error) error {
		for batch, err := range inventory.Discover(ctx, inventory.DiscoverOptions{BatchFamilies: 8}) {
			if err != nil {
				return err
			}
			if err := visit(syncv4.DiscoveryBatch{Sessions: batch.Sessions, ContentBytes: batch.ContentBytes}); err != nil {
				return err
			}
			for _, item := range batch.Sessions {
				if item.Agent == "claude" && item.ID == id {
					discoveredActivity <- item.LastActivityTime
				}
			}
		}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inventoryWake := make(chan struct{}, 1)
	ticks := make(chan struct{})
	scanned := make(chan struct{})
	inventoryDone := make(chan struct{})
	go func() {
		defer close(inventoryDone)
		runInventoryWithTicks(ctx, fingerprints, queue, inventoryWake, &inventory.Tracker{}, func(context.Context) bool { return true }, ticks, scanned)
	}()
	<-scanned
	select {
	case <-inventoryWake: // Discard startup's initial discovery request.
	default:
		t.Fatal("initial inventory did not wake the sync loop")
	}

	controller := &syncController{}
	worker := controller.observed(runner, nil)
	syncDone := make(chan struct{})
	go func() {
		defer close(syncDone)
		runV4SyncLoopForTest(ctx, worker, queue, func(ctx context.Context, _ int64) (hubclient.V4Wait, error) {
			<-ctx.Done()
			return hubclient.V4Wait{}, ctx.Err()
		}, inventoryWake)
	}()
	oldActivity := time.Date(2026, 10, 11, 10, 1, 0, 0, time.UTC).UnixMilli()
	select {
	case got := <-discoveredActivity:
		if got != oldActivity {
			t.Fatalf("initial session activity = %d, want %d", got, oldActivity)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("initial sync did not discover the older session")
	}
	if entries := queue.Entries(); len(entries) != 1 || entries[0].Activity != oldActivity {
		t.Fatalf("initial queue entries = %+v, want the older session at %d", entries, oldActivity)
	}
	initialInventory := queue.Inventory()

	newTurns := "{\"sessionId\":\"" + id + "\",\"uuid\":\"new-user\",\"parentUuid\":\"old-assistant\",\"cwd\":\"/repo\",\"timestamp\":\"2026-10-12T10:00:00Z\",\"type\":\"user\",\"message\":{\"content\":\"new prompt\"}}\n" +
		"{\"sessionId\":\"" + id + "\",\"uuid\":\"new-assistant\",\"parentUuid\":\"new-user\",\"cwd\":\"/repo\",\"timestamp\":\"2026-10-12T10:01:00Z\",\"type\":\"assistant\",\"message\":{\"content\":\"new response\"}}\n"
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
	if current := queue.Inventory(); current == nil || initialInventory == nil || current.Bytes <= initialInventory.Bytes {
		t.Fatalf("inventory after transcript growth = %+v, want more than %+v", current, initialInventory)
	}
	updatedActivity := time.Date(2026, 10, 12, 10, 1, 0, 0, time.UTC).UnixMilli()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case got := <-discoveredActivity:
			if got != updatedActivity {
				continue
			}
			entries := queue.Entries()
			if len(entries) != 1 || entries[0].Activity != updatedActivity {
				t.Fatalf("queue after one changed-source wake = %+v, want activity %d", entries, updatedActivity)
			}
			cancel()
			<-syncDone
			<-inventoryDone
			return
		case <-deadline:
			t.Fatalf("first changed-source inventory wake did not update activity; queue = %+v", queue.Entries())
		}
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
