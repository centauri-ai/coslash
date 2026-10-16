package syncv4

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

type planHub struct {
	plan         *hubclient.V4ImportPlan
	withoutScale bool
	creates      int
	lists        [][]hubclient.V4ListItem
	checks       int
	checkInQueue []hubclient.V4Queue
	checkInLogs  [][]hubclient.V4LogEntry
	checkInErr   error
	results      []hubclient.V4CommandResult
	retryLists   int
	leaveOut     []string
}

type asyncCompletionHub struct {
	*planHub
	statuses int
}

type rateLimitedCreateHub struct {
	*planHub
	statuses  int
	finalized int
}

type idempotencyConflictHub struct {
	*planHub
	creates map[string]int
}

func (h *idempotencyConflictHub) V4Create(_ context.Context, request hubclient.V4Create) (hubclient.V4Status, error) {
	key := request.Session.LocalKeyHash
	h.creates[key]++
	if key == "conflict" {
		return hubclient.V4Status{}, hubclient.V4Problem{Code: "idempotency_conflict", HTTPStatus: 409}
	}
	return hubclient.V4Status{SessionID: "ses_" + key, State: "completed", RevisionID: "revision_" + key}, nil
}

type freshDuringPassHub struct {
	*planHub
	onCreate func() error
}

func (h *freshDuringPassHub) V4Create(context.Context, hubclient.V4Create) (hubclient.V4Status, error) {
	h.creates++
	if h.onCreate != nil {
		if err := h.onCreate(); err != nil {
			return hubclient.V4Status{}, err
		}
	}
	return hubclient.V4Status{}, hubclient.V4Problem{Code: "temporary_unavailable", HTTPStatus: 503}
}

type deadlineCreateHub struct {
	*planHub
}

type tooLargeCreateHub struct {
	*planHub
}

func managerCancelledDuringPrepare(t *testing.T) (*sessionbackupproducer.Manager, <-chan struct{}) {
	t.Helper()
	started := make(chan struct{})
	manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir(), OpenSource: func(ctx context.Context, _ sessionbackupproducer.Selection) (sessionbackupproducer.SourceHandle, error) {
		close(started)
		<-ctx.Done()
		return sessionbackupproducer.SourceHandle{}, ctx.Err()
	}})
	return manager, started
}

func (h *deadlineCreateHub) V4Create(context.Context, hubclient.V4Create) (hubclient.V4Status, error) {
	h.creates++
	return hubclient.V4Status{}, context.DeadlineExceeded
}

func (h *tooLargeCreateHub) V4Create(context.Context, hubclient.V4Create) (hubclient.V4Status, error) {
	h.creates++
	return hubclient.V4Status{}, hubclient.V4Problem{Code: "too_large", HTTPStatus: 413}
}

func TestPlannedContentYieldsForFreshDiscovery(t *testing.T) {
	started := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	now := started
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, prepared := artifactLimitBundle(t, 1)
	plan := hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
		t.Fatal(err)
	}
	hub := &freshDuringPassHub{planHub: &planHub{plan: &plan}}
	runner := &Runner{Queue: q, Backup: manager, Hub: hub, Now: func() time.Time { return now }, checkedAt: now,
		scaleEnabled: true, config: hubclient.V4Config{ImportPlan: &plan}, lastReportedPhase: "recent"}
	manifest, err := runner.manifest(prepared)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"old-1", "old-2"} {
		if err := q.Merge([]Entry{{Key: key, Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: key},
			Activity: now.UnixMilli(), Listed: true, BundleID: prepared.BundleID, Manifest: &manifest,
			ContentSHA256: manifest.ContentSHA256}}, now); err != nil {
			t.Fatal(err)
		}
	}
	hub.onCreate = func() error {
		now = now.Add(plannedContentBudget + time.Second)
		return q.Merge([]Entry{{Key: "fresh", Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: "fresh"},
			Activity: now.UnixMilli()}}, now)
	}
	if err := runner.syncPlannedContent(t.Context(), plan); err == nil {
		t.Fatal("expected first upload error")
	}
	if hub.creates != 1 {
		t.Fatalf("content pass did not yield after budget: %d creates", hub.creates)
	}
	if err := runner.listAll(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	for _, entry := range q.Entries() {
		if entry.Key == "fresh" && entry.Listed {
			return
		}
	}
	t.Fatal("fresh session was not listed on the next boundary within the budget")
}

func TestPlannedContentCancellationDuringPrepareLeavesEntryPending(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
		t.Fatal(err)
	}
	queue.SetActiveSince(now)
	entry := Entry{Key: "cancelled", SourceRevision: "source-1", Selection: sessionbackupproducer.Selection{
		SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: "codex", SessionID: "cancelled",
	}, Session: hubclient.V4Session{InstallID: queue.InstallID(), Agent: "codex", LocalKeyHash: "cancelled"}, Activity: now.UnixMilli(), Listed: true}
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	manager, started := managerCancelledDuringPrepare(t)
	runner := &Runner{Queue: queue, Backup: manager, Now: func() time.Time { return now }, checkedAt: now,
		config: hubclient.V4Config{ImportPlan: &plan}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- runner.syncPlannedContent(ctx, plan) }()
	<-started
	cancel()
	_ = <-done

	got := queue.Entries()[0]
	if got.FailureCode != "" || !pending(got) {
		t.Fatalf("cancelled preparation changed pending entry: %+v", got)
	}
	if logs, _ := queue.PendingLog(now); len(logs) != 0 {
		t.Fatalf("cancelled preparation queued Hub log lines: %+v", logs)
	}
}

func TestWarmStartCancellationDuringPrepareLeavesEntryPending(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
		t.Fatal(err)
	}
	queue.SetActiveSince(now)
	entry := Entry{Key: "cancelled", SourceRevision: "source-1", Selection: sessionbackupproducer.Selection{
		SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: "codex", SessionID: "cancelled",
	}, Session: hubclient.V4Session{InstallID: queue.InstallID(), Agent: "codex", LocalKeyHash: "cancelled"}, Activity: now.UnixMilli()}
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	manager, started := managerCancelledDuringPrepare(t)
	runner := &Runner{Queue: queue, Backup: manager, Now: func() time.Time { return now }, checkedAt: now}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- runner.warmStart(ctx, plan, now, time.Minute, 0) }()
	<-started
	cancel()
	_ = <-done

	got := queue.Entries()[0]
	if got.FailureCode != "" || !pending(got) {
		t.Fatalf("cancelled preparation changed pending entry: %+v", got)
	}
	if logs, _ := queue.PendingLog(now); len(logs) != 0 {
		t.Fatalf("cancelled preparation queued Hub log lines: %+v", logs)
	}
}

func TestPlannedSyncReportsParkedListedFailureAndProgressPromptly(t *testing.T) {
	t.Setenv("COSLASH_SCALE_IMPORT", "1")
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.SetPhase("awaiting_plan"); err != nil {
		t.Fatal(err)
	}
	if err := queue.Merge([]Entry{{Key: "unreadable", Activity: now.UnixMilli(), SourceRevision: "source-1",
		Session: hubclient.V4Session{Agent: "claude"}}}, now); err != nil {
		t.Fatal(err)
	}
	entry := queue.Entries()[0]
	hub := &planHub{}
	runner := &Runner{Queue: queue, Hub: hub, Now: func() time.Time { return now }, Version: "0.0.5",
		scaleSupported: true, lastReportedPhase: "awaiting_plan"}
	if err := runner.recordFailure(&entry, preparationProblem(sessionbackupv1.ProblemInvalid, false)); err != nil {
		t.Fatal(err)
	}
	if err := queue.MarkListedAt([]hubclient.V4ListResult{{LocalKeyHash: entry.Key, SessionID: "ses_unreadable", State: "listed"}}, now); err != nil {
		t.Fatal(err)
	}

	if err := runner.runPlannedAndReport(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hub.checks != 1 || len(hub.checkInQueue) != 1 || len(hub.checkInLogs) != 1 {
		t.Fatalf("check-ins=%d queue=%d logs=%d", hub.checks, len(hub.checkInQueue), len(hub.checkInLogs))
	}
	progress := hub.checkInQueue[0]
	if progress.Pending != 0 || progress.Failing != 1 || progress.FirstSync.RecentDone != 1 || progress.FirstSync.RecentTotal != 1 {
		t.Fatalf("reported queue progress=%+v", progress)
	}
	lines := hub.checkInLogs[0]
	if len(lines) != 2 || lines[0].SessionID != "" || lines[1].SessionID != "ses_unreadable" || lines[1].Code != "malformed_artifact" {
		t.Fatalf("reported failure lines=%+v", lines)
	}
	if pending, _ := queue.PendingLog(now); len(pending) != 0 {
		t.Fatalf("successful same-pass check-in left pending logs: %+v", pending)
	}
}

func TestPlannedCheckInErrorTakesPrecedenceOverUploadError(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, prepared, _ := fixtureBundle(t)
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: plan}}, now); err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "too-large", Activity: now.UnixMilli(), Listed: true, BundleID: prepared.BundleID,
		Selection: prepared.Selection, Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: "too-large"}}
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	hub := &tooLargeCreateHub{planHub: &planHub{plan: plan, checkInErr: hubclient.V4Problem{Code: "unauthorized", HTTPStatus: 401}}}
	runner := &Runner{Queue: queue, Backup: manager, Hub: hub, Now: func() time.Time { return now }, checkedAt: now,
		scaleEnabled: true, scaleSupported: true, config: hubclient.V4Config{ImportPlan: plan}, lastReportedPhase: "recent"}

	err = runner.runPlannedAndReport(t.Context())
	var problem hubclient.V4Problem
	if !errors.As(err, &problem) || problem.Code != "unauthorized" || DeferReason(err) != "rejected" || !strings.Contains(err.Error(), "too_large") {
		t.Fatalf("sync error = %v, defer reason = %q; want check-in unauthorized", err, DeferReason(err))
	}
}

