package syncv4

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	backup "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

type unstableCapFixture struct {
	queue        *Queue
	runner       *Runner
	hub          *preparationRecoveryHub
	plan         hubclient.V4ImportPlan
	item         Entry
	now          time.Time
	preparations int
	failures     int
	mergeDuring  bool
}

func newUnstableCapFixture(t *testing.T, failures int) *unstableCapFixture {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(claude.ProjectsRoot(home), "project")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	homeJSON, err := json.Marshal(home)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, fixtureRootID+".jsonl")
	row := func(content string) string {
		return fmt.Sprintf(`{"sessionId":%q,"type":"user","timestamp":"2026-09-28T11:00:00Z","cwd":%s,"message":{"content":%q}}`, fixtureRootID, homeJSON, content) + "\n"
	}
	if err := os.WriteFile(file, []byte(row("first")), 0o600); err != nil {
		t.Fatal(err)
	}
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	f := &unstableCapFixture{queue: queue, now: start, failures: failures, mergeDuring: true, plan: hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &f.plan}}, start); err != nil {
		t.Fatal(err)
	}
	queue.SetActiveSince(start)
	f.item = Entry{Key: "live", Live: true, Listed: true, Activity: start.UnixMilli(), SourceRevision: "first",
		Selection: sessionbackupproducer.Selection{SourceKind: backup.SourceLocal, SourceID: "local", Agent: vendors.AgentClaude, SessionID: fixtureRootID},
		Session:   hubclient.V4Session{Agent: vendors.AgentClaude, LocalKeyHash: "live", Repo: "owner/private"}}
	if err := queue.Merge([]Entry{f.item}, start); err != nil {
		t.Fatal(err)
	}
	if err := queue.FreezeCatchUp(f.plan); err != nil {
		t.Fatal(err)
	}
	manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir(), OpenSource: func(context.Context, sessionbackupproducer.Selection) (sessionbackupproducer.SourceHandle, error) {
		return sessionbackupproducer.SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}, AfterRawCopy: func() {
		f.preparations++
		if f.preparations > f.failures {
			return
		}
		fileHandle, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = fileHandle.WriteString(row(fmt.Sprint(f.preparations)))
		closeErr := fileHandle.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if f.mergeDuring {
			f.item.Activity, f.item.SourceRevision = f.now.UnixMilli()+int64(f.preparations), fmt.Sprintf("during-%d", f.preparations)
			if err := f.queue.Merge([]Entry{f.item}, f.now); err != nil {
				t.Fatal(err)
			}
		}
	}})
	f.hub = &preparationRecoveryHub{planHub: planHub{plan: &f.plan}}
	f.runner = &Runner{Queue: queue, Backup: manager, Hub: f.hub, Now: func() time.Time { return f.now }, config: hubclient.V4Config{ImportPlan: &f.plan}}
	return f
}

func (f *unstableCapFixture) poll(t *testing.T, now time.Time, change bool) {
	t.Helper()
	f.now = now
	if change {
		f.item.Activity, f.item.SourceRevision = now.UnixMilli()+10, fmt.Sprintf("poll-%d", now.UnixMilli())
		if err := f.queue.Merge([]Entry{f.item}, now); err != nil {
			t.Fatal(err)
		}
	}
	f.runner.checkedAt = now
	if err := f.runner.syncPlannedContent(t.Context(), f.plan); err != nil {
		t.Fatal(err)
	}
}

func TestMaxHoldUnstableRetriesThenWaitsForQuiet(t *testing.T) {
	f := newUnstableCapFixture(t, 3)
	capAt := f.now.Add(10 * time.Minute)
	f.poll(t, capAt, true)
	if got := f.queue.Entries()[0]; f.preparations != 1 || got.CapAttempts != 1 || got.CapRetryAt != capAt.Add(20*time.Second).UnixMilli() || got.CapQuiet || f.queue.CapUnstableCount() != 0 {
		t.Fatalf("first failure: %+v preparations=%d", got, f.preparations)
	}
	root := filepath.Dir(f.queue.path)
	var err error
	f.queue, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	f.runner.Queue = f.queue
	f.poll(t, capAt.Add(19*time.Second), true)
	if f.preparations != 1 {
		t.Fatal("retried before 20 seconds")
	}
	f.poll(t, capAt.Add(20*time.Second), true)
	if got := f.queue.Entries()[0]; f.preparations != 2 || got.CapAttempts != 2 || got.CapRetryAt != capAt.Add(40*time.Second).UnixMilli() {
		t.Fatalf("second failure: %+v", got)
	}
	f.poll(t, capAt.Add(40*time.Second), true)
	if got := f.queue.Entries()[0]; f.preparations != 3 || got.CapAttempts != 3 || !got.CapQuiet || got.CapRetryAt != 0 || f.queue.CapUnstableCount() != 1 {
		t.Fatalf("fallback: %+v count=%d", got, f.queue.CapUnstableCount())
	}
	f.queue, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	f.runner.Queue = f.queue
	f.poll(t, capAt.Add(60*time.Second), true)
	f.poll(t, capAt.Add(179*time.Second), false)
	if f.preparations != 3 || f.hub.creates != 0 || f.queue.CapUnstableCount() != 1 {
		t.Fatalf("fallback retried early: preparations=%d creates=%d", f.preparations, f.hub.creates)
	}
	f.poll(t, capAt.Add(180*time.Second), false)
	got := f.queue.Entries()[0]
	if f.preparations != 4 || f.hub.creates != 1 || got.RevisionID != "recovered" || got.CapAttempts != 0 || got.CapQuiet || f.queue.CapUnstableCount() != 1 {
		t.Fatalf("quiet recovery: %+v preparations=%d creates=%d", got, f.preparations, f.hub.creates)
	}
}

