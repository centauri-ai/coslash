package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
)

func grokHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GROK_HOME", filepath.Join(home, "custom grok"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("OPENCODE_DB", filepath.Join(home, "missing.db"))
	return home
}

func grokFile(t *testing.T, dir, name, body string, stamp time.Time) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func grokSummary(t *testing.T, id, kind, parent string, stamp time.Time) string {
	t.Helper()
	dir := filepath.Join(os.Getenv("GROK_HOME"), "sessions", "encoded", id)
	body, err := json.Marshal(map[string]any{"info": map[string]string{"id": id}, "chat_format_version": 1, "session_kind": kind, "parent_session_id": parent, "created_at": "2026-09-01T00:00:00Z", "last_active_at": stamp.UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	grokFile(t, dir, "summary.json", string(body), stamp)
	grokFile(t, dir, "signals.json", `{"turnCount":1}`, stamp)
	return dir
}

func TestGrokInventoryCountsOnlyParserInputs(t *testing.T) {
	home := grokHome(t)
	dir := grokSummary(t, "root", "", "", now)
	names := []string{"updates.jsonl", "signals.json", "usage.json", "plan.json", "plan.md", "plan_mode.json", "prompt_context.json", "goal/state.json", "compaction_checkpoints/one.json", "subagents/sub/meta.json", "subagents/sub/output.json"}
	for _, name := range names {
		grokFile(t, dir, name, "{}", now)
	}
	for _, name := range []string{"chat_history.jsonl", "events.jsonl", "auth.json", "goal/plan.md", "subagents/sub/unrelated.json", "arbitrary/summary.json", "compaction_checkpoints/one.txt"} {
		grokFile(t, dir, name, "private", now)
	}
	grokFile(t, filepath.Dir(filepath.Dir(dir)), "session_search.sqlite", "private", now)
	snapshot, err := Scan(t.Context(), Options{Home: home, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	report := snapshot.Inventory()
	if len(report.Agents) != 1 || report.Agents[0].Agent != "grok" || report.Files != 12 || report.Windows.H24.Sessions != 1 {
		t.Fatalf("inventory = %+v", report)
	}
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := os.ReadFile(filepath.Join(dir, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Bytes != int64(len(summary)+22) {
		t.Fatalf("source bytes=%d", report.Bytes)
	}
	for _, file := range snapshot.Files {
		if file.Agent != vendors.AgentGrok {
			t.Fatalf("unexpected file %+v", file)
		}
	}
	if strings.Contains(string(payload), home) || strings.Contains(string(payload), ".json") {
		t.Fatalf("unexpected inventory fields: %s", payload)
	}
}

func TestGrokDiscoveryWindowsFamiliesAndCursor(t *testing.T) {
	home := grokHome(t)
	old := now.Add(-10 * 24 * time.Hour)
	root := grokSummary(t, "root", "", "", old)
	grokSummary(t, "child", "subagent", "", old)
	grandchild := grokSummary(t, "grandchild", "subagent", "child", old)
	grokFile(t, root, "subagents/sub/meta.json", `{"child_session_id":"child","description":"child"}`, old)
	child := filepath.Join(filepath.Dir(root), "child")
	grokFile(t, child, "subagents/sub/meta.json", `{"child_session_id":"grandchild","description":"grandchild"}`, old)
	grokFile(t, grandchild, "usage.json", `{"session":{"inputTokens":12,"outputTokens":3,"cachedReadTokens":4}}`, now)
	grokSummary(t, "fork", "fork", "root", now.Add(-time.Hour))
	grokSummary(t, "worktree", "worktree", "root", now.Add(-2*time.Hour))
	grokSummary(t, "old", "", "", old)
	cutoff := now.Add(-3 * time.Hour).UnixMilli()
	snapshot, err := Scan(t.Context(), Options{Home: home, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	var resume *Cursor
	for batch, err := range Discover(t.Context(), DiscoverOptions{Snapshot: snapshot, MinActivityMs: cutoff, BatchFamilies: 1, Persist: func(c Cursor) error { resume = &c; return nil }}) {
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range batch.Sessions {
			ids = append(ids, s.ID)
			if s.ID == "root" {
				if len(s.Subagents) != 1 || s.Subagents[0].ID != "child" {
					t.Fatalf("root children = %+v", s.Subagents)
				}
				if batch.ContentBytes["grok\x00root"] <= 0 {
					t.Fatal("missing family bytes")
				}
			}
		}
		if len(ids) == 1 {
			break
		}
	}
	if !reflect.DeepEqual(ids, []string{"root"}) || resume == nil || resume.Complete {
		t.Fatalf("first batch = %v, cursor=%+v", ids, resume)
	}
	for batch, err := range Discover(t.Context(), DiscoverOptions{Snapshot: snapshot, MinActivityMs: cutoff, BatchFamilies: 1, Resume: resume}) {
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range batch.Sessions {
			ids = append(ids, s.ID)
		}
	}
	if !reflect.DeepEqual(ids, []string{"root", "fork", "worktree"}) {
		t.Fatalf("resumed ids = %v", ids)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := DiscoverAll(ctx, DiscoverOptions{Snapshot: snapshot}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestGrokMissingAndDefaultHome(t *testing.T) {
	home := grokHome(t)
	t.Setenv("GROK_HOME", "")
	snapshot, err := Scan(t.Context(), Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, agent := range snapshot.Missing {
		found = found || agent == "grok"
	}
	if !found {
		t.Fatalf("missing=%v", snapshot.Missing)
	}
	dir := filepath.Join(home, ".grok", "sessions", "encoded", "root")
	grokFile(t, dir, "summary.json", `{"info":{"id":"root"},"chat_format_version":1}`, now)
	snapshot, err = Scan(t.Context(), Options{Home: home})
	if err != nil || len(snapshot.Files) != 1 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
}

func TestGrokDiscoveryWindowBoundariesAndChangedCursor(t *testing.T) {
	home := grokHome(t)
	cutoff := now.Add(-24 * time.Hour)
	for _, name := range []string{"usage.json", "updates.jsonl"} {
		dir := grokSummary(t, name, "", "", cutoff.Add(-time.Hour))
		grokFile(t, dir, name, "{}", cutoff)
	}
	grokSummary(t, "old", "", "", cutoff.Add(-time.Millisecond))
	grokSummary(t, "summary-active", "", "", now)
	// A copied store can have old mtimes while the source summary is recent.
	activeDir := filepath.Join(os.Getenv("GROK_HOME"), "sessions", "encoded", "summary-active")
	for _, name := range []string{"summary.json", "signals.json"} {
		if err := os.Chtimes(filepath.Join(activeDir, name), cutoff.Add(-time.Hour), cutoff.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := Scan(t.Context(), Options{Home: home, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planDiscovery(t.Context(), DiscoverOptions{Snapshot: snapshot, MinActivityMs: cutoff.UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, family := range plan.families {
		ids = append(ids, family.id)
	}
	if !reflect.DeepEqual(ids, []string{"summary-active", "updates.jsonl", "usage.json"}) {
		t.Fatalf("window=%v", ids)
	}
	resume := &Cursor{StartedAtMs: now.Add(-time.Hour).UnixMilli(), ActivityMs: cutoff.UnixMilli(), Agent: "grok", Family: "usage.json"}
	changed := filepath.Join(os.Getenv("GROK_HOME"), "sessions", "encoded", "usage.json")
	grokFile(t, changed, "usage.json", `{"session":{"inputTokens":19}}`, now)
	snapshot, err = Scan(t.Context(), Options{Home: home, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	plan, err = planDiscovery(t.Context(), DiscoverOptions{Snapshot: snapshot, MinActivityMs: cutoff.UnixMilli(), Resume: resume})
	if err != nil {
		t.Fatal(err)
	}
	ids = nil
	for _, family := range plan.families {
		ids = append(ids, family.id)
	}
	if !reflect.DeepEqual(ids, []string{"summary-active", "usage.json"}) {
		t.Fatalf("changed resume=%v", ids)
	}
}

func TestGrokInventoryIncludesFiveAgentsWithoutPi(t *testing.T) {
	home, db := fixtureHome(t)
	t.Setenv("GROK_HOME", filepath.Join(home, "custom grok"))
	grokSummary(t, "root", "", "", now)
	grokFile(t, filepath.Join(home, ".pi", "agent", "sessions"), "unrelated.jsonl", "private", now)
	snapshot, err := Scan(t.Context(), Options{Home: home, OpenCodeDB: db, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	agents := []string{}
	for _, agent := range snapshot.Inventory().Agents {
		agents = append(agents, agent.Agent)
	}
	if !reflect.DeepEqual(agents, []string{"codex", "claude", "cursor", "opencode", "grok"}) {
		t.Fatalf("agents=%v", agents)
	}
}

func TestGrokDiscoveryKeepsSameIDAcrossAgents(t *testing.T) {
	home := grokHome(t)
	id := "11111111-2222-3333-4444-555555555555"
	grokSummary(t, id, "", "", now)
	dir := filepath.Join(claude.ProjectsRoot(home), "project")
	grokFile(t, dir, id+".jsonl", `{"sessionId":"`+id+`","uuid":"row-1","timestamp":"2026-09-29T12:00:00Z","type":"user","message":{"content":"hello"}}`, now)
	snapshot, err := Scan(t.Context(), Options{Home: home, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := DiscoverAll(t.Context(), DiscoverOptions{Snapshot: snapshot, BatchFamilies: 1})
	if err != nil {
		t.Fatal(err)
	}
	identities := map[string]string{}
	for _, s := range sessions {
		identities[s.Agent] = s.ID
	}
	if !reflect.DeepEqual(identities, map[string]string{"claude": id, "grok": id}) {
		t.Fatalf("identities=%v", identities)
	}
}

func TestGrokPlanningFailureReplaysRepairedFamily(t *testing.T) {
	home := grokHome(t)
	grokSummary(t, "healthy", "", "", now.Add(-2*time.Hour))
	oldStamp := now.Add(-90 * 24 * time.Hour)
	old := grokSummary(t, "old", "", "", oldStamp)
	grokFile(t, old, "summary.json", `{`, oldStamp)
	snapshot, err := Scan(t.Context(), Options{Home: home, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	var scanErr error
	persisted := []Cursor{}
	resume := Cursor{StartedAtMs: now.UnixMilli(), ActivityMs: now.Add(-3 * time.Hour).UnixMilli(), Agent: "grok", Family: "previous"}
	for batch, err := range Discover(t.Context(), DiscoverOptions{Snapshot: snapshot, MinActivityMs: now.Add(-24 * time.Hour).UnixMilli(), Resume: &resume, Persist: func(c Cursor) error { persisted = append(persisted, c); return nil }}) {
		if err != nil {
			scanErr = err
			break
		}
		if batch.Cursor.Complete {
			t.Fatal("incomplete scan yielded complete cursor")
		}
		for _, item := range batch.Sessions {
			ids = append(ids, item.ID)
		}
	}
	if !reflect.DeepEqual(ids, []string{"healthy"}) || scanErr == nil || len(persisted) == 0 {
		t.Fatalf("ids=%v error=%v persisted=%+v", ids, scanErr, persisted)
	}
	retry := persisted[len(persisted)-1]
	encoded, err := json.Marshal(retry)
	if err != nil {
		t.Fatal(err)
	}
	if DecodeCursor(encoded) != nil {
		t.Fatalf("failed scan retained a resumable checkpoint: %+v", retry)
	}
	for _, cursor := range persisted {
		if cursor.Complete {
			t.Fatal("incomplete scan persisted complete cursor")
		}
	}
	// The repaired root sorts above the last healthy batch with unchanged old mtimes.
	grokFile(t, old, "summary.json", `{"info":{"id":"repaired"},"chat_format_version":1,"created_at":"2026-09-01T00:00:00Z","last_active_at":"2026-09-29T11:00:00Z"}`, oldStamp)
	snapshot, err = Scan(t.Context(), Options{Home: home, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	ids = nil
	complete := false
	for batch, err := range Discover(t.Context(), DiscoverOptions{Snapshot: snapshot, MinActivityMs: now.Add(-24 * time.Hour).UnixMilli(), Resume: &retry, BatchFamilies: 1}) {
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range batch.Sessions {
			ids = append(ids, item.ID)
		}
		complete = batch.Cursor.Complete
	}
	if !reflect.DeepEqual(ids, []string{"repaired", "healthy"}) || !complete {
		t.Fatalf("retry ids=%v complete=%v", ids, complete)
	}
}

func TestGrokInvalidFamiliesNeverCompleteDiscovery(t *testing.T) {
	for _, scenario := range []string{"only malformed", "duplicate id", "missing parent", "conflicting parents", "cycle", "malformed linked child"} {
		t.Run(scenario, func(t *testing.T) {
			home := grokHome(t)
			a := grokSummary(t, "a", "", "", now)
			switch scenario {
			case "only malformed":
				grokFile(t, a, "summary.json", `{`, now)
			case "duplicate id":
				b := grokSummary(t, "b", "", "", now)
				grokFile(t, b, "summary.json", `{"info":{"id":"a"},"chat_format_version":1}`, now)
			case "missing parent":
				grokSummary(t, "b", "subagent", "absent", now)
			case "conflicting parents":
				grokSummary(t, "b", "subagent", "other", now)
				grokFile(t, a, "subagents/sub/meta.json", `{"child_session_id":"b"}`, now)
			case "cycle":
				grokSummary(t, "a", "subagent", "b", now)
				grokSummary(t, "b", "subagent", "a", now)
			case "malformed linked child":
				b := grokSummary(t, "b", "subagent", "a", now)
				grokFile(t, a, "subagents/sub/meta.json", `{"child_session_id":"b"}`, now)
				grokFile(t, b, "summary.json", `{`, now)
			}
			snapshot, err := Scan(t.Context(), Options{Home: home, Now: now})
			if err != nil {
				t.Fatal(err)
			}
			failed := false
			saved := 0
			for batch, err := range Discover(t.Context(), DiscoverOptions{Snapshot: snapshot, Persist: func(c Cursor) error {
				saved++
				if c.Complete || c.StartedAtMs != 0 {
					t.Fatal("failed scan persisted complete")
				}
				return nil
			}}) {
				if err != nil {
					failed = true
					break
				}
				if batch.Cursor.Complete || len(batch.Sessions) != 0 {
					t.Fatalf("invalid family published: %+v", batch)
				}
			}
			if !failed || saved == 0 {
				t.Fatalf("failed planning was acknowledged or checkpoint was not reset: failed=%v saved=%d", failed, saved)
			}
		})
	}
}