func TestPrioritizedRetryKeepsPlannedPhaseIncomplete(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := hubclient.V4ImportPlan{Version: 1, Window: "3d"}
	entry := Entry{Key: "prioritized", Activity: now.Add(-10 * 24 * time.Hour).UnixMilli(), Priority: true,
		RetryAt: now.Add(time.Minute), Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: "prioritized"}}
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
		t.Fatal(err)
	}
	if err := queue.FreezeCatchUp(plan); err != nil {
		t.Fatal(err)
	}
	if err := queue.SetPhase("complete"); err != nil {
		t.Fatal(err)
	}
	progress := queue.Progress().FirstSync
	snapshot := queue.ImportSnapshot(now)
	if progress.RecentDone != progress.RecentTotal || progress.HistoryState != "off" || snapshot.ContentSessions != 0 || snapshot.TotalSessions != 1 {
		t.Fatalf("test setup did not isolate prioritized work: progress=%+v import=%+v", progress, snapshot)
	}
	runner := &Runner{Queue: queue, Now: func() time.Time { return now }, config: hubclient.V4Config{ImportPlan: &plan}}
	if err := runner.syncPlannedContent(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if phase, _, _ := queue.Phase(); phase != "history" || !runner.ImportActive() {
		t.Fatalf("phase = %q, active = %t; want unfinished prioritized work", phase, runner.ImportActive())
	}
	entry = queue.Entries()[0]
	entry.ParkedVersion = "0.0.5"
	if err := queue.Update(entry); err != nil {
		t.Fatal(err)
	}
	if err := runner.syncPlannedContent(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if phase, _, _ := queue.Phase(); phase != "complete" || runner.ImportActive() {
		t.Fatalf("parked phase = %q, active = %t; want settled work", phase, runner.ImportActive())
	}
}

func TestPrioritizedListRejectedEntryDoesNotKeepHistoryActive(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := hubclient.V4ImportPlan{Version: 1, Window: "3d", History: false}
	entry := Entry{Key: "rejected", Activity: now.Add(-10 * 24 * time.Hour).UnixMilli(), Priority: true,
		Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: "rejected"}}
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
		t.Fatal(err)
	}
	if err := queue.MarkListedAt([]hubclient.V4ListResult{{LocalKeyHash: entry.Key, State: "rejected", Code: "invalid_item"}}, now); err != nil {
		t.Fatal(err)
	}
	if err := queue.FreezeCatchUp(plan); err != nil {
		t.Fatal(err)
	}
	if err := queue.SetPhase("recent"); err != nil {
		t.Fatal(err)
	}

	runner := &Runner{Queue: queue, Now: func() time.Time { return now }}
	if err := runner.syncPlannedContent(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if phase, _, _ := queue.Phase(); phase != "complete" || runner.ImportActive() {
		t.Fatalf("phase = %q, active = %t; want settled work", phase, runner.ImportActive())
	}
}

func TestPlannedIdempotencyConflictParksOneRevisionAndContinuesQueue(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, prepared, _ := fixtureBundle(t)
	plan := hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
		t.Fatal(err)
	}
	q.SetActiveSince(now)
	hub := &idempotencyConflictHub{planHub: &planHub{plan: &plan}, creates: map[string]int{}}
	runner := &Runner{Queue: q, Backup: manager, Hub: hub, Now: func() time.Time { return now }, checkedAt: now,
		scaleEnabled: true, config: hubclient.V4Config{ImportPlan: &plan}, Version: "0.0.5"}
	manifest, err := runner.manifest(prepared)
	if err != nil {
		t.Fatal(err)
	}
	contentBytes := int64(0)
	for _, artifact := range manifest.Artifacts {
		contentBytes += artifact.Bytes
	}
	makeEntry := func(key string) Entry {
		return Entry{Key: key, Selection: prepared.Selection, Session: hubclient.V4Session{InstallID: q.InstallID(), Agent: "codex", LocalKeyHash: key},
			Activity: now.UnixMilli(), SourceRevision: "source-" + key, SessionID: "ses_" + key, Listed: true,
			BundleID: prepared.BundleID, Manifest: &manifest, ContentSHA256: manifest.ContentSHA256, ContentBytes: contentBytes}
	}
	if err := q.Merge([]Entry{makeEntry("conflict"), makeEntry("healthy")}, now); err != nil {
		t.Fatal(err)
	}
	var problem hubclient.V4Problem
	if err := runner.syncPlannedContent(t.Context(), plan); !errors.As(err, &problem) || problem.Code != "idempotency_conflict" {
		t.Fatalf("first pass error = %v, want idempotency_conflict", err)
	}
	entries := map[string]Entry{}
	for _, entry := range q.Entries() {
		entries[entry.Key] = entry
	}
	conflicted, healthy := entries["conflict"], entries["healthy"]
	if conflicted.ParkedVersion != runner.Version || conflicted.FailureCode != "idempotency_conflict" || conflicted.BundleID != "" ||
		healthy.RevisionID == "" || healthy.FailureCode != "" || hub.creates["conflict"] != 1 || hub.creates["healthy"] != 1 {
		t.Fatalf("queue after first pass conflict=%+v healthy=%+v creates=%v", conflicted, healthy, hub.creates)
	}
	if err := runner.syncPlannedContent(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if err := runner.syncPlannedContent(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if hub.creates["conflict"] != 1 || hub.creates["healthy"] != 1 {
		t.Fatalf("parked revision retried or healthy session reopened: creates=%v", hub.creates)
	}
}

func TestPlannedContentClassifiesHubDeadlineAsServerError(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, prepared := artifactLimitBundle(t, 1)
	plan := hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
		t.Fatal(err)
	}
	hub := &deadlineCreateHub{planHub: &planHub{plan: &plan}}
	runner := &Runner{Queue: q, Backup: manager, Hub: hub, Now: func() time.Time { return now }, checkedAt: now,
		scaleEnabled: true, config: hubclient.V4Config{ImportPlan: &plan}, lastReportedPhase: "recent"}
	manifest, err := runner.manifest(prepared)
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "timed-out-create", Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: "timed-out-create"},
		Activity: now.UnixMilli(), Listed: true, BundleID: prepared.BundleID, Manifest: &manifest, ContentSHA256: manifest.ContentSHA256}
	if err := q.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	if err := runner.syncPlannedContent(t.Context(), plan); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sync error = %v, want Hub deadline", err)
	}
	if got := q.Entries()[0].FailureCode; got != "server_error" {
		t.Fatalf("failure code = %q, want server_error", got)
	}
}

func (h *rateLimitedCreateHub) V4Create(context.Context, hubclient.V4Create) (hubclient.V4Status, error) {
	h.creates++
	return hubclient.V4Status{}, hubclient.V4Problem{Code: "rate_limited", HTTPStatus: 429}
}

func (h *rateLimitedCreateHub) V4Status(_ context.Context, uploadID string) (hubclient.V4Status, error) {
	h.statuses++
	return hubclient.V4Status{UploadID: uploadID, SessionID: "ses_open", State: "open"}, nil
}

func (h *rateLimitedCreateHub) V4Confirm(_ context.Context, uploadID string, _ ...hubclient.V4Missing) (hubclient.V4Status, error) {
	return hubclient.V4Status{UploadID: uploadID, SessionID: "ses_open", State: "open"}, nil
}

func (h *rateLimitedCreateHub) V4Finalize(_ context.Context, uploadID string) (hubclient.V4Status, error) {
	h.finalized++
	return hubclient.V4Status{UploadID: uploadID, SessionID: "ses_open", State: "finalizing"}, nil
}

func TestPlannedPassAdvancesOpenUploadWhileCreatesRateLimited(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, prepared := artifactLimitBundle(t, 1)
	plan := hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
		t.Fatal(err)
	}
	hub := &rateLimitedCreateHub{planHub: &planHub{plan: &plan}}
	runner := &Runner{Queue: q, Backup: manager, Hub: hub, Now: func() time.Time { return now }, checkedAt: now,
		scaleEnabled: true, config: hubclient.V4Config{ImportPlan: &plan}, lastReportedPhase: "recent"}
	manifest, err := runner.manifest(prepared)
	if err != nil {
		t.Fatal(err)
	}
	entries := []Entry{
		{Key: "new", Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: "new"}, Activity: now.UnixMilli(),
			Listed: true, BundleID: prepared.BundleID, Manifest: &manifest, ContentSHA256: manifest.ContentSHA256},
		{Key: "open", Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: "open"}, Activity: now.Add(-time.Hour).UnixMilli(),
			Listed: true, BundleID: prepared.BundleID, Manifest: &manifest, ContentSHA256: manifest.ContentSHA256,
			UploadID: "up_open", SessionID: "ses_open"},
	}
	if err := q.Merge(entries, now); err != nil {
		t.Fatal(err)
	}
	if err := runner.syncPlannedContent(t.Context(), plan); !Busy(err) {
		t.Fatalf("expected create rate limit, got %v", err)
	}
	if hub.statuses != 1 || hub.finalized != 1 || hub.creates != 1 {
		t.Fatalf("statuses=%d finalized=%d creates=%d", hub.statuses, hub.finalized, hub.creates)
	}
}

