package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/inventory"
	"github.com/centauri-ai/coslash/collector/internal/session"
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
