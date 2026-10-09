package syncv4

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
)

func catchUpPlanFixture(t *testing.T, plan hubclient.V4ImportPlan, started time.Time, entries []Entry, leaveOut []string) *Queue {
	t.Helper()
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Merge(entries, started.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{
		LeaveOut: leaveOut, ImportPlan: &plan,
	}}, started); err != nil {
		t.Fatal(err)
	}
	return queue
}

func catchUpEntry(queue *Queue, key, agent string, activity time.Time) Entry {
	return Entry{Key: key, Activity: activity.UnixMilli(), SourceRevision: "source-" + key,
		Session:   hubclient.V4Session{InstallID: queue.InstallID(), LocalKeyHash: key, Agent: agent, Title: key},
		Selection: sessionbackupproducer.Selection{Agent: agent, SessionID: key}}
}

func entryKeys(entries []Entry) []string {
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		keys = append(keys, entry.Key)
	}
	return keys
}

func TestCatchUpThreeDaysThirtyPerAgent(t *testing.T) {
	started := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	plan := hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessionsPerAgent: 30}
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]Entry, 0, 77)
	for i := 0; i < 34; i++ {
		entries = append(entries, catchUpEntry(queue, fmt.Sprintf("codex-%02d", i), "codex", started.Add(-time.Duration(i+1)*time.Hour)))
	}
	for i := 0; i < 12; i++ {
		entries = append(entries, catchUpEntry(queue, fmt.Sprintf("claude-%02d", i), "claude", started.Add(-time.Duration(i+1)*6*time.Hour)))
	}
	for i := 0; i < 3; i++ {
		entries = append(entries, catchUpEntry(queue, fmt.Sprintf("opencode-%02d", i), "opencode", started.Add(-time.Duration(i+1)*8*time.Hour)))
	}
	for i := 0; i < 6; i++ {
		entries = append(entries, catchUpEntry(queue, fmt.Sprintf("codex-older-%02d", i), "codex", started.Add(-time.Duration(11+i)*24*time.Hour)))
	}
	for i := 0; i < 20; i++ {
		entries = append(entries, catchUpEntry(queue, fmt.Sprintf("opencode-older-%02d", i), "opencode", started.Add(-time.Duration(11+i)*24*time.Hour)))
	}
	for i := 0; i < 2; i++ {
		personal := catchUpEntry(queue, fmt.Sprintf("codex-personal-%02d", i), "codex", started.Add(-time.Duration(i+1)*time.Minute))
		personal.Session.CWDLabel = "~/personal/private-project"
		entries = append(entries, personal)
	}
	if err := queue.Merge(entries, started.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{LeaveOut: []string{"~/personal"}, ImportPlan: &plan}}, started); err != nil {
		t.Fatal(err)
	}
	if err := queue.FreezeCatchUp(plan); err != nil {
		t.Fatal(err)
	}
	planned := queue.PlannedEntries(plan, started.Add(90*24*time.Hour))
	if len(planned) != 45 {
		t.Fatalf("catch-up size = %d, want 45", len(planned))
	}
	counts := map[string]int{}
	for i, entry := range planned {
		counts[entry.Session.Agent]++
		if entry.Key == "older-than-window" || !isCatchUpEntry(entry, plan) {
			t.Fatalf("unexpected catch-up entry: %+v", entry)
		}
		if i > 0 && planned[i-1].Activity < entry.Activity {
			t.Fatalf("catch-up is not newest first: %s preceded %s", planned[i-1].Key, entry.Key)
		}
	}
	if counts["codex"] != 30 || counts["claude"] != 12 || counts["opencode"] != 3 {
		t.Fatalf("per-agent catch-up = %v", counts)
	}
	for _, entry := range queue.Entries() {
		if entry.Activity < started.Add(-10*24*time.Hour).UnixMilli() && isCatchUpEntry(entry, plan) {
			t.Fatalf("out-of-window session was selected: %s", entry.Key)
		}
		if strings.HasPrefix(entry.Key, "codex-personal-") && isCatchUpEntry(entry, plan) {
			t.Fatalf("leave-out session was selected: %s", entry.Key)
		}
	}
}