func (h *asyncCompletionHub) V4Status(_ context.Context, uploadID string) (hubclient.V4Status, error) {
	h.statuses++
	return hubclient.V4Status{UploadID: uploadID, SessionID: "ses_async", State: "completed", RevisionID: "rev_async"}, nil
}

func TestPlannedPassReconcilesAsyncFinalizeBeforeNextCreate(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
	manager, prepared := artifactLimitBundle(t, 1)
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "async", Recent: true, Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: "async"},
		Activity: now.Add(-time.Hour).UnixMilli(), Listed: true, UploadID: "up_async", SessionID: "ses_async", BundleID: prepared.BundleID}
	if err := q.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	if err := q.FreezeCatchUp(plan); err != nil {
		t.Fatal(err)
	}
	entry = q.Entries()[0]
	hub := &asyncCompletionHub{planHub: &planHub{plan: &plan}}
	runner := &Runner{Queue: q, Backup: manager,
		Hub: hub, Now: func() time.Time { return now }, checkedAt: now, scaleEnabled: true,
		config: hubclient.V4Config{ImportPlan: &plan}, lastReportedPhase: "recent"}
	if err := runner.syncPlannedContent(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if hub.statuses != 1 || hub.creates != 0 {
		t.Fatalf("statuses=%d creates=%d", hub.statuses, hub.creates)
	}
	progress := q.Progress()
	snapshot := q.ImportSnapshot(now)
	if progress.Pending != 0 || snapshot.ContentSessions != 1 {
		t.Fatalf("queue=%+v import=%+v", progress, snapshot)
	}
}

func (*planHub) V4Binding(context.Context) (string, error) { return strings.Repeat("a", 64), nil }
func (h *planHub) V4CheckIn(_ context.Context, queue hubclient.V4Queue, _ int64, results []hubclient.V4CommandResult, _ []string, log []hubclient.V4LogEntry) (hubclient.V4CheckIn, error) {
	h.checks++
	h.checkInQueue = append(h.checkInQueue, queue)
	h.checkInLogs = append(h.checkInLogs, append([]hubclient.V4LogEntry(nil), log...))
	h.results = append([]hubclient.V4CommandResult(nil), results...)
	if h.checkInErr != nil {
		return hubclient.V4CheckIn{}, h.checkInErr
	}
	rules := h.leaveOut
	if rules == nil {
		rules = []string{}
	}
	capabilities := []string{"scale-import/v1"}
	if h.withoutScale {
		capabilities = nil
	}
	return hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{LeaveOut: rules, ImportPlan: h.plan}, Capabilities: capabilities}, nil
}
func (h *planHub) V4Create(context.Context, hubclient.V4Create) (hubclient.V4Status, error) {
	h.creates++
	return hubclient.V4Status{}, fmt.Errorf("unexpected create")
}
func (*planHub) V4Status(context.Context, string) (hubclient.V4Status, error) {
	return hubclient.V4Status{}, fmt.Errorf("unexpected status")
}
func (*planHub) V4PutChunk(context.Context, string, hubclient.V4Missing, io.Reader) error {
	return fmt.Errorf("unexpected chunk")
}
func (*planHub) V4Confirm(context.Context, string, ...hubclient.V4Missing) (hubclient.V4Status, error) {
	return hubclient.V4Status{}, fmt.Errorf("unexpected confirm")
}
func (*planHub) V4Finalize(context.Context, string) (hubclient.V4Status, error) {
	return hubclient.V4Status{}, fmt.Errorf("unexpected finalize")
}
func (h *planHub) V4ListBatch(_ context.Context, items []hubclient.V4ListItem) ([]hubclient.V4ListResult, error) {
	h.lists = append(h.lists, append([]hubclient.V4ListItem(nil), items...))
	if h.retryLists > 0 {
		h.retryLists--
		return nil, hubclient.V4Problem{Code: "temporary_unavailable", HTTPStatus: 503}
	}
	results := make([]hubclient.V4ListResult, len(items))
	for i, item := range items {
		results[i] = hubclient.V4ListResult{LocalKeyHash: item.LocalKeyHash, SessionID: "ses_" + item.LocalKeyHash, State: "listed"}
	}
	return results, nil
}

func TestScaleHubWithoutPlanCreatesOrListsNothing(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Merge([]Entry{{Key: "pending", Activity: now.UnixMilli(), Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: "pending"}}}, now); err != nil {
		t.Fatal(err)
	}
	hub := &planHub{}
	runner := &Runner{Queue: q, Backup: sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir()}), Hub: hub,
		Discover: func(context.Context) ([]*session.Session, error) { return nil, nil }, Now: func() time.Time { return now }}
	if err := runner.SyncOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hub.creates != 0 || len(hub.lists) != 0 || q.ImportSnapshot(now).Phase != "awaiting_plan" {
		t.Fatalf("creates=%d lists=%d phase=%s", hub.creates, len(hub.lists), q.ImportSnapshot(now).Phase)
	}
}

func TestMergePlannedDiscoveryCountsOnlyPendingInScopeEntries(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := hubclient.V4ImportPlan{Version: 1, Window: "all"}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
		t.Fatal(err)
	}
	listed := Entry{Key: "listed", Listed: true, Activity: now.Add(time.Minute).UnixMilli(), Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: "listed"}}
	if err := q.Merge([]Entry{listed}, now); err != nil {
		t.Fatal(err)
	}
	newEntry := Entry{Key: "new", Activity: now.Add(time.Minute).UnixMilli(), Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: "new"}}
	oldEntry := Entry{Key: "old", Activity: now.Add(-24 * time.Hour).UnixMilli(), Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: "old"}}
	count, err := q.MergePlannedDiscovery([]Entry{newEntry, listed, oldEntry}, now, plan, now)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("pending listing count = %d, want 1", count)
	}
}

func TestListedSessionWithoutServerRevisionClearsLocalCompletion(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	plan := &hubclient.V4ImportPlan{Version: 2, Window: "3d", MaxSessionsPerAgent: 30}
	root := t.TempDir()
	q, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	manager, prepared, _ := fixtureBundle(t)
	activity := now.Add(-time.Hour).UnixMilli()
	entry := Entry{Key: "synthetic-claude-key", Activity: activity, SyncedActivity: activity, Listed: true,
		SourceRevision: "synthetic-source-revision", SyncedSourceRevision: "synthetic-source-revision",
		RevisionID: "synthetic-previous-revision", SessionID: "ses_synthetic_selected",
		BundleID: prepared.BundleID,
		Session:  hubclient.V4Session{InstallID: q.InstallID(), LocalKeyHash: "synthetic-claude-key", Agent: "claude"}}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 2, Config: hubclient.V4Config{ImportPlan: plan}}, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := q.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	if err := q.FreezeCatchUp(*plan); err != nil {
		t.Fatal(err)
	}
	if progress, snapshot := q.Progress(), q.ImportSnapshot(now); progress.Pending != 0 ||
		snapshot.TotalSessions != 1 || snapshot.ContentSessions != 1 {
		t.Fatalf("synthetic persisted state did not match the completed Local: queue=%+v import=%+v", progress, snapshot)
	}
	if err := q.SetPhase("complete"); err != nil {
		t.Fatal(err)
	}
	q, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	hub := &planHub{plan: plan}
	runner := &Runner{Queue: q, Backup: manager, Hub: hub, Now: func() time.Time { return now }, checkedAt: now,
		scaleEnabled: true, config: hubclient.V4Config{ImportPlan: plan}}
	if err := runner.runPlanned(t.Context()); err == nil {
		t.Fatal("expected the synthetic Hub to reject upload creation")
	}
	progress := q.Progress()
	snapshot := q.ImportSnapshot(now)
	phase, _, _ := q.Phase()
	entry = q.Entries()[0]
	if len(hub.lists) != 1 || hub.creates != 1 || phase == "complete" || progress.Pending != 1 ||
		snapshot.ContentSessions != 0 || snapshot.TotalSessions != 1 || entry.RevisionID != "" || !entry.Listed {
		t.Fatalf("listed session was not reconciled: lists=%d creates=%d phase=%q queue=%+v import=%+v entry=%+v",
			len(hub.lists), hub.creates, phase, progress, snapshot, entry)
	}
}

