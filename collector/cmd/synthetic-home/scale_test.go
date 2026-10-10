package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/inventory"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func sessionKeys(sessions []*session.Session) []string {
	keys := make([]string, 0, len(sessions))
	for _, item := range sessions {
		keys = append(keys, item.Agent+"/"+item.ID)
	}
	sort.Strings(keys)
	return keys
}

// The inventory counts what discovery reads, the parse cache makes an
// unchanged pass parse nothing, one touched file costs one parse, and the
// streamed pass yields the same sessions newest first with a resumable cursor.
func TestSyntheticHomeInventoryCacheAndStreamedDiscovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the synthetic home reproduces the macOS and Linux agent layouts")
	}
	home := t.TempDir()
	result, err := generate(options{out: home, seed: 11, sessionsPerAgent: 5, bytesPerSession: 8192, now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("COSLASH_HOME", t.TempDir())
	store, err := syncv4.OpenFingerprints(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vendors.SetParseCache(store)
	t.Cleanup(func() { vendors.SetParseCache(nil) })

	// Inventory: every counted file is one discovery would read.
	snapshot, err := inventory.Scan(t.Context(), inventory.Options{Home: home, OpenCodeDB: filepath.Join(home, filepath.FromSlash(openCodeDB))})
	if err != nil {
		t.Fatal(err)
	}
	report := snapshot.Inventory()
	var walked, walkedBytes int64
	err = filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		relative, _ := filepath.Rel(home, path)
		counted := false
		switch {
		case strings.HasPrefix(relative, ".codex/sessions/") || strings.HasPrefix(relative, ".codex/archived_sessions/"):
			counted = strings.HasSuffix(path, ".jsonl")
		case strings.HasPrefix(relative, ".claude/projects/"):
			counted = strings.HasSuffix(path, ".jsonl") && !strings.Contains(relative, "/subagents/workflows/") || strings.HasPrefix(filepath.Base(path), "agent-") && strings.HasSuffix(path, ".jsonl")
		case strings.HasPrefix(relative, ".cursor/projects/"):
			counted = strings.HasSuffix(path, ".jsonl") && !strings.Contains(relative, "scratch-notes-draft")
		case relative == openCodeDB:
			counted = true
		}
		if counted {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			walked++
			walkedBytes += info.Size()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Files != walked || report.Bytes != walkedBytes || report.Windows.All.Bytes != walkedBytes {
		t.Fatalf("inventory files/bytes = %d/%d, independent walk = %d/%d", report.Files, report.Bytes, walked, walkedBytes)
	}
	if report.DurationMs < 0 || len(report.Agents) != 4 || report.Windows.H24.Sessions > report.Windows.D3.Sessions || report.Windows.D30.Sessions > report.Windows.All.Sessions {
		t.Fatalf("inventory = %+v", report)
	}

	// Cold pass stores one entry per parsed source; a warm pass parses none.
	first, err := collector.List(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(result.Sessions) {
		t.Fatalf("discovered %d sessions, manifest has %d", len(first), len(result.Sessions))
	}
	cold := store.Stats()
	if cold.Stores == 0 || cold.Hits != 0 {
		t.Fatalf("cold pass stats = %+v", cold)
	}
	roots := map[string]int64{}
	for _, item := range first {
		roots[item.Agent]++
	}
	for _, agent := range report.Agents {
		if (agent.Agent == vendors.AgentClaude || agent.Agent == vendors.AgentCursor) && agent.Files < roots[agent.Agent] {
			t.Fatalf("%s inventory files %d < discovered roots %d", agent.Agent, agent.Files, roots[agent.Agent])
		}
	}
	second, err := collector.List(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	warm := store.Stats()
	if warm.Stores != cold.Stores || warm.Hits != cold.Stores {
		t.Fatalf("warm pass stats = %+v after cold %+v; want 0 new stores and one hit per entry", warm, cold)
	}
	if strings.Join(sessionKeys(first), ",") != strings.Join(sessionKeys(second), ",") {
		t.Fatal("cached pass returned different sessions")
	}
	before, _ := json.Marshal(first)
	after, _ := json.Marshal(second)
	if string(before) != string(after) {
		t.Fatal("cached pass returned different session content")
	}

	// One touched transcript costs exactly one parse.
	var touched string
	for _, entry := range result.Sessions {
		if entry.Agent == vendors.AgentClaude && entry.ExpectedProblem == "" {
			touched = filepath.Join(home, filepath.FromSlash(entry.Paths[0]))
			break
		}
	}
	stamp := time.Now()
	if err := os.Chtimes(touched, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := collector.List(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	if touchedStats := store.Stats(); touchedStats.Stores != warm.Stores+1 {
		t.Fatalf("touched pass stores = %d, want %d", touchedStats.Stores, warm.Stores+1)
	}

	// Streamed discovery yields the same set, newest first, in resumable batches.
	snapshot, err = inventory.Scan(t.Context(), inventory.Options{Home: home, OpenCodeDB: filepath.Join(home, filepath.FromSlash(openCodeDB))})
	if err != nil {
		t.Fatal(err)
	}
	var streamed []*session.Session
	lastActivity := int64(-1)
	batches := 0
	for batch, err := range inventory.Discover(t.Context(), inventory.DiscoverOptions{Snapshot: snapshot, BatchFamilies: 3}) {
		if err != nil {
			t.Fatal(err)
		}
		batches++
		if lastActivity >= 0 && batch.Cursor.ActivityMs > lastActivity {
			t.Fatalf("batch %d moved forward in time: %d after %d", batches, batch.Cursor.ActivityMs, lastActivity)
		}
		lastActivity = batch.Cursor.ActivityMs
		for _, item := range batch.Sessions {
			if batch.ContentBytes[item.Agent+"\x00"+item.ID] <= 0 {
				t.Fatalf("missing source-byte estimate for %s/%s", item.Agent, item.ID)
			}
		}
		streamed = append(streamed, batch.Sessions...)
	}
	if batches < 2 || strings.Join(sessionKeys(streamed), ",") != strings.Join(sessionKeys(first), ",") {
		t.Fatalf("streamed %d batches, %d sessions; full pass %d", batches, len(streamed), len(first))
	}
	// The first streamed pass also caches each Codex rollout's header; the
	// next one parses and reads nothing.
	codexFiles := int64(0)
	for _, file := range snapshot.Files {
		if file.Agent == vendors.AgentCodex {
			codexFiles++
		}
	}
	streamStats := store.Stats()
	if streamStats.Stores > warm.Stores+1+codexFiles {
		t.Fatalf("streamed pass parsed %d sources beyond %d cached headers", streamStats.Stores-warm.Stores-1-codexFiles, codexFiles)
	}
	if _, err := inventory.DiscoverAll(t.Context(), inventory.DiscoverOptions{Snapshot: snapshot, BatchFamilies: 3}); err != nil {
		t.Fatal(err)
	}
	if again := store.Stats(); again.Stores != streamStats.Stores {
		t.Fatalf("second streamed pass stored %d entries", again.Stores-streamStats.Stores)
	}

	// Stop after the first batch, resume from the persisted cursor, and see
	// only the remaining families.
	var firstBatch []*session.Session
	consumerReceived := false
	persist := func(cursor inventory.Cursor) error {
		if !consumerReceived {
			return errors.New("cursor persisted before consumer received the batch")
		}
		encoded, err := json.Marshal(cursor)
		if err != nil {
			return err
		}
		return store.SaveDiscoveryCursor(encoded)
	}
	for batch, err := range inventory.Discover(t.Context(), inventory.DiscoverOptions{Snapshot: snapshot, BatchFamilies: 3, Persist: persist}) {
		if err != nil {
			t.Fatal(err)
		}
		firstBatch = batch.Sessions
		consumerReceived = true
		break
	}
	saved, ok := store.LoadDiscoveryCursor()
	if !ok {
		t.Fatal("cursor not persisted")
	}
	resume := inventory.DecodeCursor(saved)
	if resume == nil || resume.Complete {
		t.Fatalf("cursor = %+v", resume)
	}
	var rest []*session.Session
	for batch, err := range inventory.Discover(t.Context(), inventory.DiscoverOptions{Snapshot: snapshot, BatchFamilies: 3, Resume: resume, Persist: persist}) {
		if err != nil {
			t.Fatal(err)
		}
		rest = append(rest, batch.Sessions...)
	}
	seen := map[string]bool{}
	for _, item := range firstBatch {
		seen[item.Agent+"/"+item.ID] = true
	}
	for _, item := range rest {
		if seen[item.Agent+"/"+item.ID] {
			t.Fatalf("%s/%s yielded again after resume", item.Agent, item.ID)
		}
	}
	if strings.Join(sessionKeys(append(append([]*session.Session{}, firstBatch...), rest...)), ",") != strings.Join(sessionKeys(first), ",") {
		t.Fatalf("resume covered %d + %d sessions, want %d", len(firstBatch), len(rest), len(first))
	}
	if final := inventory.DecodeCursor(mustCursor(t, store)); final == nil || !final.Complete {
		t.Fatalf("final cursor = %+v", final)
	}

	// A corrupt cache is rebuilt without a crash or duplicates.
	corrupted := 0
	err = filepath.WalkDir(t.TempDir(), func(string, fs.DirEntry, error) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(cacheRoot(t, store), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() || filepath.Ext(path) != ".json" || corrupted >= 3 {
			return err
		}
		corrupted++
		return os.WriteFile(path, []byte("not a cache entry"), 0o600)
	})
	if err != nil || corrupted != 3 {
		t.Fatalf("corrupted %d entries: %v", corrupted, err)
	}
	reopened, err := syncv4.OpenFingerprints(cacheRoot(t, store))
	if err != nil {
		t.Fatal(err)
	}
	vendors.SetParseCache(reopened)
	rebuilt, err := collector.List(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sessionKeys(rebuilt), ",") != strings.Join(sessionKeys(first), ",") {
		t.Fatalf("rebuilt pass = %d sessions, want %d", len(rebuilt), len(first))
	}
	if stats := reopened.Stats(); stats.Stores < 1 || stats.Stores > 3 {
		t.Fatalf("rebuild parsed %d sources for 3 corrupt entries", stats.Stores)
	}
}

func TestDefaultImportPlanStaysBoundedAcrossTwoRestarts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the synthetic home reproduces the macOS and Linux agent layouts")
	}
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	home := t.TempDir()
	fixture, err := generate(options{out: home, seed: 1010, sessionsPerAgent: 20, now: now})
	if err != nil {
		t.Fatal(err)
	}
	var recent, history int
	for _, item := range fixture.Sessions {
		if item.Recent {
			recent++
		} else {
			history++
		}
	}
	if recent < 46 || history < 40 {
		t.Fatalf("synthetic home has recent=%d history=%d; want at least 46 and 40", recent, history)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("COSLASH_HOME", t.TempDir())
	t.Setenv("COSLASH_SCALE_IMPORT", "1")
	discovered, err := collector.List(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	byKey := make(map[string]*session.Session, len(discovered))
	for _, item := range discovered {
		byKey[item.Agent+"/"+item.ID] = item
	}
	entries := make([]syncv4.Entry, 0, len(fixture.Sessions))
	for _, item := range fixture.Sessions {
		key := item.Agent + "/" + item.ID
		parsed := byKey[key]
		if parsed == nil {
			t.Fatalf("synthetic %s session %s was not discovered", item.Agent, item.ID)
		}
		entries = append(entries, syncv4.Entry{
			Key: key, Activity: parsed.LastActivityTime, SourceRevision: parsed.DetailRevision,
			Session:   hubclient.V4Session{LocalKeyHash: key, Agent: parsed.Agent},
			Selection: sessionbackupproducer.Selection{SourceKind: "local", SourceID: "local", Agent: parsed.Agent, SessionID: parsed.ID},
		})
	}

	root := t.TempDir()
	queue, err := syncv4.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	plan := hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessions: 30, MaxSessionsPerAgent: 30}
	activeSince := now.Add(time.Minute)
	queue.SetActiveSince(activeSince)
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{
		ConfigVersion: 1, Capabilities: []string{hubclient.CapabilityScaleImport},
		Config: hubclient.V4Config{ImportPlan: &plan},
	}, now); err != nil {
		t.Fatal(err)
	}

	assertSelection := func(stage string, at time.Time) []syncv4.Entry {
		t.Helper()
		if err := queue.FreezeCatchUp(plan); err != nil {
			t.Fatal(err)
		}
		selected := queue.PlannedEntries(plan, at)
		if len(selected) > 30 {
			details := make([]string, 0, len(selected))
			for _, entry := range selected {
				details = append(details, fmt.Sprintf("%s(activity=%s catchup=%d changed=%d)", entry.Key,
					time.UnixMilli(entry.Activity).UTC().Format(time.RFC3339), entry.CatchUpPlanVersion, entry.ChangedPlanVersion))
			}
			t.Fatalf("%s selected %d sessions, want at most 30: %s", stage, len(selected), strings.Join(details, ", "))
		}
		cutoff := now.Add(-72 * time.Hour).UnixMilli()
		for _, entry := range selected {
			if entry.Activity < cutoff {
				t.Fatalf("%s selected %s outside the three-day window", stage, entry.Key)
			}
		}
		return selected
	}
	markListed := func(selected []syncv4.Entry, at time.Time) {
		t.Helper()
		results := make([]hubclient.V4ListResult, 0, len(selected))
		for i, entry := range selected {
			results = append(results, hubclient.V4ListResult{
				LocalKeyHash: entry.Key, SessionID: fmt.Sprintf("synthetic-session-%03d", i), State: "existing",
			})
		}
		if err := queue.MarkListedAt(results, at); err != nil {
			t.Fatal(err)
		}
	}
	markUploaded := func(selected []syncv4.Entry) {
		t.Helper()
		for i, entry := range selected {
			entry.RevisionID = fmt.Sprintf("synthetic-revision-%03d", i)
			entry.SyncedActivity = entry.Activity
			entry.SyncedSourceRevision = entry.SourceRevision
			if err := queue.Update(entry); err != nil {
				t.Fatal(err)
			}
		}
	}

	for start, restart := 0, 0; start < len(entries); start += 8 {
		end := min(start+8, len(entries))
		at := activeSince.Add(time.Duration(start) * time.Second)
		if _, err := queue.MergePlannedDiscovery(entries[start:end], at, plan, activeSince); err != nil {
			t.Fatal(err)
		}
		selected := assertSelection(fmt.Sprintf("discovery batch %d", start/8+1), at)
		markListed(selected, at)
		if restart < 2 && start/8 == restart {
			markUploaded(selected[:len(selected)/2])
			queue, err = syncv4.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			activeSince = now.Add(time.Duration(restart+2) * time.Hour)
			queue.SetActiveSince(activeSince)
			assertSelection(fmt.Sprintf("restart %d", restart+1), activeSince)
			restart++
		}
	}
	selected := assertSelection("completed discovery", activeSince)
	if len(selected) != 30 {
		t.Fatalf("completed discovery selected %d sessions, want 30 from the synthetic recent corpus", len(selected))
	}
	markUploaded(selected)
	progress := queue.Progress()
	if progress.FirstSync.RecentDone != 30 || progress.FirstSync.RecentTotal != 30 || progress.FirstSync.HistoryState != "off" {
		t.Fatalf("completed recent-only import progress = %+v", progress.FirstSync)
	}
}

type warmRestartHub struct {
	plan       *hubclient.V4ImportPlan
	creates    int
	lists      [][]hubclient.V4ListItem
	cancel     context.CancelFunc
	cancelAt   int
	advanceNow func()
}

func (*warmRestartHub) V4Binding(context.Context) (string, error) {
	return strings.Repeat("a", 64), nil
}

func (h *warmRestartHub) V4CheckIn(context.Context, hubclient.V4Queue, int64, []hubclient.V4CommandResult, []string, []hubclient.V4LogEntry) (hubclient.V4CheckIn, error) {
	return hubclient.V4CheckIn{ConfigVersion: 1, Capabilities: []string{hubclient.CapabilityScaleImport},
		Config: hubclient.V4Config{ImportPlan: h.plan}}, nil
}

func (h *warmRestartHub) V4Create(_ context.Context, _ hubclient.V4Create) (hubclient.V4Status, error) {
	h.creates++
	if h.creates == h.cancelAt {
		h.advanceNow()
		h.cancel()
	}
	return hubclient.V4Status{SessionID: fmt.Sprintf("session-%03d", h.creates), State: "completed",
		RevisionID: fmt.Sprintf("revision-%03d", h.creates)}, nil
}

func (*warmRestartHub) V4Status(context.Context, string) (hubclient.V4Status, error) {
	return hubclient.V4Status{}, errors.New("unexpected status request")
}

func (*warmRestartHub) V4PutChunk(context.Context, string, hubclient.V4Missing, io.Reader) error {
	return errors.New("unexpected chunk upload")
}

func (*warmRestartHub) V4Confirm(context.Context, string, ...hubclient.V4Missing) (hubclient.V4Status, error) {
	return hubclient.V4Status{}, errors.New("unexpected confirmation")
}

func (*warmRestartHub) V4Finalize(context.Context, string) (hubclient.V4Status, error) {
	return hubclient.V4Status{}, errors.New("unexpected finalization")
}

func (h *warmRestartHub) V4ListBatch(ctx context.Context, items []hubclient.V4ListItem) ([]hubclient.V4ListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h.lists = append(h.lists, append([]hubclient.V4ListItem(nil), items...))
	results := make([]hubclient.V4ListResult, len(items))
	for i, item := range items {
		results[i] = hubclient.V4ListResult{LocalKeyHash: item.LocalKeyHash, SessionID: "session-" + item.LocalKeyHash, State: "listed"}
	}
	return results, nil
}

func TestRunnerReopenDuringWarmStartKeepsDefaultPlanBounded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the synthetic home reproduces the macOS and Linux agent layouts")
	}
	started := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	home := t.TempDir()
	if _, err := generate(options{out: home, seed: 1013, sessionsPerAgent: 20, now: started}); err != nil {
		t.Fatal(err)
	}
	coslashHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("COSLASH_HOME", coslashHome)
	t.Setenv("COSLASH_SCALE_IMPORT", "1")

	plan := &hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessions: 30, MaxSessionsPerAgent: 30, WarmStartSeconds: 30}
	now := started
	queue, err := syncv4.Open("")
	if err != nil {
		t.Fatal(err)
	}
	firstCtx, stopFirst := context.WithCancel(t.Context())
	firstHub := &warmRestartHub{plan: plan, cancel: stopFirst, cancelAt: 3, advanceNow: func() { now = started.Add(28 * time.Second) }}
	backup := sessionbackupproducer.New(sessionbackupproducer.Options{
		Root:      filepath.Join(coslashHome, "session-backups", "prepared"),
		LocalHome: func() (string, error) { return home, nil },
	})
	restartPass := false
	discoverBatches := func(ctx context.Context, visit func(syncv4.DiscoveryBatch) error) error {
		sessions, err := collector.List(ctx, 0)
		if err != nil {
			return err
		}
		if restartPass {
			// Re-discovery can observe a changed content revision without making
			// an older session eligible for this history-off plan.
			cutoff := started.Add(-72 * time.Hour).UnixMilli()
			for _, item := range sessions {
				if item.LastActivityTime < cutoff {
					item.DetailRevision += "-reopened"
				}
			}
		}
		for start := 0; start < len(sessions); start += 64 {
			if err := visit(syncv4.DiscoveryBatch{Sessions: sessions[start:min(start+64, len(sessions))]}); err != nil {
				return err
			}
		}
		return nil
	}
	firstRunner := &syncv4.Runner{Queue: queue, Backup: backup, Hub: firstHub,
		Discover:          func(ctx context.Context) ([]*session.Session, error) { return collector.List(ctx, 0) },
		DiscoverBatches:   discoverBatches,
		RequireImportPlan: true, Now: func() time.Time { return now }}
	firstErr := firstRunner.SyncOnce(firstCtx)
	if firstErr == nil {
		t.Fatal("first process kept running after the warm-start interruption")
	}
	if firstHub.creates != 3 {
		t.Fatalf("first process completed %d uploads before interruption, want 3 (error: %v)", firstHub.creates, firstErr)
	}
	completed := 0
	for _, entry := range queue.Entries() {
		if entry.RevisionID != "" {
			completed++
		}
	}
	if completed != 3 {
		t.Fatalf("persisted completed uploads = %d, want 3", completed)
	}
	if elapsed := now.Sub(started); elapsed >= 30*time.Second {
		t.Fatalf("process stopped after %s, outside the 30-second warm-start window", elapsed)
	}

	// Drop the first runner and queue without calling any shutdown hook. The next
	// process must use only the atomically persisted queue under COSLASH_HOME.
	firstRunner, queue = nil, nil
	restartedAt := started.Add(28 * time.Second)
	now = restartedAt
	restartPass = true
	queue, err = syncv4.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if !queue.PlanStartedAt().Equal(started) {
		t.Fatalf("reopened plan start = %s, want %s", queue.PlanStartedAt(), started)
	}
	_, persistedConfig, _ := queue.Policy()
	if persisted := persistedConfig.ImportPlan; persisted == nil || persisted.Window != "3d" || persisted.History ||
		persisted.MaxSessions != 30 || persisted.MaxSessionsPerAgent != 30 || persisted.WarmStartSeconds != 30 {
		t.Fatalf("reopened import plan = %+v, want default 3d / 30 / history off / warm start 30", persisted)
	}
	queue.SetActiveSince(restartedAt)
	secondHub := &warmRestartHub{plan: plan}
	secondRunner := &syncv4.Runner{Queue: queue, Backup: backup, Hub: secondHub,
		Discover:          func(ctx context.Context) ([]*session.Session, error) { return collector.List(ctx, 0) },
		DiscoverBatches:   discoverBatches,
		RequireImportPlan: true, Now: func() time.Time { return now }}
	restartErr := secondRunner.SyncOnce(t.Context())
	if len(secondHub.lists) == 0 {
		t.Fatalf("reopened runner did not list the selected sessions: %v", restartErr)
	}

	var listed []hubclient.V4ListItem
	for _, batch := range secondHub.lists {
		listed = append(listed, batch...)
	}
	if len(listed) > 30 {
		t.Fatalf("restart listed %d sessions, want at most 30", len(listed))
	}
	cutoff := started.Add(-72 * time.Hour)
	for _, item := range listed {
		if item.ActivityAt.Before(cutoff) {
			t.Fatalf("restart listed %s outside the three-day window: %s", item.LocalKeyHash, item.ActivityAt)
		}
	}
}

func mustCursor(t *testing.T, store *syncv4.Fingerprints) json.RawMessage {
	t.Helper()
	cursor, ok := store.LoadDiscoveryCursor()
	if !ok {
		t.Fatal("cursor missing")
	}
	return cursor
}

func cacheRoot(t *testing.T, store *syncv4.Fingerprints) string {
	t.Helper()
	return store.Root()
}

// TestScaleCorpusTimings measures the D-13 inventory target and the cache on
// a generated corpus. It runs only with COSLASH_SCALE_HOME set to a home
// written by this generator, and writes numbers only.
func TestScaleCorpusTimings(t *testing.T) {
	home := os.Getenv("COSLASH_SCALE_HOME")
	if home == "" {
		t.Skip("set COSLASH_SCALE_HOME to a generated synthetic home")
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("COSLASH_HOME", t.TempDir())
	cacheRoot := os.Getenv("COSLASH_SCALE_CACHE")
	if cacheRoot == "" {
		cacheRoot = t.TempDir()
	}
	store, err := syncv4.OpenFingerprints(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	vendors.SetParseCache(store)
	t.Cleanup(func() { vendors.SetParseCache(nil) })
	timings := map[string]any{"cacheEntriesAtStart": store.Stats().Entries}
	started := time.Now()
	snapshot, err := inventory.Scan(t.Context(), inventory.Options{Home: home, OpenCodeDB: filepath.Join(home, filepath.FromSlash(openCodeDB))})
	if err != nil {
		t.Fatal(err)
	}
	report := snapshot.Inventory()
	timings["inventoryMs"] = time.Since(started).Milliseconds()
	timings["inventory"] = report
	if report.DurationMs > 10_000 {
		t.Errorf("inventory took %d ms, D-13 allows 10,000", report.DurationMs)
	}

	started = time.Now()
	var firstBatchMs int64 = -1
	streamed := 0
	for batch, err := range inventory.Discover(t.Context(), inventory.DiscoverOptions{Snapshot: snapshot}) {
		if err != nil {
			t.Fatal(err)
		}
		if firstBatchMs < 0 {
			firstBatchMs = time.Since(started).Milliseconds()
		}
		streamed += len(batch.Sessions)
	}
	timings["coldStreamFirstBatchMs"] = firstBatchMs
	timings["coldStreamTotalMs"] = time.Since(started).Milliseconds()
	timings["coldStreamSessions"] = streamed
	cold := store.Stats()
	timings["coldStores"] = cold.Stores

	started = time.Now()
	sessions, err := collector.List(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	timings["warmListMs"] = time.Since(started).Milliseconds()
	timings["warmListSessions"] = len(sessions)
	warm := store.Stats()
	timings["warmStores"] = warm.Stores - cold.Stores
	timings["warmHits"] = warm.Hits - cold.Hits
	if warm.Stores != cold.Stores {
		t.Errorf("warm pass parsed %d sources", warm.Stores-cold.Stores)
	}

	started = time.Now()
	if _, err := collector.List(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	timings["warmListAgainMs"] = time.Since(started).Milliseconds()
	encoded, _ := json.MarshalIndent(timings, "", "  ")
	t.Logf("scale timings:\n%s", encoded)
	if path := os.Getenv("COSLASH_SCALE_EVIDENCE"); path != "" {
		if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