func TestDefaultPlanDoesNotUploadOlderHistory(t *testing.T) {
	started := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	plan := hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessions: 30, MaxSessionsPerAgent: 30}
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	older := catchUpEntry(queue, "offline-history", "codex", started.Add(-4*24*time.Hour))
	if err := queue.Merge([]Entry{older}, started); err != nil {
		t.Fatal(err)
	}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, started); err != nil {
		t.Fatal(err)
	}
	hub := &planHub{plan: &plan}
	runner := &Runner{Queue: queue, Hub: hub, Now: func() time.Time { return started }, checkedAt: started,
		config: hubclient.V4Config{ImportPlan: &plan}, lastReportedPhase: "warm_start"}
	if err := runner.runPlanned(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hub.creates != 0 || len(hub.lists) != 0 {
		t.Fatalf("default plan contacted Hub for older history: creates=%d lists=%v", hub.creates, hub.lists)
	}
	if got := queue.Entries()[0]; got.Listed || got.UploadID != "" || got.RevisionID != "" {
		t.Fatalf("older history entered the upload queue: %+v", got)
	}
}

func TestManualSixtyDayBackfillIsFiniteAndPerDevice(t *testing.T) {
	started := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	entries := make([]Entry, 0, 34)
	entry := func(key string, activity time.Time) Entry {
		return Entry{Key: key, Activity: activity.UnixMilli(), Session: hubclient.V4Session{LocalKeyHash: key, Agent: "codex"},
			Selection: sessionbackupproducer.Selection{Agent: "codex", SessionID: key}}
	}
	for i := 0; i < 31; i++ {
		entries = append(entries, entry(fmt.Sprintf("within-%02d", i), started.Add(-30*24*time.Hour-time.Duration(i)*time.Minute)))
	}
	entries = append(entries,
		entry("recent", started.Add(-2*24*time.Hour)),
		entry("cutoff", started.Add(-60*24*time.Hour)),
		entry("outside", started.Add(-60*24*time.Hour-time.Millisecond)))

	newQueue := func(plan hubclient.V4ImportPlan) *Queue {
		t.Helper()
		queue, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		for i := range entries {
			entries[i].Session.InstallID = queue.InstallID()
		}
		if err := queue.Merge(entries, started.Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, started); err != nil {
			t.Fatal(err)
		}
		if err := queue.FreezeCatchUp(plan); err != nil {
			t.Fatal(err)
		}
		return queue
	}

	defaultPlan := hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessions: 30, MaxSessionsPerAgent: 30}
	defaultQueue := newQueue(defaultPlan)
	if got := defaultQueue.PlannedEntries(defaultPlan, started); len(got) != 1 || got[0].Key != "recent" {
		t.Fatalf("default device plan = %v; want recent activity only", entryKeys(got))
	}

	backfillPlan := hubclient.V4ImportPlan{Version: 1, Window: "60d", Backfill: true}
	backfillQueue := newQueue(backfillPlan)
	planned := backfillQueue.PlannedEntries(backfillPlan, started)
	if len(planned) != 33 {
		t.Fatalf("manual 60-day plan selected %d sessions, want 33", len(planned))
	}
	keys := make(map[string]bool, len(planned))
	for _, entry := range planned {
		keys[entry.Key] = true
		if !isCatchUpEntry(entry, backfillPlan) {
			t.Fatalf("selected history was not frozen for the manual plan: %+v", entry)
		}
	}
	if !keys["cutoff"] || keys["outside"] {
		t.Fatalf("60-day boundary is not finite: cutoff=%v outside=%v", keys["cutoff"], keys["outside"])
	}
	discoveryPlan, planStartedAt := backfillQueue.DiscoveryPlan()
	if got, want := DiscoveryMinActivity(discoveryPlan, planStartedAt), started.Add(-60*24*time.Hour).UnixMilli(); got != want {
		t.Fatalf("discovery cutoff = %d, want %d", got, want)
	}
	if got := defaultQueue.PlannedEntries(defaultPlan, started); len(got) != 1 {
		t.Fatalf("opting one device into backfill changed another device's plan: %v", entryKeys(got))
	}
	for _, window := range []string{"7d", "10d", "30d", "60d", "all"} {
		if err := validateImportPlan(hubclient.V4ImportPlan{Window: window}); err == nil {
			t.Fatalf("extended %s plan was accepted without an explicit choice", window)
		}
	}
	if err := validateImportPlan(hubclient.V4ImportPlan{Window: "all", Backfill: true}); err == nil {
		t.Fatal("unbounded history was accepted as a finite backfill")
	}
	invalidSixtyDay := defaultPlan
	invalidSixtyDay.Version++
	invalidSixtyDay.Window = "60d"
	if err := defaultQueue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 2, Config: hubclient.V4Config{ImportPlan: &invalidSixtyDay}}, started.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := defaultQueue.FreezeCatchUp(invalidSixtyDay); err == nil {
		t.Fatal("60-day plan without explicit backfill was accepted")
	}
}