func TestNewImportPlanVersionInvalidatesListingState(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prior := &hubclient.V4ImportPlan{Version: 1, Window: "3d"}
	next := &hubclient.V4ImportPlan{Version: 2, Window: "3d"}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: prior}}, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "synthetic-key", Listed: true, SessionID: "ses_old", Activity: now.UnixMilli(),
		Session: hubclient.V4Session{LocalKeyHash: "synthetic-key", Agent: "claude"}}
	if err := q.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 2, Config: hubclient.V4Config{ImportPlan: next}}, now); err != nil {
		t.Fatal(err)
	}
	if got := q.Entries()[0]; got.Listed {
		t.Fatalf("new plan retained old listing confirmation: %+v", got)
	}
}

func TestExistingHubRevisionPreservesLocalCompletion(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	activity := now.Add(-time.Hour).UnixMilli()
	entry := Entry{Key: "synthetic-key", Activity: activity, SyncedActivity: activity,
		SourceRevision: "synthetic-source-revision", SyncedSourceRevision: "synthetic-source-revision",
		RevisionID: "synthetic-accepted-revision",
		Session:    hubclient.V4Session{LocalKeyHash: "synthetic-key", Agent: "claude"}}
	if err := q.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkListedAt([]hubclient.V4ListResult{{LocalKeyHash: "synthetic-key", SessionID: "ses_existing", State: "existing"}}, now); err != nil {
		t.Fatal(err)
	}
	got := q.Entries()[0]
	if pending(got) || got.RevisionID != "synthetic-accepted-revision" || got.SessionID != "ses_existing" {
		t.Fatalf("existing Hub revision changed local completion: %+v", got)
	}
}

func TestChunkScalingEnabledHonorsKillSwitchAndPlan(t *testing.T) {
	plan := hubclient.V4ImportPlan{Version: 1, Window: "all"}
	runner := &Runner{scaleEnabled: true, config: hubclient.V4Config{ImportPlan: &plan}}
	t.Setenv("COSLASH_SCALE_IMPORT", "0")
	if runner.chunkScalingEnabled() {
		t.Fatal("chunk scaling enabled with kill switch off")
	}
	t.Setenv("COSLASH_SCALE_IMPORT", "1")
	runner.scaleEnabled = false
	if !runner.chunkScalingEnabled() {
		t.Fatal("plan-based chunk scaling disabled without Hub capability")
	}
	runner.config.ImportPlan = nil
	if runner.chunkScalingEnabled() {
		t.Fatal("chunk scaling enabled without capability or import plan")
	}
}

func TestDefaultWorkerDoesNotUploadToHubWithoutImportPlanSupport(t *testing.T) {
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hub := &planHub{withoutScale: true}
	runner := &Runner{Queue: q, Backup: sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir()}), Hub: hub,
		Discover:          func(context.Context) ([]*session.Session, error) { t.Fatal("unplanned discovery"); return nil, nil },
		RequireImportPlan: true}
	if err := runner.SyncOnce(t.Context()); !errors.Is(err, ErrPaused) {
		t.Fatalf("sync without import plan support = %v, want paused", err)
	}
	if hub.creates != 0 || len(hub.lists) != 0 || hub.checks != 1 {
		t.Fatalf("creates=%d lists=%d checks=%d", hub.creates, len(hub.lists), hub.checks)
	}
}

func TestScaleDiscoveryMergesEachYieldBeforeTheNext(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hub := &planHub{}
	runner := &Runner{Queue: q, Backup: sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir()}), Hub: hub,
		Discover: func(context.Context) ([]*session.Session, error) { t.Fatal("full discovery used"); return nil, nil },
		Now:      func() time.Time { return now }}
	runner.DiscoverBatches = func(_ context.Context, emit func(DiscoveryBatch) error) error {
		for i := 0; i < 2; i++ {
			item := &session.Session{Agent: "codex", ID: fmt.Sprintf("session-%d", i), LastActivityTime: now.Add(-time.Duration(i) * time.Hour).UnixMilli()}
			if err := emit(DiscoveryBatch{Sessions: []*session.Session{item}, ContentBytes: map[string]int64{"codex\x00" + item.ID: int64(i + 1)}}); err != nil {
				return err
			}
			if got := len(q.Entries()); got != i+1 {
				t.Fatalf("entries after yield %d = %d", i, got)
			}
			if got := q.Entries()[0].ContentBytes; got != 1 {
				t.Fatalf("first byte estimate after yield %d = %d", i, got)
			}
		}
		return nil
	}
	if err := runner.SyncOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hub.creates != 0 || len(hub.lists) != 0 {
		t.Fatalf("unplanned streamed discovery wrote to Hub: creates=%d lists=%d", hub.creates, len(hub.lists))
	}
}

func TestRequestedDiscoveryBypassesCooldown(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hub := &planHub{}
	discoveries := 0
	interrupted := errors.New("discovery interrupted")
	runner := &Runner{
		Queue:    q,
		Backup:   sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir()}),
		Hub:      hub,
		Discover: func(context.Context) ([]*session.Session, error) { return nil, nil },
		Now:      func() time.Time { return now },
	}
	runner.DiscoverBatches = func(context.Context, func(DiscoveryBatch) error) error {
		discoveries++
		if discoveries == 2 {
			return interrupted
		}
		return nil
	}
	if err := runner.SyncOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := runner.SyncOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if discoveries != 1 {
		t.Fatalf("discovery calls before request = %d, want 1", discoveries)
	}
	runner.RequestDiscovery()
	if err := runner.SyncOnce(t.Context()); !errors.Is(err, interrupted) {
		t.Fatalf("requested discovery error = %v, want interrupted scan", err)
	}
	if err := runner.SyncOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if discoveries != 3 {
		t.Fatalf("discovery calls after request and retry = %d, want 3", discoveries)
	}
}

func TestScaleQueueRetainsPreviousReaderVersion(t *testing.T) {
	root := t.TempDir()
	q, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "3d", History: true, WarmStartSeconds: 30}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: plan}}, now); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	var previous struct {
		Version   int    `json:"version"`
		InstallID string `json:"installId"`
	}
	if err := json.Unmarshal(data, &previous); err != nil || previous.Version != 1 || previous.InstallID == "" {
		t.Fatalf("previous reader: %+v, %v", previous, err)
	}
	var current struct {
		ScaleVersion int `json:"scaleVersion"`
	}
	if err := json.Unmarshal(data, &current); err != nil || current.ScaleVersion != 1 {
		t.Fatalf("scale version: %+v, %v", current, err)
	}
	if _, err := Open(root); err != nil {
		t.Fatalf("new reader: %v", err)
	}
}

func TestEmptyPlanMovesThroughListingToComplete(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "3d", History: true}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: plan}}, now); err != nil {
		t.Fatal(err)
	}
	hub := &planHub{plan: plan}
	runner := &Runner{Queue: q, Hub: hub, Now: func() time.Time { return now }, checkedAt: now, config: hubclient.V4Config{ImportPlan: plan}}
	if err := runner.runPlanned(t.Context()); err != nil {
		t.Fatal(err)
	}
	if phase, _, _ := q.Phase(); phase != "complete" {
		t.Fatalf("phase = %s", phase)
	}
}

