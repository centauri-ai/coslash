package syncv4

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

type maxHoldHub struct {
	*planHub
	mu        sync.Mutex
	manifest  hubclient.V4Manifest
	confirmed map[string]bool
	puts      int
	finalized int
}

func (h *maxHoldHub) V4Create(_ context.Context, request hubclient.V4Create) (hubclient.V4Status, error) {
	h.manifest = request.Manifest
	return hubclient.V4Status{SessionID: "ses_live", UploadID: "up_live", State: "open"}, nil
}

func (h *maxHoldHub) V4Status(context.Context, string) (hubclient.V4Status, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	status := hubclient.V4Status{SessionID: "ses_live", UploadID: "up_live", State: "open"}
	for _, artifact := range h.manifest.Artifacts {
		for _, chunk := range artifact.Chunks {
			if !h.confirmed[chunk.SHA256] {
				status.Missing = append(status.Missing, hubclient.V4Missing{ArtifactOrdinal: artifact.Ordinal, ChunkOrdinal: chunk.Ordinal, Offset: chunk.Offset, Bytes: chunk.Bytes, SHA256: chunk.SHA256})
			}
		}
	}
	return status, nil
}

func (h *maxHoldHub) V4PutChunk(_ context.Context, _ string, _ hubclient.V4Missing, body io.Reader) error {
	_, err := io.Copy(io.Discard, body)
	h.mu.Lock()
	h.puts++
	h.mu.Unlock()
	return err
}

func (h *maxHoldHub) V4Confirm(_ context.Context, _ string, chunks ...hubclient.V4Missing) (hubclient.V4Status, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, chunk := range chunks {
		h.confirmed[chunk.SHA256] = true
	}
	return hubclient.V4Status{SessionID: "ses_live", UploadID: "up_live", State: "open"}, nil
}

func (h *maxHoldHub) V4Finalize(context.Context, string) (hubclient.V4Status, error) {
	h.finalized++
	return hubclient.V4Status{SessionID: "ses_live", State: "completed", RevisionID: fmt.Sprintf("rev_%d", h.finalized)}, nil
}

func TestReadyLiveMaxHold(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item := Entry{Key: "live", Live: true, Activity: now.UnixMilli(), Session: hubclient.V4Session{Agent: "codex"}}
	if err := q.Merge([]Entry{item}, now); err != nil {
		t.Fatal(err)
	}
	for seconds := 20; seconds <= 600; seconds += 20 {
		at := now.Add(time.Duration(seconds) * time.Second)
		item.Activity = at.UnixMilli()
		if err := q.Merge([]Entry{item}, at); err != nil {
			t.Fatal(err)
		}
		if got := readyLive(q.Entries()[0], at); got != (seconds == 600) {
			t.Fatalf("ready at %ds = %v", seconds, got)
		}
	}
}

func TestReadyLiveBoundariesAndOldState(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item := Entry{Key: "old", Live: true, Activity: now.UnixMilli(), Session: hubclient.V4Session{Agent: "codex"}}
	if err := q.Merge([]Entry{item}, now); err != nil {
		t.Fatal(err)
	}
	entry := q.Entries()[0]
	if entry.LastSentAt != now.UnixMilli() || readyLive(entry, now) || readyLive(entry, now.Add(2*time.Minute-time.Millisecond)) || !readyLive(entry, now.Add(2*time.Minute)) {
		t.Fatalf("new cycle: %+v", entry)
	}
	entry.Live = false
	if !readyLive(entry, now) {
		t.Fatal("ended session held")
	}
}