func TestCompletedBackfillReturnsToLiveOnlyActivity(t *testing.T) {
	started := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := catchUpEntry(queue, "backfilled", "codex", started.Add(-40*24*time.Hour))
	if err := queue.Merge([]Entry{old}, started.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	backfill := hubclient.V4ImportPlan{Version: 1, Window: "60d", Backfill: true}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &backfill}}, started); err != nil {
		t.Fatal(err)
	}
	if err := queue.FreezeCatchUp(backfill); err != nil {
		t.Fatal(err)
	}
	old = queue.Entries()[0]
	old.RevisionID, old.SyncedActivity, old.SyncedSourceRevision = "rev_backfilled", old.Activity, old.SourceRevision
	if err := queue.Update(old); err != nil {
		t.Fatal(err)
	}
	if err := queue.SetPhase("complete"); err != nil {
		t.Fatal(err)
	}

	returnedAt := started.Add(time.Hour)
	livePlan := hubclient.V4ImportPlan{Version: 2, Window: "3d", MaxSessions: 30, MaxSessionsPerAgent: 30}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 2, Config: hubclient.V4Config{ImportPlan: &livePlan}}, returnedAt); err != nil {
		t.Fatal(err)
	}
	if got := queue.ImportSnapshot(returnedAt).Phase; got != "complete" {
		t.Fatalf("post-backfill phase = %q, want complete", got)
	}
	offline := catchUpEntry(queue, "created-while-offline", "codex", returnedAt.Add(-time.Minute))
	live := catchUpEntry(queue, "live-after-reset", "codex", returnedAt.Add(time.Minute))
	if _, err := queue.MergePlannedDiscovery([]Entry{offline, live}, returnedAt.Add(2*time.Minute), livePlan, returnedAt); err != nil {
		t.Fatal(err)
	}
	if err := queue.FreezeCatchUp(livePlan); err != nil {
		t.Fatal(err)
	}
	keys := entryKeys(queue.PlannedEntries(livePlan, returnedAt.Add(2*time.Minute)))
	if slices.Contains(keys, offline.Key) || !slices.Contains(keys, live.Key) {
		t.Fatalf("post-backfill live plan = %v; offline session must stay out and new activity must stay in", keys)
	}
}

func TestChangedOldSessionSyncsDespiteCap(t *testing.T) {
	started := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	plan := hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessionsPerAgent: 1}
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	newer := catchUpEntry(queue, "newer", "codex", started.Add(-time.Hour))
	older := catchUpEntry(queue, "changed-old", "codex", started.Add(-2*time.Hour))
	if err := queue.Merge([]Entry{newer, older}, started.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, started); err != nil {
		t.Fatal(err)
	}
	if err := queue.FreezeCatchUp(plan); err != nil {
		t.Fatal(err)
	}
	changed := older
	changed.SourceRevision = "source-changed"
	changed.Activity = started.Add(time.Second).UnixMilli()
	if _, err := queue.MergePlannedDiscovery([]Entry{changed}, started.Add(time.Second), plan, started); err != nil {
		t.Fatal(err)
	}
	planned := queue.PlannedEntries(plan, started.Add(24*time.Hour))
	if len(planned) != 2 {
		t.Fatalf("planned entries after old session changed = %d, want catch-up plus changed old session", len(planned))
	}
	for _, entry := range planned {
		if entry.Key == "changed-old" && !pending(entry) {
			t.Fatal("changed old session is not pending")
		}
	}
}

func TestNewSessionDoesNotEvictCatchUpEntry(t *testing.T) {
	started := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	plan := hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessionsPerAgent: 30}
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]Entry, 30)
	for i := range entries {
		entries[i] = catchUpEntry(queue, fmt.Sprintf("baseline-%02d", i), "codex", started.Add(-time.Duration(i+1)*time.Hour))
	}
	if err := queue.Merge(entries, started.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, started); err != nil {
		t.Fatal(err)
	}
	if err := queue.FreezeCatchUp(plan); err != nil {
		t.Fatal(err)
	}
	queue, err = Open(filepath.Dir(queue.path))
	if err != nil {
		t.Fatal(err)
	}
	selected := map[string]bool{}
	for _, entry := range queue.PlannedEntries(plan, started) {
		if isCatchUpEntry(entry, plan) {
			selected[entry.Key] = true
		}
	}
	if _, err := queue.MergePlannedDiscovery([]Entry{catchUpEntry(queue, "new-session", "codex", started.Add(time.Second))}, started.Add(time.Second), plan, started); err != nil {
		t.Fatal(err)
	}
	planned := queue.PlannedEntries(plan, started.Add(24*time.Hour))
	catchUpCount, newInScope := 0, false
	for _, entry := range planned {
		if isCatchUpEntry(entry, plan) {
			catchUpCount++
			if !selected[entry.Key] {
				t.Fatalf("new session evicted %s and entered catch-up", entry.Key)
			}
		}
		if entry.Key == "new-session" {
			newInScope = true
			if isCatchUpEntry(entry, plan) {
				t.Fatal("post-start session was added to the frozen catch-up set")
			}
		}
	}
	if catchUpCount != 30 || !newInScope {
		t.Fatalf("catch-up=%d, new session in scope=%v", catchUpCount, newInScope)
	}
}