func TestLiveResyncDoesNotBlockCatchUpHTTP(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessionsPerAgent: 30}
	policy := hubclient.V4CheckIn{ConfigVersion: 1, MinVersion: "0.0.3",
		Capabilities: []string{hubclient.CapabilityScaleImport},
		Config:       hubclient.V4Config{ImportPlan: &plan, LeaveOut: []string{}}}
	if err := queue.ApplyPolicyAt(policy, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "live", Live: true, Listed: true, Activity: now.Add(-time.Minute).UnixMilli(),
		SyncedActivity: now.Add(-time.Minute).UnixMilli(), RevisionID: "accepted", SessionID: "ses_live",
		Session: hubclient.V4Session{InstallID: queue.InstallID(), LocalKeyHash: "live", Agent: "codex"}}
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	if err := queue.FreezeCatchUp(plan); err != nil {
		t.Fatal(err)
	}
	if err := queue.SetPhase("recent"); err != nil {
		t.Fatal(err)
	}
	pass, creates, reports := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if pass > 0 && r.URL.Path != "/v4/devices/me/check-in" {
			if phase, _, _ := queue.Phase(); phase != "complete" {
				t.Errorf("phase during re-sync %s = %q, want complete", r.URL.Path, phase)
			}
		}
		switch r.URL.Path {
		case "/v4/sessions:list-batch":
			io.WriteString(w, `{"results":[{"localKeyHash":"live","sessionId":"ses_live","state":"existing"}]}`)
		case "/v4/uploads":
			creates++
			io.WriteString(w, `{"sessionId":"ses_live","state":"completed","revisionId":"updated"}`)
		case "/v4/devices/me/check-in":
			var request struct{ Queue hubclient.V4Queue }
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			reports++
			progress := request.Queue
			if progress.Import == nil || progress.Import.Phase != "complete" ||
				progress.FirstSync.RecentTotal != 1 || progress.FirstSync.RecentDone != 1 || progress.FirstSync.HistoryState != "off" {
				t.Errorf("pass %d reported incomplete catch-up: %+v import=%+v", pass, progress, progress.Import)
			}
			json.NewEncoder(w).Encode(policy)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	runner := &Runner{Queue: queue, Now: func() time.Time { return now }, checkedAt: now,
		scaleSupported: true, config: policy.Config,
		Hub: &hubclient.Client{BaseURL: base, Credentials: fixedCredential{}, CollectorVersion: "0.0.5"}}
	for pass = 0; pass < 3; pass++ {
		if pass < 2 {
			now = now.Add(time.Second)
			entry.Activity = now.UnixMilli()
			if err := queue.Merge([]Entry{entry}, now); err != nil {
				t.Fatal(err)
			}
			if got := queue.Entries()[0]; got.RevisionID != "" || got.Listed || got.ChangedAt != now.UnixMilli() {
				t.Fatalf("new live activity did not invalidate revision: %+v", got)
			}
		} else {
			now = now.Add(2 * time.Minute)
			manager, prepared, _ := fixtureBundle(t)
			runner.Backup = manager
			quiet := queue.Entries()[0]
			quiet.BundleID = prepared.BundleID
			if err := queue.Update(quiet); err != nil {
				t.Fatal(err)
			}
		}
		runner.checkedAt = now
		if err := runner.runPlanned(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := runner.refreshConsent(t.Context()); err != nil {
			t.Fatal(err)
		}
		wantPending := 1
		if pass == 2 {
			wantPending = 0
		}
		if pending := queue.Progress().Pending; pending != wantPending {
			t.Fatalf("pass %d pending uploads = %d", pass, pending)
		}
	}
	if creates != 1 || reports != 3 {
		t.Fatalf("creates=%d reports=%d, want 1 upload and 3 reports", creates, reports)
	}
}

func TestNewSessionAfterCompleteHTTP(t *testing.T) {
	for _, live := range []bool{true, false} {
		t.Run(fmt.Sprintf("live=%t", live), func(t *testing.T) {
			now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
			queue, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			plan := hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessionsPerAgent: 30}
			policy := hubclient.V4CheckIn{ConfigVersion: 1, MinVersion: "0.0.3", Capabilities: []string{hubclient.CapabilityScaleImport},
				Config: hubclient.V4Config{ImportPlan: &plan, LeaveOut: []string{}}}
			if err := queue.ApplyPolicyAt(policy, now); err != nil {
				t.Fatal(err)
			}
			if err := queue.FreezeCatchUp(plan); err != nil {
				t.Fatal(err)
			}
			if err := queue.SetPhase("complete"); err != nil {
				t.Fatal(err)
			}
			queue.SetActiveSince(now)
			creates, reports := 0, 0
			quiet, reporting := false, false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v4/sessions:list-batch":
					io.WriteString(w, `{"results":[{"localKeyHash":"new","sessionId":"ses_new","state":"listed"}]}`)
				case "/v4/uploads":
					creates++
					if !quiet {
						t.Error("uploaded before quiet period or retry elapsed")
					}
					io.WriteString(w, `{"sessionId":"ses_new","state":"completed","revisionId":"accepted"}`)
				case "/v4/devices/me/check-in":
					var request struct{ Queue hubclient.V4Queue }
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if !reporting {
						json.NewEncoder(w).Encode(policy)
						return
					}
					reports++
					wantPhase, wantDone := "complete", 1
					if !live && !quiet {
						wantPhase, wantDone = "recent", 0
					}
					progress := request.Queue
					if progress.Import == nil || progress.Import.Phase != wantPhase ||
						progress.FirstSync.RecentTotal != 1 || progress.FirstSync.RecentDone != wantDone {
						t.Errorf("report %d: want phase=%s done=%d/1, got firstSync=%+v import=%+v", reports, wantPhase, wantDone, progress.FirstSync, progress.Import)
					}
					json.NewEncoder(w).Encode(policy)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			base, _ := url.Parse(server.URL)
			runner := &Runner{Queue: queue, Now: func() time.Time { return now }, config: policy.Config,
				scaleSupported: true, Hub: &hubclient.Client{BaseURL: base, Credentials: fixedCredential{}, CollectorVersion: "0.0.5"}}
			entry := Entry{Key: "new", Live: live,
				Session: hubclient.V4Session{InstallID: queue.InstallID(), LocalKeyHash: "new", Agent: "codex"}}
			for pass := 0; pass < 13; pass++ {
				now = now.Add(time.Minute)
				entry.Activity = now.UnixMilli()
				if err := queue.Merge([]Entry{entry}, now); err != nil {
					t.Fatal(err)
				}
				if !live {
					waiting := queue.Entries()[0]
					waiting.RetryAt = now.Add(time.Hour)
					if err := queue.Update(waiting); err != nil {
						t.Fatal(err)
					}
				}
				runner.checkedAt = now
				if err := runner.runPlanned(t.Context()); err != nil {
					t.Fatal(err)
				}
				reporting = true
				if err := runner.refreshConsent(t.Context()); err != nil {
					t.Fatal(err)
				}
				reporting = false
			}
			if live {
				for _, elapsed := range []time.Duration{2*time.Minute - time.Millisecond, 2 * time.Minute} {
					wantDone := 1
					if elapsed == 2*time.Minute {
						wantDone = 0
					}
					if progress := queue.progressAt(now.Add(elapsed)); progress.FirstSync.RecentDone != wantDone || progress.Pending != 1 {
						t.Fatalf("progress at quiet boundary %s = %+v", elapsed, progress)
					}
				}
			}
			quiet = true
			now = now.Add(2 * time.Minute)
			manager, prepared, _ := fixtureBundle(t)
			runner.Backup = manager
			waiting := queue.Entries()[0]
			waiting.BundleID, waiting.RetryAt = prepared.BundleID, time.Time{}
			if err := queue.Update(waiting); err != nil {
				t.Fatal(err)
			}
			runner.checkedAt = now
			if err := runner.runPlanned(t.Context()); err != nil {
				t.Fatal(err)
			}
			reporting = true
			if err := runner.refreshConsent(t.Context()); err != nil {
				t.Fatal(err)
			}
			if creates != 1 || reports != 14 || queue.Progress().Pending != 0 {
				t.Fatalf("creates=%d reports=%d pending=%d", creates, reports, queue.Progress().Pending)
			}
		})
	}
}

func TestRestartKeepsSettledCatchUpCompleteOnHTTPFailure(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		entry   Entry
		content bool
	}{
		{name: "live", entry: Entry{Live: true, SyncedActivity: now.Add(-time.Minute).UnixMilli()}},
		{name: "parked", entry: Entry{ParkedVersion: "0.0.5"}},
		{name: "rejected", entry: Entry{ListRejected: true}},
		{name: "session_deleted", entry: Entry{FailureCode: "session_deleted"}},
		{name: "session_deleted_content", entry: Entry{FailureCode: "session_deleted"}, content: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			queue, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			plan := hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessionsPerAgent: 30}
			config := hubclient.V4Config{ImportPlan: &plan}
			if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: config}, now.Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			entry := test.entry
			entry.Key, entry.Activity, entry.Listed = "settled", now.UnixMilli(), true
			entry.Session = hubclient.V4Session{InstallID: queue.InstallID(), LocalKeyHash: entry.Key, Agent: "codex"}
			if err := queue.Merge([]Entry{entry}, now); err != nil {
				t.Fatal(err)
			}
			if err := queue.FreezeCatchUp(plan); err != nil {
				t.Fatal(err)
			}
			if err := queue.SetPhase("complete"); err != nil {
				t.Fatal(err)
			}
			queue, err = Open(root)
			if err != nil {
				t.Fatal(err)
			}
			if entry := queue.Entries()[0]; entry.Listed || !caughtUp(entry) || !pending(entry) {
				t.Fatalf("restart did not retain settled pending entry: %+v", entry)
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				path := "/v4/sessions:list-batch"
				if test.content {
					path = "/v4/uploads"
				}
				if r.Method != http.MethodPost || r.URL.Path != path {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(http.StatusGone)
				io.WriteString(w, `{"code":"session_deleted"}`)
			}))
			defer server.Close()
			base, _ := url.Parse(server.URL)
			runner := &Runner{Queue: queue, Now: func() time.Time { return now }, checkedAt: now, config: config,
				Hub: &hubclient.Client{BaseURL: base, Credentials: fixedCredential{}}}
			if test.content {
				manager, prepared, _ := fixtureBundle(t)
				runner.Backup = manager
				entry = queue.Entries()[0]
				entry.BundleID = prepared.BundleID
				if err := queue.Update(entry); err != nil {
					t.Fatal(err)
				}
				err = runner.syncPlannedContent(t.Context(), plan)
			} else {
				err = runner.runPlanned(t.Context())
			}
			wantRequests := 1
			if test.entry.ListRejected {
				wantRequests = 0
			}
			var problem hubclient.V4Problem
			if requests != wantRequests || wantRequests > 0 && (!errors.As(err, &problem) || problem.Code != "session_deleted") || wantRequests == 0 && err != nil {
				t.Fatalf("requests=%d error=%v", requests, err)
			}
			if phase, _, _ := queue.Phase(); phase != "complete" {
				t.Errorf("phase after restart = %q, want complete", phase)
			}
			if progress := queue.Progress().FirstSync; progress.RecentTotal != 1 || progress.RecentDone != progress.RecentTotal {
				t.Errorf("catch-up after restart = %+v", progress)
			}
		})
	}
}

