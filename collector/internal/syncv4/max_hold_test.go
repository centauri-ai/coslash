package syncv4

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	"github.com/centauri-ai/coslash/collector/internal/vendors/pi"
	backup "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
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

func TestFrozenRealPreparationAndRestart(t *testing.T) {
	start := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	now := start.Add(10 * time.Minute)
	home := t.TempDir()
	root := filepath.Join(claude.ProjectsRoot(home), "project")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, fixtureRootID+".jsonl")
	first := fmt.Sprintf(`{"sessionId":%q,"type":"user","timestamp":"2026-09-28T11:00:00Z","cwd":%q,"message":{"content":"first"}}`, fixtureRootID, home) + "\n"
	if err := os.WriteFile(file, []byte(first), 0o600); err != nil {
		t.Fatal(err)
	}
	queueRoot := t.TempDir()
	q, err := Open(queueRoot)
	if err != nil {
		t.Fatal(err)
	}
	selection := sessionbackupproducer.Selection{SourceKind: backup.SourceLocal, SourceID: "local", Agent: vendors.AgentClaude, SessionID: fixtureRootID}
	item := Entry{Key: "live", Selection: selection, Live: true, Activity: start.UnixMilli(), SourceRevision: "before", Session: hubclient.V4Session{Agent: vendors.AgentClaude}}
	if err := q.Merge([]Entry{item}, start); err != nil {
		t.Fatal(err)
	}
	item.Activity = now.Add(-20 * time.Second).UnixMilli()
	if err := q.Merge([]Entry{item}, now.Add(-20*time.Second)); err != nil {
		t.Fatal(err)
	}
	manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir(), OpenSource: func(context.Context, sessionbackupproducer.Selection) (sessionbackupproducer.SourceHandle, error) {
		return sessionbackupproducer.SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}, AfterRename: func() {
		later := fmt.Sprintf(`{"sessionId":%q,"type":"user","timestamp":"2026-09-28T11:01:00Z","cwd":%q,"message":{"content":"later-change"}}`, fixtureRootID, home) + "\n"
		if err := os.WriteFile(file, []byte(first+later), 0o600); err != nil {
			t.Fatal(err)
		}
		item.Activity, item.SourceRevision = now.UnixMilli(), "after"
		if err := q.Merge([]Entry{item}, now); err != nil {
			t.Fatal(err)
		}
	}})
	hub := &maxHoldHub{planHub: &planHub{}, confirmed: map[string]bool{}}
	runner := &Runner{Queue: q, Backup: manager, Hub: hub, Now: func() time.Time { return now }, checkedAt: now}
	entry := q.Entries()[0]
	if err := runner.prepareEntry(t.Context(), &entry); err != nil {
		t.Fatal(err)
	}
	stored := q.Entries()[0]
	if stored.Frozen == nil || stored.SourceRevision != "after" || stored.BundleID == "" || len(q.Discards()) != 0 {
		t.Fatalf("prepared state: %+v", stored)
	}
	prepared, err := manager.Open(stored.BundleID)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := manager.Reader(stored.BundleID)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range curatedArtifacts(prepared) {
		body, err := readChunk(reader, artifact.LogicalName, 0, artifact.ByteLength)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "later-change") {
			t.Fatal("prepared bundle included the later source change")
		}
	}
	q, err = Open(queueRoot)
	if err != nil {
		t.Fatal(err)
	}
	runner.Queue = q
	entry = uploadEntry(q.Entries()[0])
	if err := runner.createUpload(t.Context(), &entry); err != nil {
		t.Fatal(err)
	}
	if err := runner.transfer(t.Context(), &entry); err != nil {
		t.Fatal(err)
	}
	stored = q.Entries()[0]
	if hub.finalized != 1 || stored.RevisionID == "" || stored.SyncedSourceRevision != "before" || stored.SourceRevision != "after" || !pending(stored) {
		t.Fatalf("completed state: %+v", stored)
	}
}

func TestFrozenUnpreparedRestartUsesVerifiedNewerSource(t *testing.T) {
	_, file, data := piQueueSource(t)
	revision := func() string {
		t.Helper()
		plan, err := pi.PlanSourcesContext(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		inputs, err := plan.SessionInputsContext(t.Context(), "pi.custom_01")
		if err != nil {
			t.Fatal(err)
		}
		value, err := sessionbackupproducer.PiSourceRevision(t.Context(), inputs)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	start := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	now := start.Add(10 * time.Minute)
	root := t.TempDir()
	q, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	selection := sessionbackupproducer.Selection{SourceKind: backup.SourceLocal, SourceID: "local", Agent: vendors.AgentPi, SessionID: "pi.custom_01"}
	item := Entry{Key: "live", Selection: selection, Live: true, Activity: start.UnixMilli(), SourceRevision: revision(), Session: hubclient.V4Session{Agent: vendors.AgentPi}}
	if err := q.Merge([]Entry{item}, start); err != nil {
		t.Fatal(err)
	}
	item.Activity = now.Add(-time.Second).UnixMilli()
	if err := q.Merge([]Entry{item}, now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	entry := q.Entries()[0]
	if err := q.freeze(&entry); err != nil {
		t.Fatal(err)
	}
	updated := append(data, []byte(`{"type":"message","id":"new","parentId":"b","timestamp":"2026-10-01T12:00:04Z","message":{"role":"user","content":"later"}}`+"\n")...)
	if err := os.WriteFile(file, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	item.Activity, item.SourceRevision = now.UnixMilli(), revision()
	if err := q.Merge([]Entry{item}, now); err != nil {
		t.Fatal(err)
	}
	q, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir()})
	hub := &maxHoldHub{planHub: &planHub{}, confirmed: map[string]bool{}}
	runner := &Runner{Queue: q, Backup: manager, Hub: hub, Now: func() time.Time { return now }, checkedAt: now}
	entry = uploadEntry(q.Entries()[0])
	if err := runner.prepareEntry(t.Context(), &entry); err != nil {
		t.Fatal(err)
	}
	prepared, err := manager.Open(entry.BundleID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Frozen == nil || entry.SourceRevision != item.SourceRevision || prepared.Manifest.Source.SourceRevision != item.SourceRevision {
		t.Fatalf("reprepared entry: %+v", entry)
	}
	if err := runner.createUpload(t.Context(), &entry); err != nil {
		t.Fatal(err)
	}
	if err := runner.transfer(t.Context(), &entry); err != nil {
		t.Fatal(err)
	}
	if got := q.Entries()[0]; hub.finalized != 1 || got.SyncedSourceRevision != item.SourceRevision || pending(got) {
		t.Fatalf("reprepared completion: %+v", got)
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
	data, err := os.ReadFile(filepath.Join(root, "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	var legacy struct {
		Entries []struct {
			Activity       int64  `json:"activity"`
			SourceRevision string `json:"sourceRevision"`
			BundleID       string `json:"bundleId"`
			UploadID       string `json:"uploadId"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatal(err)
	}
	if len(legacy.Entries) != 1 || legacy.Entries[0].Activity != entry.Activity || legacy.Entries[0].SourceRevision != "one" || legacy.Entries[0].BundleID != entry.BundleID || legacy.Entries[0].UploadID != entry.UploadID {
		t.Fatalf("older reader sees mismatched bundle identity: %+v", legacy.Entries)
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