func TestProgressIgnoresOutOfScopeHistory(t *testing.T) {
	started := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	plan := hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessionsPerAgent: 1}
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	selected := catchUpEntry(queue, "selected", "codex", started.Add(-time.Hour))
	outOfScope := catchUpEntry(queue, "old-history", "codex", started.Add(-11*24*time.Hour))
	outOfScope.FailureCode = "upload_failed"
	if err := queue.Merge([]Entry{selected, outOfScope}, started.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, started); err != nil {
		t.Fatal(err)
	}
	if err := queue.FreezeCatchUp(plan); err != nil {
		t.Fatal(err)
	}
	progress := queue.Progress()
	if progress.Pending != 1 || progress.Failing != 0 || progress.FirstSync.RecentTotal != 1 || progress.FirstSync.HistoryState != "syncing" {
		t.Fatalf("in-scope progress = %+v", progress)
	}
	hub := &planHub{plan: &plan}
	runner := &Runner{Queue: queue, Hub: hub, Now: func() time.Time { return started }, checkedAt: started, config: hubclient.V4Config{ImportPlan: &plan}}
	if err := runner.listAll(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if len(hub.lists) != 1 || len(hub.lists[0]) != 1 || hub.lists[0][0].LocalKeyHash != "selected" {
		t.Fatalf("listing escaped scope: %+v", hub.lists)
	}
	oldEntry := queue.Entries()[1]
	if err := runner.ensureCreated(t.Context(), &oldEntry); !errors.Is(err, ErrPaused) || hub.creates != 0 {
		t.Fatalf("out-of-scope create: err=%v creates=%d", err, hub.creates)
	}
}

func TestLeaveOutBeatsCatchUp(t *testing.T) {
	started := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	plan := hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessionsPerAgent: 30}
	entry := Entry{Key: "private", Activity: started.Add(-time.Hour).UnixMilli(),
		Session:   hubclient.V4Session{Agent: "codex", Repo: "repo/private"},
		Selection: sessionbackupproducer.Selection{Agent: "codex", SessionID: "private"}}
	queue := catchUpPlanFixture(t, plan, started, []Entry{entry}, []string{"repo/private"})
	if err := queue.FreezeCatchUp(plan); err != nil {
		t.Fatal(err)
	}
	if got := len(queue.PlannedEntries(plan, started)); got != 0 || queue.Progress().Pending != 0 {
		t.Fatalf("leave-out entry leaked into catch-up: planned=%d progress=%+v", got, queue.Progress())
	}
}

func TestUnknownWindowStillFailsClosed(t *testing.T) {
	started := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	plan := hubclient.V4ImportPlan{Version: 1, Window: "11d", MaxSessionsPerAgent: 30}
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := catchUpEntry(queue, "unknown-window", "codex", started.Add(-time.Hour))
	if err := queue.Merge([]Entry{entry}, started.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, started); err != nil {
		t.Fatal(err)
	}
	if err := queue.FreezeCatchUp(plan); err == nil {
		t.Fatal("unknown window was accepted")
	}
	if got := len(queue.PlannedEntries(plan, started)); got != 0 {
		t.Fatalf("unknown window planned %d entries", got)
	}
	hub := &planHub{plan: &plan}
	runner := &Runner{Queue: queue, Hub: hub, Now: func() time.Time { return started }, checkedAt: started, config: hubclient.V4Config{ImportPlan: &plan}}
	if err := runner.listAll(t.Context(), plan); err == nil || len(hub.lists) != 0 || hub.creates != 0 {
		t.Fatalf("unknown window escaped fail-closed path: err=%v lists=%d creates=%d", err, len(hub.lists), hub.creates)
	}
}