func TestPlannedContentCompletesRecentPhaseWhenHistoryOffAndFailuresParked(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := hubclient.V4ImportPlan{Version: 1, Window: "3d", History: false, MaxSessionsPerAgent: 30}
	activity := now.UnixMilli()
	entries := []Entry{
		{Key: "synced", Activity: activity, RevisionID: "revision-synced", SyncedActivity: activity,
			Session: hubclient.V4Session{InstallID: q.InstallID(), LocalKeyHash: "synced", Agent: "codex"}},
		{Key: "parked", Activity: activity, ParkedVersion: "0.0.5", FailureCode: "unreadable_source",
			Session: hubclient.V4Session{InstallID: q.InstallID(), LocalKeyHash: "parked", Agent: "codex"}},
		{Key: "rejected", Activity: activity,
			Session: hubclient.V4Session{InstallID: q.InstallID(), LocalKeyHash: "rejected", Agent: "codex"}},
		{Key: "deleted", Activity: activity, FailureCode: "session_deleted", RetryAt: now.Add(time.Hour),
			Session: hubclient.V4Session{InstallID: q.InstallID(), LocalKeyHash: "deleted", Agent: "codex"}},
	}
	if err := q.Merge(entries, now); err != nil {
		t.Fatal(err)
	}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
		t.Fatal(err)
	}
	q.SetActiveSince(now)
	if err := q.MarkListedAt([]hubclient.V4ListResult{{LocalKeyHash: "rejected", State: "rejected", Code: "invalid_item"}}, now); err != nil {
		t.Fatal(err)
	}
	if err := q.FreezeCatchUp(plan); err != nil {
		t.Fatal(err)
	}
	if err := q.SetPhase("recent"); err != nil {
		t.Fatal(err)
	}
	progress := q.Progress().FirstSync
	if progress.RecentDone != 4 || progress.RecentTotal != 4 || progress.HistoryState != "off" {
		t.Fatalf("settled live-only progress = %+v", progress)
	}

	runner := &Runner{Queue: q, Now: func() time.Time { return now }}
	if err := runner.syncPlannedContent(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if phase, _, _ := q.Phase(); phase != "complete" {
		t.Fatalf("phase = %s, want complete", phase)
	}
}

type warmOrderHub struct {
	*planHub
	events []string
}

func (h *warmOrderHub) V4Create(_ context.Context, _ hubclient.V4Create) (hubclient.V4Status, error) {
	h.events = append(h.events, "create")
	return hubclient.V4Status{SessionID: "ses_first", State: "completed", RevisionID: "rev_first"}, nil
}

func (h *warmOrderHub) V4ListBatch(ctx context.Context, items []hubclient.V4ListItem) ([]hubclient.V4ListResult, error) {
	h.events = append(h.events, "list")
	results, err := h.planHub.V4ListBatch(ctx, items)
	if err != nil {
		return nil, err
	}
	for i, item := range items {
		if item.LocalKeyHash == "first" {
			results[i].State = "existing"
		}
	}
	return results, nil
}

func TestWarmSessionCompletesBeforeMetadataListing(t *testing.T) {
	manager, prepared, _ := fixtureBundle(t)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := Entry{Key: "first", Activity: now.UnixMilli(), ContentBytes: 1024, BundleID: prepared.BundleID,
		Selection: prepared.Selection, Session: hubclient.V4Session{InstallID: q.InstallID(), LocalKeyHash: "first", Agent: "codex", Title: "First"}}
	second := Entry{Key: "second", Activity: now.Add(-time.Hour).UnixMilli(), ContentBytes: 30 << 20, ParkedVersion: "unavailable",
		Session: hubclient.V4Session{InstallID: q.InstallID(), LocalKeyHash: "second", Agent: "codex", Title: "Second"}}
	if err := q.Merge([]Entry{first, second}, now); err != nil {
		t.Fatal(err)
	}
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "3d", History: true, WarmStartSeconds: 30}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: plan}}, now); err != nil {
		t.Fatal(err)
	}
	hub := &warmOrderHub{planHub: &planHub{plan: plan}}
	runner := &Runner{Queue: q, Backup: manager, Hub: hub, Now: func() time.Time { return now }, checkedAt: now,
		config: hubclient.V4Config{ImportPlan: plan}}
	if err := runner.runPlanned(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(hub.events) != 2 || hub.events[0] != "create" || hub.events[1] != "list" {
		t.Fatalf("warm/list order = %v", hub.events)
	}
	if snapshot := q.ImportSnapshot(now); snapshot.ContentSessions != 1 || snapshot.Listed != 2 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestPlanWindowListingBatchesAndPrioritize(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	plan := hubclient.V4ImportPlan{Version: 3, Window: "24h", History: true, WarmStartSeconds: 30}
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]Entry, 121)
	for i := range entries {
		key := fmt.Sprintf("%03d", i)
		activity := now.Add(-time.Duration(i) * time.Hour).UnixMilli()
		entries[i] = Entry{Key: key, Activity: activity, ContentBytes: int64(i + 1),
			Session: hubclient.V4Session{InstallID: q.InstallID(), LocalKeyHash: key, Agent: "codex", Title: key, Repo: "repo/allowed"}}
	}
	entries[120].Session.Repo = "repo/secret"
	if err := q.Merge(entries, now); err != nil {
		t.Fatal(err)
	}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{LeaveOut: []string{"repo/secret"}, ImportPlan: &plan}}, now); err != nil {
		t.Fatal(err)
	}
	hub := &planHub{plan: &plan, retryLists: 1}
	runner := &Runner{Queue: q, Hub: hub, Now: func() time.Time { return now }, checkedAt: now, config: hubclient.V4Config{ImportPlan: &plan}}
	if err := runner.listAll(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if len(hub.lists) != 4 {
		t.Fatalf("list requests = %d", len(hub.lists))
	}
	if got := []int{len(hub.lists[0]), len(hub.lists[1]), len(hub.lists[2]), len(hub.lists[3])}; got[0] != 50 || got[1] != 50 || got[2] != 50 || got[3] != 20 {
		t.Fatalf("batch sizes = %v", got)
	}
	if snapshot := q.ImportSnapshot(now); snapshot.Listed != 120 || snapshot.TotalSessions != 120 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	for _, batch := range hub.lists {
		for _, item := range batch {
			if item.LocalKeyHash == "120" || item.ContentBytes < 1 || item.ActivityAt.IsZero() || item.InstallID == "" {
				t.Fatalf("invalid listing item: %+v", item)
			}
		}
	}
	if ok, err := q.Prioritize("ses_119"); err != nil || !ok {
		t.Fatalf("prioritize: %v, %v", ok, err)
	}
	if first := q.PlannedEntries(plan, now)[0].Key; first != "119" {
		t.Fatalf("first scheduled = %s", first)
	}
	plan.History = false
	if got := len(q.PlannedEntries(plan, now)); got != 26 {
		t.Fatalf("24h scope = %d", got)
	}
}

// rejectingHub answers 400 invalid_query for any batch that contains the
// poisoned key, the way the Hub rejects a batch whose item fails the schema.
type rejectingHub struct {
	planHub
	poison string
}

func (h *rejectingHub) V4ListBatch(ctx context.Context, items []hubclient.V4ListItem) ([]hubclient.V4ListResult, error) {
	for _, item := range items {
		if item.LocalKeyHash == h.poison {
			h.lists = append(h.lists, append([]hubclient.V4ListItem(nil), items...))
			return nil, hubclient.V4Problem{Code: "invalid_query", HTTPStatus: 400}
		}
	}
	return h.planHub.V4ListBatch(ctx, items)
}