func TestMaxHoldUnstableSucceedsOnNextPoll(t *testing.T) {
	f := newUnstableCapFixture(t, 1)
	capAt := f.now.Add(10 * time.Minute)
	f.poll(t, capAt, true)
	f.poll(t, capAt.Add(19*time.Second), true)
	if f.preparations != 1 {
		t.Fatal("retried before 20 seconds")
	}
	f.poll(t, capAt.Add(20*time.Second), true)
	got := f.queue.Entries()[0]
	if f.preparations != 2 || f.hub.creates != 1 || got.RevisionID != "recovered" || got.CapAttempts != 0 || got.CapQuiet || got.CapRetryAt != 0 || f.queue.CapUnstableCount() != 0 {
		t.Fatalf("recovery: %+v preparations=%d creates=%d", got, f.preparations, f.hub.creates)
	}
}

func TestMaxHoldUnstableWarmStartEstimate(t *testing.T) {
	f := newUnstableCapFixture(t, 1)
	capAt := f.now.Add(10 * time.Minute)
	f.now = capAt
	f.item.Activity, f.item.SourceRevision = capAt.UnixMilli(), "cap"
	if err := f.queue.Merge([]Entry{f.item}, capAt); err != nil {
		t.Fatal(err)
	}
	f.runner.checkedAt = capAt
	if err := f.runner.warmStart(t.Context(), f.plan, capAt, time.Minute, 0); err != nil {
		t.Fatal(err)
	}
	if got := f.queue.Entries()[0]; f.preparations != 1 || got.CapAttempts != 1 || got.CapRetryAt != capAt.Add(20*time.Second).UnixMilli() {
		t.Fatalf("warm estimate: %+v", got)
	}
	f.now = capAt.Add(20 * time.Second)
	f.item.Activity, f.item.SourceRevision = f.now.UnixMilli()+10, "next"
	if err := f.queue.Merge([]Entry{f.item}, f.now); err != nil {
		t.Fatal(err)
	}
	f.runner.checkedAt = f.now
	if err := f.runner.warmStart(t.Context(), f.plan, f.now, time.Minute, 0); err != nil {
		t.Fatal(err)
	}
	if got := f.queue.Entries()[0]; f.preparations != 2 || f.hub.creates != 1 || got.RevisionID != "recovered" || got.CapAttempts != 0 {
		t.Fatalf("warm recovery: %+v", got)
	}
}

func TestUnstableNonCapUsesNormalBackoff(t *testing.T) {
	f := newUnstableCapFixture(t, 1)
	f.mergeDuring = false
	f.now = f.now.Add(time.Minute)
	f.item.Live, f.item.Activity, f.item.SourceRevision = false, f.now.UnixMilli(), "ended"
	if err := f.queue.Merge([]Entry{f.item}, f.now); err != nil {
		t.Fatal(err)
	}
	entry := f.queue.Entries()[0]
	err := f.runner.prepareEntry(t.Context(), &entry)
	if err == nil || capPreparationUnstable(entry, err) {
		t.Fatalf("ordinary preparation: %v", err)
	}
	if err := f.runner.recordFailure(&entry, err); err != nil {
		t.Fatal(err)
	}
	got := f.queue.Entries()[0]
	if got.CapAttempts != 0 || got.CapQuiet || f.queue.CapUnstableCount() != 0 || !got.RetryAt.Equal(f.now.Add(time.Minute)) {
		t.Fatalf("ordinary backoff: %+v", got)
	}
}