func TestProgressDesignState_setup_sync_progress(t *testing.T) {
	started := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	plan := hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessionsPerAgent: 30}
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]Entry, 45)
	for i := range entries {
		agent := "codex"
		if i >= 30 {
			agent = "claude"
		}
		entries[i] = catchUpEntry(queue, fmt.Sprintf("screen-%02d", i), agent, started.Add(-time.Duration(i+1)*time.Hour))
	}
	if err := queue.Merge(entries, started.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, started); err != nil {
		t.Fatal(err)
	}
	if err := queue.FreezeCatchUp(plan); err != nil {
		t.Fatal(err)
	}
	progress := queue.Progress()
	if progress.FirstSync.RecentTotal != 45 || progress.FirstSync.RecentDone != 0 {
		t.Fatalf("initial design state progress = %+v", progress.FirstSync)
	}
	for i, entry := range queue.PlannedEntries(plan, started) {
		entry.RevisionID = fmt.Sprintf("revision-%02d", i)
		entry.SyncedActivity = entry.Activity
		entry.SyncedSourceRevision = entry.SourceRevision
		if err := queue.Update(entry); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if got := queue.Progress().FirstSync.RecentDone; got != 1 {
				t.Fatalf("recentDone after one completed session = %d", got)
			}
		}
	}
	progress = queue.Progress()
	if progress.Pending != 0 || progress.FirstSync.RecentDone != 45 || progress.FirstSync.RecentTotal != 45 || progress.FirstSync.HistoryState != "off" {
		t.Fatalf("completed design state progress = %+v", progress)
	}
}

func TestBaseCadenceFollowsNextCheckInSeconds(t *testing.T) {
	for _, test := range []struct {
		seconds int
		want    time.Duration
	}{{0, 300 * time.Second}, {1, 60 * time.Second}, {317, 317 * time.Second}, {1800, 900 * time.Second}} {
		if got := BaseCheckInCadence(test.seconds); got != test.want {
			t.Errorf("BaseCheckInCadence(%d) = %s, want %s", test.seconds, got, test.want)
		}
		queue, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, NextCheckInSeconds: test.seconds,
			Config: hubclient.V4Config{LeaveOut: []string{}}}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if got := queue.NextCheckInDelay(); got != test.want {
			t.Errorf("queue cadence for %d seconds = %s, want %s", test.seconds, got, test.want)
		}
	}
}

func TestDebounceDesignState_badges_live_revision(t *testing.T) {
	manager, prepared, _ := fixtureBundle(t)
	started := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	now := started
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessionsPerAgent: 30}
	entry := Entry{Key: "live", Activity: started.UnixMilli(), SourceRevision: "source-live", Live: true,
		ChangedAt: started.UnixMilli(), BundleID: prepared.BundleID, Selection: prepared.Selection,
		Session: hubclient.V4Session{InstallID: queue.InstallID(), LocalKeyHash: "live", Agent: "codex", Title: "Live"}}
	if err := queue.Merge([]Entry{entry}, started); err != nil {
		t.Fatal(err)
	}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, started); err != nil {
		t.Fatal(err)
	}
	hub := &warmOrderHub{planHub: &planHub{plan: &plan}}
	runner := &Runner{Queue: queue, Backup: manager, Hub: hub, Now: func() time.Time { return now }, checkedAt: started,
		config: hubclient.V4Config{ImportPlan: &plan}}
	if err := runner.syncPlannedContent(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if len(hub.events) != 0 {
		t.Fatal("active badge session uploaded before its two-minute debounce")
	}
	now = started.Add(150 * time.Second)
	if err := runner.syncPlannedContent(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if len(hub.events) != 1 || hub.events[0] != "create" || queue.Entries()[0].RevisionID == "" {
		t.Fatalf("active badge revision was not created by 2.5 minutes: events=%v entry=%+v", hub.events, queue.Entries()[0])
	}
	quietAt := now.Add(10 * time.Minute)
	entry = queue.Entries()[0]
	entry.Activity = quietAt.UnixMilli()
	entry.SourceRevision = "source-quiet"
	entry.Live = false
	entry.Session.Title = "Quiet"
	if err := queue.Merge([]Entry{entry}, quietAt); err != nil {
		t.Fatal(err)
	}
	quietManager, quietPrepared, _ := fixtureBundle(t)
	entry = queue.Entries()[0]
	entry.BundleID, entry.Selection = quietPrepared.BundleID, quietPrepared.Selection
	if err := queue.Update(entry); err != nil {
		t.Fatal(err)
	}
	runner.Backup = quietManager
	now = quietAt.Add(59 * time.Second)
	if err := runner.syncPlannedContent(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if len(hub.events) != 2 || hub.events[1] != "create" || queue.Progress().Pending != 0 {
		t.Fatalf("quiet badge revision missed the 60-second window: events=%v progress=%+v", hub.events, queue.Progress())
	}
}