// One item the Hub refuses must not block the other 119 or make the next
// pass resend the same rejected batch: the runner isolates it and records it
// as rejected with a closed code.
func TestListingIsolatesAnItemTheHubRejects(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	plan := hubclient.V4ImportPlan{Version: 3, Window: "24h", History: true, WarmStartSeconds: 30}
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]Entry, 120)
	for i := range entries {
		key := fmt.Sprintf("%03d", i)
		entries[i] = Entry{Key: key, Activity: now.Add(-time.Duration(i) * time.Hour).UnixMilli(), ContentBytes: int64(i + 1),
			Session: hubclient.V4Session{InstallID: q.InstallID(), LocalKeyHash: key, Agent: "codex", Title: key}}
	}
	if err := q.Merge(entries, now); err != nil {
		t.Fatal(err)
	}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
		t.Fatal(err)
	}
	hub := &rejectingHub{planHub: planHub{plan: &plan}, poison: "007"}
	runner := &Runner{Queue: q, Hub: hub, Now: func() time.Time { return now }, checkedAt: now, config: hubclient.V4Config{ImportPlan: &plan}}
	if err := runner.listAll(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	listed, rejected := 0, 0
	for _, entry := range q.PlannedEntries(plan, now) {
		switch {
		case entry.Key == "007":
			if !entry.ListRejected || entry.FailureCode != "invalid_item" || entry.Listed {
				t.Fatalf("poisoned entry = %+v", entry)
			}
			rejected++
		case entry.Listed:
			listed++
		}
	}
	if listed != 119 || rejected != 1 {
		t.Fatalf("listed=%d rejected=%d", listed, rejected)
	}
	if err := runner.listAll(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	// The second pass must not resend the rejected item.
	for _, batch := range hub.lists[len(hub.lists)-1:] {
		for _, item := range batch {
			if item.LocalKeyHash == "007" {
				t.Fatal("rejected item was sent again")
			}
		}
	}
	if len(q.PlannedEntries(plan, now)) != 120 {
		t.Fatalf("entries = %d", len(q.PlannedEntries(plan, now)))
	}
}

func TestSessionMetadataClampsNegativeTokens(t *testing.T) {
	item := &session.Session{ID: "s1", Agent: "opencode", Tokens: map[string]session.ModelTokens{"claude-sonnet-4-5": {InputTokens: -4096, OutputTokens: 180}}}
	if meta := sessionMetadata(item, "30000000-0000-4000-8000-000000000305"); meta.Tokens != 0 {
		t.Fatalf("tokens = %d, want 0", meta.Tokens)
	}
}

func TestListingLeftOutPersistsUntilPolicyChanges(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "key", Activity: now.UnixMilli(), Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: "key"}}
	if err := q.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "all"}
	policy := hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: plan}}
	if err := q.ApplyPolicyAt(policy, now); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkListedAt([]hubclient.V4ListResult{{LocalKeyHash: "key", State: "left_out"}}, now); err != nil {
		t.Fatal(err)
	}
	if !q.Entries()[0].ServerLeftOut || !q.Entries()[0].Excluded {
		t.Fatal("server leave-out was not persisted")
	}
	if err := q.ApplyPolicyAt(policy, now); err != nil {
		t.Fatal(err)
	}
	if !q.Entries()[0].Excluded {
		t.Fatal("unchanged policy cleared server leave-out")
	}
	policy.ConfigVersion = 2
	if err := q.ApplyPolicyAt(policy, now); err != nil {
		t.Fatal(err)
	}
	if q.Entries()[0].Excluded {
		t.Fatal("new policy did not reconsider server leave-out")
	}
}

func TestRetrySupersedesAnInFlightEntryCopy(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "retry", SessionID: "ses_retry", UploadID: "up_old", Activity: now.UnixMilli(),
		Session: hubclient.V4Session{Agent: "codex"}}
	if err := q.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	copy := q.Entries()[0]
	if !q.Matches(copy) {
		t.Fatal("fresh copy did not match")
	}
	if ok, err := q.RetrySession("ses_retry"); err != nil || !ok {
		t.Fatalf("retry: %v, %v", ok, err)
	}
	if q.Matches(copy) {
		t.Fatal("stale in-flight copy still matched after retry")
	}
	if retried := q.Entries()[0]; !retried.Priority || retried.UploadID != "" || retried.BundleID != "" {
		t.Fatalf("retry was not queued for a fresh priority upload: %+v", retried)
	}
}

func TestListingFiveThousandSessionsUsesOneHundredBatches(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]Entry, 5000)
	for i := range entries {
		key := fmt.Sprintf("%064x", i+1)
		entries[i] = Entry{Key: key, Activity: now.Add(-time.Duration(i) * time.Minute).UnixMilli(), ContentBytes: 4096,
			Session: hubclient.V4Session{InstallID: q.InstallID(), LocalKeyHash: key, Agent: "codex", Title: "Session"}}
	}
	if err := q.Merge(entries, now); err != nil {
		t.Fatal(err)
	}
	plan := hubclient.V4ImportPlan{Version: 1, Window: "3d", History: true}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
		t.Fatal(err)
	}
	hub := &planHub{plan: &plan}
	runner := &Runner{Queue: q, Hub: hub, Now: func() time.Time { return now }, checkedAt: now, config: hubclient.V4Config{ImportPlan: &plan}}
	if err := runner.listAll(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if len(hub.lists) != 100 || q.ImportSnapshot(now).Listed != 5000 {
		t.Fatalf("batches=%d listed=%d", len(hub.lists), q.ImportSnapshot(now).Listed)
	}
}

func TestWarmBudgetAndLiveDebounce(t *testing.T) {
	for _, tc := range []struct {
		bytes int64
		first bool
		want  bool
	}{{20 << 20, true, true}, {26 << 20, true, false}, {20 << 20, false, true}, {80 << 20, false, false}} {
		if got := warmFits(Entry{ContentBytes: tc.bytes}, 10*time.Second, 2_500_000, tc.first); got != tc.want {
			t.Errorf("warmFits(%d, %v) = %v", tc.bytes, tc.first, got)
		}
	}
	now := time.Now()
	entry := Entry{Live: true, ChangedAt: now.Add(-119 * time.Second).UnixMilli()}
	if readyLive(entry, now) || !readyLive(entry, now.Add(time.Second)) {
		t.Fatal("live session debounce boundary changed")
	}
}

func TestConsentRefreshesBeforeLaterBatch(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hub := &planHub{}
	runner := &Runner{Queue: q, Hub: hub, Now: func() time.Time { return now }, checkedAt: now.Add(-4 * time.Minute)}
	if err := runner.ensureConsent(t.Context()); err != nil || hub.checks != 2 {
		t.Fatalf("refresh and capability handshake: %v, checks=%d", err, hub.checks)
	}
	now = now.Add(3 * time.Minute)
	if err := runner.ensureConsent(t.Context()); err != nil || hub.checks != 2 {
		t.Fatalf("early refresh: %v, checks=%d", err, hub.checks)
	}
	now = now.Add(time.Minute)
	if err := runner.ensureConsent(t.Context()); err != nil || hub.checks != 3 {
		t.Fatalf("second refresh: %v, checks=%d", err, hub.checks)
	}
}

func TestActiveImportRefreshesAtTenSecondsAndOnPhaseChange(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "all", WarmStartSeconds: 30}
	hub := &planHub{plan: plan}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: plan}}, now); err != nil {
		t.Fatal(err)
	}
	if err := q.SetPhase("warm_start"); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Queue: q, Hub: hub, Now: func() time.Time { return now },
		checkedAt: now, scaleEnabled: true, scaleSupported: true, lastReportedPhase: "warm_start"}
	now = now.Add(9 * time.Second)
	if err := runner.ensureConsent(t.Context()); err != nil || hub.checks != 0 {
		t.Fatalf("before cadence: %v, checks=%d", err, hub.checks)
	}
	now = now.Add(time.Second)
	if err := runner.ensureConsent(t.Context()); err != nil || hub.checks != 1 {
		t.Fatalf("ten-second refresh: %v, checks=%d", err, hub.checks)
	}
	if err := q.SetPhase("listing"); err != nil {
		t.Fatal(err)
	}
	if err := runner.ensureConsent(t.Context()); err != nil || hub.checks != 2 {
		t.Fatalf("phase refresh: %v, checks=%d", err, hub.checks)
	}
}

func TestListingRechecksLeaveOutAfterConsentRefresh(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "key", Activity: now.UnixMilli(), ContentBytes: 100,
		Session: hubclient.V4Session{InstallID: q.InstallID(), LocalKeyHash: "key", Agent: "codex", Title: "Secret", Repo: "repo/secret"}}
	if err := q.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 0, Config: hubclient.V4Config{ImportPlan: plan}}, now); err != nil {
		t.Fatal(err)
	}
	hub := &planHub{plan: plan, leaveOut: []string{"repo/secret"}}
	runner := &Runner{Queue: q, Hub: hub, Now: func() time.Time { return now }, checkedAt: now.Add(-4 * time.Minute),
		config: hubclient.V4Config{ImportPlan: plan}}
	if err := runner.listAll(t.Context(), *plan); err != nil {
		t.Fatal(err)
	}
	if len(hub.lists) != 0 || !q.Entries()[0].Excluded {
		t.Fatalf("stale listing escaped new leave-out: calls=%d entry=%+v", len(hub.lists), q.Entries()[0])
	}
}

type parallelHub struct {
	*planHub
	active atomic.Int32
	peak   atomic.Int32
	gate   chan struct{}
	once   sync.Once
}