func TestFrozenReservationDuringPreparation(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item := Entry{Key: "live", Live: true, Activity: now.UnixMilli(), SourceRevision: "one", Session: hubclient.V4Session{Agent: "codex"}}
	if err := q.Merge([]Entry{item}, now); err != nil {
		t.Fatal(err)
	}
	entry := q.Entries()[0]
	if err := q.freeze(&entry); err != nil {
		t.Fatal(err)
	}
	item.Activity++
	item.SourceRevision = "two"
	if err := q.Merge([]Entry{item}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	entry.BundleID = "prepared"
	if err := q.Update(entry); err != nil {
		t.Fatal(err)
	}
	got := q.Entries()[0]
	if got.SourceRevision != "two" || got.BundleID != "prepared" || got.Frozen == nil || len(q.Discards()) != 0 {
		t.Fatalf("reservation: %+v", got)
	}
}

func TestFrozenSourceVerificationFailureRetainsCapture(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item := Entry{Key: "live", Live: true, Activity: now.UnixMilli(), SourceRevision: "one", Session: hubclient.V4Session{Agent: "grok"}, BundleID: "bundle", UploadID: "upload"}
	if err := q.Merge([]Entry{item}, now); err != nil {
		t.Fatal(err)
	}
	entry := q.Entries()[0]
	if err := q.freeze(&entry); err != nil {
		t.Fatal(err)
	}
	item.Activity++
	item.SourceRevision = "two"
	if err := q.Merge([]Entry{item}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	entry = q.Entries()[0]
	stale := entry
	runner := &Runner{Queue: q, Now: func() time.Time { return now }, Version: "test"}
	if err := runner.recordFailureState(&entry, errors.New("source unavailable"), true); err != nil {
		t.Fatal(err)
	}
	got := q.Entries()[0]
	if !got.SourceBlocked || got.BundleID != "bundle" || got.Frozen == nil || len(q.Discards()) != 0 {
		t.Fatalf("blocked capture: %+v", got)
	}
	if err := q.Update(stale); err == nil {
		t.Fatal("stale update cleared the source block")
	}
	got.SourceBlocked, got.ParkedVersion = false, ""
	if err := q.Update(got); err != nil {
		t.Fatal(err)
	}
}

func TestFrozenBundleSurvivesSourceChange(t *testing.T) {
	manager, prepared := artifactLimitBundle(t, 3)
	root := t.TempDir()
	q, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	now := start.Add(10 * time.Minute)
	hub := &resumeScaleHub{planHub: &planHub{}, confirmed: map[[2]int]bool{{0, 0}: true}, attempts: map[[2]int]int{}, failOnce: [2]int{-1, -1}}
	runner := &Runner{Queue: q, Backup: manager, Hub: hub, Now: func() time.Time { return now }, checkedAt: now}
	manifest, err := runner.manifest(prepared)
	if err != nil {
		t.Fatal(err)
	}
	hub.manifest = manifest
	item := Entry{Key: "live", Live: true, Activity: start.UnixMilli(), SourceRevision: "one", Session: hubclient.V4Session{Agent: "codex"}}
	if err := q.Merge([]Entry{item}, start); err != nil {
		t.Fatal(err)
	}
	item.Activity = now.UnixMilli()
	if err := q.Merge([]Entry{item}, now); err != nil {
		t.Fatal(err)
	}
	// Install the upload after the last ordinary source change, then freeze it.
	entry := q.Entries()[0]
	entry.BundleID, entry.Manifest, entry.ContentSHA256, entry.UploadID, entry.SessionID = prepared.BundleID, &manifest, manifest.ContentSHA256, "up_resume", "ses_resume"
	if err := q.Update(entry); err != nil {
		t.Fatal(err)
	}
	if err := q.freeze(&entry); err != nil {
		t.Fatal(err)
	}
	item.Activity++
	item.SourceRevision = "two"
	if err := q.Merge([]Entry{item}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(q.Discards()) != 0 {
		t.Fatalf("frozen bundle discarded: %v", q.Discards())
	}
	if err := q.Update(entry); err != nil {
		t.Fatal(err)
	}
	q, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	runner.Queue = q
	entry = uploadEntry(q.Entries()[0])
	now = now.Add(2 * time.Second)
	runner.checkedAt = now
	if err := runner.transfer(t.Context(), &entry); err != nil {
		t.Fatal(err)
	}
	got := q.Entries()[0]
	if hub.finalized != 1 || got.RevisionID != "rev_once" || got.SourceRevision != "two" || got.SyncedSourceRevision != "one" || !pending(got) || got.LastSentAt != now.UnixMilli() || got.Frozen != nil {
		t.Fatalf("completion: finalized=%d entry=%+v", hub.finalized, got)
	}
	if hub.attempts[[2]int{0, 0}] != 0 || hub.attempts[[2]int{1, 0}] != 1 {
		t.Fatalf("missing chunk dedupe: %v", hub.attempts)
	}
}

func TestFrozenBundleAbandonedOnExclude(t *testing.T) {
	for _, action := range []string{"exclude", "retry", "rebind"} {
		t.Run(action, func(t *testing.T) {
			now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
			q, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := q.Rebind(strings.Repeat("a", 64)); err != nil {
				t.Fatal(err)
			}
			item := Entry{Key: "live", Activity: now.UnixMilli(), Live: true, SourceRevision: "one", Session: hubclient.V4Session{Agent: "codex", Repo: "org/private"}, BundleID: "bundle", UploadID: "upload", SessionID: "ses_live"}
			if err := q.Merge([]Entry{item}, now); err != nil {
				t.Fatal(err)
			}
			entry := q.Entries()[0]
			if err := q.freeze(&entry); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "exclude":
				err = q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{LeaveOut: []string{"org/private"}}}, now)
			case "retry":
				_, err = q.RetrySession("ses_live")
			case "rebind":
				err = q.Rebind(strings.Repeat("b", 64))
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(q.Discards()) != 1 || q.Discards()[0] != "bundle" || q.Matches(entry) {
				t.Fatalf("abandon %s: %+v", action, q.Entries()[0])
			}
			if err := q.Update(entry); err == nil {
				t.Fatal("abandoned upload updated the queue")
			}
			hub := &maxHoldHub{planHub: &planHub{}, confirmed: map[string]bool{}}
			runner := &Runner{Queue: q, Hub: hub, Now: func() time.Time { return now }, checkedAt: now}
			if err := runner.transfer(t.Context(), &entry); !errors.Is(err, ErrPaused) || hub.finalized != 0 {
				t.Fatalf("transfer after abandonment: %v", err)
			}
			if err := runner.complete(&entry, hubclient.V4Status{State: "completed", SessionID: "ses_live", RevisionID: "rev"}); !errors.Is(err, ErrPaused) {
				t.Fatalf("completion: %v", err)
			}
		})
	}
}

func TestMaxHoldContinuousLiveSession(t *testing.T) {
	start := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item := Entry{Key: "live", Live: true, Activity: start.UnixMilli(), SourceRevision: "0", Session: hubclient.V4Session{Agent: "codex"}}
	if err := q.Merge([]Entry{item}, start); err != nil {
		t.Fatal(err)
	}
	now := start
	hub := &maxHoldHub{planHub: &planHub{}, confirmed: map[string]bool{}}
	runner := &Runner{Queue: q, Hub: hub, Now: func() time.Time { return now }}
	var revisions []int
	firstPuts := 0
	for seconds := 20; seconds <= 1200; seconds += 20 {
		now = start.Add(time.Duration(seconds) * time.Second)
		item.Activity, item.SourceRevision = now.UnixMilli(), fmt.Sprint(seconds)
		if err := q.Merge([]Entry{item}, now); err != nil {
			t.Fatal(err)
		}
		entry := q.Entries()[0]
		if !readyLive(entry, now) {
			continue
		}
		manager, prepared, _ := fixtureBundle(t)
		runner.Backup, runner.checkedAt = manager, now
		entry.BundleID = prepared.BundleID
		if err := q.Update(entry); err != nil {
			t.Fatal(err)
		}
		if err := runner.ensureCreated(t.Context(), &entry); err != nil {
			t.Fatal(err)
		}
		if err := runner.transfer(t.Context(), &entry); err != nil {
			t.Fatal(err)
		}
		if seconds == 600 {
			firstPuts = hub.puts
		}
		revisions = append(revisions, seconds)
	}
	if fmt.Sprint(revisions) != "[600 1200]" {
		t.Fatalf("revisions: %v", revisions)
	}
	if hub.finalized != 2 || firstPuts == 0 || hub.puts != firstPuts {
		t.Fatalf("finalized=%d puts=%d first=%d", hub.finalized, hub.puts, firstPuts)
	}
}