func TestMaxHoldUnstableRetryRespectsExclusion(t *testing.T) {
	f := newUnstableCapFixture(t, 3)
	capAt := f.now.Add(10 * time.Minute)
	f.poll(t, capAt, true)
	if err := f.queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 2, Config: hubclient.V4Config{ImportPlan: &f.plan, LeaveOut: []string{"owner/private"}}}, capAt); err != nil {
		t.Fatal(err)
	}
	f.runner.config.LeaveOut = []string{"owner/private"}
	f.poll(t, capAt.Add(20*time.Second), true)
	if !f.queue.Entries()[0].Excluded || f.preparations != 1 || f.hub.creates != 0 || f.queue.CapUnstableCount() != 0 {
		t.Fatalf("excluded retry: %+v", f.queue.Entries()[0])
	}
}

func TestMaxHoldUnstableCannotReviveExcludedReservation(t *testing.T) {
	f := newUnstableCapFixture(t, 1)
	now := f.now.Add(10 * time.Minute)
	f.item.Activity = now.UnixMilli()
	if err := f.queue.Merge([]Entry{f.item}, now); err != nil {
		t.Fatal(err)
	}
	entry := f.queue.Entries()[0]
	if err := f.queue.freeze(&entry); err != nil {
		t.Fatal(err)
	}
	if err := f.queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 2, Config: hubclient.V4Config{ImportPlan: &f.plan, LeaveOut: []string{"owner/private"}}}, now); err != nil {
		t.Fatal(err)
	}
	if err := f.queue.capUnstable(&entry, now); err == nil {
		t.Fatal("excluded reservation was rescheduled")
	}
	if got := f.queue.Entries()[0]; !got.Excluded || got.Frozen != nil || got.CapAttempts != 0 || f.queue.CapUnstableCount() != 0 {
		t.Fatalf("excluded reservation: %+v", got)
	}
}

func TestMaxHoldPiRevisionMismatchRetries(t *testing.T) {
	_, _, _ = piQueueSource(t)
	start := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	now := start.Add(10 * time.Minute)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item := Entry{Key: "pi", Live: true, Activity: start.UnixMilli(), SourceRevision: "stale",
		Selection: sessionbackupproducer.Selection{SourceKind: backup.SourceLocal, SourceID: "local", Agent: vendors.AgentPi, SessionID: "pi.custom_01"},
		Session:   hubclient.V4Session{Agent: vendors.AgentPi}}
	if err := q.Merge([]Entry{item}, start); err != nil {
		t.Fatal(err)
	}
	item.Activity = now.UnixMilli()
	if err := q.Merge([]Entry{item}, now); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Queue: q, Backup: sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir()}), Now: func() time.Time { return now }}
	entry := q.Entries()[0]
	err = runner.prepareEntry(t.Context(), &entry)
	if !capPreparationUnstable(entry, err) || entry.BundleID == "" {
		t.Fatalf("Pi mismatch: %+v %v", entry, err)
	}
	if err := runner.recordFailure(&entry, err); err != nil {
		t.Fatal(err)
	}
	got := q.Entries()[0]
	if got.Frozen != nil || got.BundleID != "" || got.CapAttempts != 1 || got.CapRetryAt != now.Add(20*time.Second).UnixMilli() || len(q.Discards()) != 1 {
		t.Fatalf("Pi retry: %+v discards=%v", got, q.Discards())
	}
}

func TestMaxHoldRetrySchedulingBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	entry := Entry{Live: true, ChangedAt: now.UnixMilli(), CapRetryAt: now.Add(20 * time.Second).UnixMilli()}
	if retryDue(entry, now.Add(19*time.Second)) || !retryDue(entry, now.Add(20*time.Second)) {
		t.Fatal("cap retry interval changed")
	}
	q := &Queue{state: state{Entries: []Entry{entry}}}
	if got := q.nextRetryDelayAt(now); got != 20*time.Second {
		t.Fatalf("cap retry delay=%v", got)
	}
	q.state.Entries[0].RetryAt = now.Add(time.Minute)
	if got := q.nextRetryDelayAt(now); got != time.Minute {
		t.Fatalf("combined retry delay=%v", got)
	}
	q.state.Entries[0].RetryAt = time.Time{}
	q.state.Entries[0].Excluded = true
	if got := q.nextRetryDelayAt(now); got != 0 {
		t.Fatalf("excluded cap retry delay=%v", got)
	}
	entry.Live = false
	if !retryDue(entry, now) || !readyLive(entry, now) {
		t.Fatal("ended session waited for cap retry")
	}
	entry.Live, entry.CapQuiet, entry.CapRetryAt, entry.LastSentAt = true, true, 0, now.Add(-10*time.Minute).UnixMilli()
	if readyLive(entry, now.Add(2*time.Minute-time.Millisecond)) || !readyLive(entry, now.Add(2*time.Minute)) {
		t.Fatal("quiet fallback boundary changed")
	}
	q.state.Entries[0] = entry
	if got := q.nextRetryDelayAt(now); got != 2*time.Minute {
		t.Fatalf("quiet fallback delay=%v", got)
	}
}
