package syncv4

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
)

type planHub struct {
	plan       *hubclient.V4ImportPlan
	creates    int
	lists      [][]hubclient.V4ListItem
	checks     int
	checkInErr error
	results    []hubclient.V4CommandResult
	retryLists int
	leaveOut   []string
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
		if entry.Key == "fresh" && entry.Listed && now.Sub(started) < time.Minute {
			return
		}
	}
	t.Fatal("fresh session was not listed on the next boundary within the budget")
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
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
	manager, prepared := artifactLimitBundle(t, 1)
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "async", Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: "async"},
		Activity: now.UnixMilli(), Listed: true, UploadID: "up_async", SessionID: "ses_async", BundleID: prepared.BundleID}
	if err := q.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
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
	if progress.Pending != 0 || progress.FirstSync.RecentDone != 1 || snapshot.ContentSessions != 1 {
		t.Fatalf("queue=%+v import=%+v", progress, snapshot)
	}
}

func (*planHub) V4Binding(context.Context) (string, error) { return strings.Repeat("a", 64), nil }
func (h *planHub) V4CheckIn(_ context.Context, _ hubclient.V4Queue, _ int64, results []hubclient.V4CommandResult, _ []string, _ []hubclient.V4LogEntry) (hubclient.V4CheckIn, error) {
	h.checks++
	h.results = append([]hubclient.V4CommandResult(nil), results...)
	if h.checkInErr != nil {
		return hubclient.V4CheckIn{}, h.checkInErr
	}
	rules := h.leaveOut
	if rules == nil {
		rules = []string{}
	}
	return hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{LeaveOut: rules, ImportPlan: h.plan}, Capabilities: []string{"scale-import/v1"}}, nil
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
	return h.planHub.V4ListBatch(ctx, items)
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
	runner := &Runner{Backup: manager, Hub: hub, scaleEnabled: true}
	manifest, err := runner.manifest(prepared)
	if err != nil {
		t.Fatal(err)
	}
	jobs := make([]chunkJob, 0, 4)
	for i, artifact := range manifest.Artifacts {
		if len(jobs) == 4 {
			break
		}
		chunk := artifact.Chunks[0]
		jobs = append(jobs, chunkJob{missing: hubclient.V4Missing{ArtifactOrdinal: i, ChunkOrdinal: 0,
			Offset: chunk.Offset, Bytes: chunk.Bytes, SHA256: chunk.SHA256}, name: prepared.Manifest.Artifacts[i].LogicalName})
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
	plan := hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
		t.Fatal(err)
	}
	hub := &resumeScaleHub{planHub: &planHub{}, confirmed: map[[2]int]bool{}, attempts: map[[2]int]int{}, failOnce: [2]int{1, 0}}
	runner := &Runner{Queue: q, Backup: manager, Hub: hub, Now: func() time.Time { return now }, checkedAt: now, scaleEnabled: true, lastReportedPhase: "awaiting_plan",
		config: hubclient.V4Config{ImportPlan: &plan}}
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
	if err := runner.transfer(t.Context(), &entry); err == nil {
		t.Fatal("first parallel pass should stop on the injected chunk failure")
	}
	hub.mu.Lock()
	firstConfirmed := make(map[[2]int]bool, len(hub.confirmed))
	for key := range hub.confirmed {
		firstConfirmed[key] = true
	}
	hub.mu.Unlock()
	if len(firstConfirmed) == 0 {
		t.Fatal("successful chunks were not confirmed before interruption")
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