func (h *parallelHub) V4PutChunk(ctx context.Context, _ string, _ hubclient.V4Missing, body io.Reader) error {
	if _, err := io.Copy(io.Discard, body); err != nil {
		return err
	}
	active := h.active.Add(1)
	for {
		peak := h.peak.Load()
		if peak >= active || h.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	if active == 4 {
		h.once.Do(func() { close(h.gate) })
	}
	select {
	case <-h.gate:
	case <-ctx.Done():
		h.active.Add(-1)
		return ctx.Err()
	}
	h.active.Add(-1)
	return nil
}

func TestScaleChunkUploadRunsFourInParallel(t *testing.T) {
	manager, prepared := artifactLimitBundle(t, 4)
	reader, err := manager.Reader(prepared.BundleID)
	if err != nil {
		t.Fatal(err)
	}
	hub := &parallelHub{planHub: &planHub{}, gate: make(chan struct{})}
	runner := &Runner{Backup: manager, Hub: hub, scaleEnabled: true, chunkWorkers: 4}
	curated := curatedArtifacts(prepared)
	body := make([]byte, 1)
	if n, err := reader.Read(curated[0].LogicalName, 0, body); err != nil || n != len(body) {
		t.Fatalf("read synthetic record byte: %d, %v", n, err)
	}
	sum := sha256.Sum256(body)
	jobs := make([]chunkJob, 4)
	for i := range jobs {
		jobs[i] = chunkJob{missing: hubclient.V4Missing{ArtifactOrdinal: 0, ChunkOrdinal: i, Bytes: 1, SHA256: hex.EncodeToString(sum[:])},
			name: curated[0].LogicalName}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sent, err := runner.putChunkGroup(ctx, reader, "up_parallel", jobs)
	if err != nil || len(sent) != 4 || hub.peak.Load() != 4 {
		t.Fatalf("parallel put: sent=%d peak=%d err=%v", len(sent), hub.peak.Load(), err)
	}
}

type resumeScaleHub struct {
	*planHub
	mu        sync.Mutex
	manifest  hubclient.V4Manifest
	confirmed map[[2]int]bool
	attempts  map[[2]int]int
	failOnce  [2]int
	finalized int
}

type bandwidthHub struct {
	*resumeScaleHub
	clockMu sync.Mutex
	clock   time.Time
}

func (h *bandwidthHub) Now() time.Time {
	h.clockMu.Lock()
	defer h.clockMu.Unlock()
	return h.clock
}

func (h *bandwidthHub) V4Create(context.Context, hubclient.V4Create) (hubclient.V4Status, error) {
	h.creates++
	return hubclient.V4Status{UploadID: "up_resume", SessionID: "ses_resume", State: "open"}, nil
}

func (h *bandwidthHub) V4ListBatch(ctx context.Context, items []hubclient.V4ListItem) ([]hubclient.V4ListResult, error) {
	results, err := h.resumeScaleHub.planHub.V4ListBatch(ctx, items)
	if err != nil {
		return nil, err
	}
	if h.resumeScaleHub.finalized > 0 {
		for i, item := range items {
			if item.LocalKeyHash == "first" {
				results[i].State = "existing"
			}
		}
	}
	return results, nil
}

func (h *bandwidthHub) V4PutChunk(_ context.Context, _ string, missing hubclient.V4Missing, body io.Reader) error {
	n, err := io.Copy(io.Discard, body)
	if err != nil || n != missing.Bytes {
		return fmt.Errorf("chunk bytes = %d, want %d: %v", n, missing.Bytes, err)
	}
	h.clockMu.Lock()
	h.clock = h.clock.Add(time.Duration(float64(n) * 8 / 20_000_000 * float64(time.Second)))
	h.clockMu.Unlock()
	return nil
}

func (h *resumeScaleHub) V4Status(context.Context, string) (hubclient.V4Status, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	status := hubclient.V4Status{UploadID: "up_resume", SessionID: "ses_resume", State: "open"}
	for _, artifact := range h.manifest.Artifacts {
		for _, chunk := range artifact.Chunks {
			key := [2]int{artifact.Ordinal, chunk.Ordinal}
			if !h.confirmed[key] {
				status.Missing = append(status.Missing, hubclient.V4Missing{ArtifactOrdinal: artifact.Ordinal,
					ChunkOrdinal: chunk.Ordinal, Offset: chunk.Offset, Bytes: chunk.Bytes, SHA256: chunk.SHA256})
			}
		}
	}
	return status, nil
}

func (h *resumeScaleHub) V4PutChunk(_ context.Context, _ string, missing hubclient.V4Missing, body io.Reader) error {
	if _, err := io.Copy(io.Discard, body); err != nil {
		return err
	}
	key := [2]int{missing.ArtifactOrdinal, missing.ChunkOrdinal}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.attempts[key]++
	if key == h.failOnce && h.attempts[key] == 1 {
		return hubclient.V4Problem{Code: "hash_mismatch"}
	}
	return nil
}

func (h *resumeScaleHub) V4Confirm(_ context.Context, _ string, chunks ...hubclient.V4Missing) (hubclient.V4Status, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, item := range chunks {
		h.confirmed[[2]int{item.ArtifactOrdinal, item.ChunkOrdinal}] = true
	}
	return hubclient.V4Status{UploadID: "up_resume", SessionID: "ses_resume", State: "open"}, nil
}

func (h *resumeScaleHub) V4Finalize(ctx context.Context, uploadID string) (hubclient.V4Status, error) {
	status, _ := h.V4Status(ctx, uploadID)
	if len(status.Missing) != 0 {
		return status, fmt.Errorf("finalized with missing chunks")
	}
	h.mu.Lock()
	h.finalized++
	h.mu.Unlock()
	return hubclient.V4Status{UploadID: "up_resume", SessionID: "ses_resume", State: "completed", RevisionID: "rev_once"}, nil
}

func TestScaleResumeOnlySendsMissingChunks(t *testing.T) {
	manager, prepared := artifactLimitBundle(t, 4)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
	hub := &resumeScaleHub{planHub: &planHub{plan: plan}, confirmed: map[[2]int]bool{}, attempts: map[[2]int]int{}, failOnce: [2]int{1, 0}}
	runner := &Runner{Queue: q, Backup: manager, Hub: hub, Now: func() time.Time { return now }, checkedAt: now, scaleEnabled: true, chunkWorkers: 4, lastReportedPhase: "awaiting_plan",
		config: hubclient.V4Config{ImportPlan: plan}}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: runner.config}, now); err != nil {
		t.Fatal(err)
	}
	manifest, err := runner.manifest(prepared)
	if err != nil {
		t.Fatal(err)
	}
	hub.manifest = manifest
	entry := Entry{Key: "resume", Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: "resume"}, Activity: now.UnixMilli(),
		BundleID: prepared.BundleID, Manifest: &manifest, ContentSHA256: manifest.ContentSHA256, UploadID: "up_resume", SessionID: "ses_resume"}
	if err := q.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Capabilities: []string{"scale-import/v1"}, Config: hubclient.V4Config{ImportPlan: plan}}, now); err != nil {
		t.Fatal(err)
	}
	if err := q.FreezeCatchUp(*plan); err != nil {
		t.Fatal(err)
	}
	entry = q.Entries()[0]
	firstErr := runner.transfer(t.Context(), &entry)
	if firstErr == nil {
		t.Fatal("first parallel pass should stop on the injected chunk failure")
	}
	hub.mu.Lock()
	firstConfirmed := make(map[[2]int]bool, len(hub.confirmed))
	for key := range hub.confirmed {
		firstConfirmed[key] = true
	}
	hub.mu.Unlock()
	if len(firstConfirmed) == 0 {
		t.Fatalf("successful chunks were not confirmed before interruption: %v", firstErr)
	}
	if err := runner.transfer(t.Context(), &entry); err != nil {
		t.Fatal(err)
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for key := range firstConfirmed {
		if hub.attempts[key] != 1 {
			t.Errorf("confirmed chunk %v was resent %d times", key, hub.attempts[key])
		}
	}
	if hub.finalized != 1 {
		t.Fatalf("finalizations = %d", hub.finalized)
	}
}

func TestWarmStartAtTwentyMegabitsFinalizesBeforeListing(t *testing.T) {
	manager, prepared, _ := fixtureBundle(t)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := &resumeScaleHub{planHub: &planHub{}, confirmed: map[[2]int]bool{}, attempts: map[[2]int]int{}}
	hub := &bandwidthHub{resumeScaleHub: base, clock: now}
	runner := &Runner{Queue: q, Backup: manager, Hub: hub, Now: hub.Now, checkedAt: now, scaleEnabled: true}
	manifest, err := runner.manifest(prepared)
	if err != nil {
		t.Fatal(err)
	}
	base.manifest = manifest
	var contentBytes int64
	for _, artifact := range manifest.Artifacts {
		contentBytes += artifact.Bytes
	}
	first := Entry{Key: "first", Activity: now.UnixMilli(), ContentBytes: contentBytes, BundleID: prepared.BundleID,
		Manifest: &manifest, ContentSHA256: manifest.ContentSHA256, Selection: prepared.Selection,
		Session: hubclient.V4Session{InstallID: q.InstallID(), LocalKeyHash: "first", Agent: "codex", Title: "First"}}
	second := Entry{Key: "second", Activity: now.Add(-time.Hour).UnixMilli(), ContentBytes: 30 << 20, ParkedVersion: "unavailable",
		Session: hubclient.V4Session{InstallID: q.InstallID(), LocalKeyHash: "second", Agent: "codex", Title: "Second"}}
	if err := q.Merge([]Entry{first, second}, now); err != nil {
		t.Fatal(err)
	}
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "3d", History: true, WarmStartSeconds: 30}
	base.plan = plan
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: plan}}, now); err != nil {
		t.Fatal(err)
	}
	runner.config.ImportPlan = plan
	if err := runner.runPlanned(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hub.Now().Sub(now) > 10*time.Second || base.finalized != 1 || len(base.lists) != 1 || q.ImportSnapshot(hub.Now()).Phase == "warm_start" {
		t.Fatalf("warm start: elapsed=%s finalized=%d listing calls=%d phase=%s", hub.Now().Sub(now), base.finalized, len(base.lists), q.ImportSnapshot(hub.Now()).Phase)
	}
}
