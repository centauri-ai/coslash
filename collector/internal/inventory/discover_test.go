package inventory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestDiscoverUsesSmallFirstBatchThenRegularBatches(t *testing.T) {
	plan := &discoveryPlan{
		families:   make([]family, 10),
		firstBatch: 2,
		batch:      4,
		cursor:     Cursor{StartedAtMs: 1},
	}
	for i := range plan.families {
		plan.families[i] = family{agent: "test", id: string(rune('a' + i)), activityMs: int64(10 - i)}
	}
	var lastFamilies []string
	plan.run(context.Background(), func(batch Batch, err error) bool {
		if err != nil {
			t.Fatal(err)
		}
		lastFamilies = append(lastFamilies, batch.Cursor.Family)
		return true
	})
	want := []string{"b", "f", "j"}
	if len(lastFamilies) != len(want) {
		t.Fatalf("batch ends = %v, want %v", lastFamilies, want)
	}
	for i := range want {
		if lastFamilies[i] != want[i] {
			t.Fatalf("batch ends = %v, want %v", lastFamilies, want)
		}
	}
}

func TestPlanDiscoveryResumesBelowTheCursorAndRevisitsChangedFamilies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	projects := filepath.Join(home, ".claude", "projects", "p")
	file := func(id string) string { return filepath.Join(projects, id+".jsonl") }
	snapshot := &Snapshot{Files: []File{
		{Agent: vendors.AgentClaude, Path: file("a"), ModTimeMs: 300, Session: true, FamilyID: "a"},
		{Agent: vendors.AgentClaude, Path: file("b"), ModTimeMs: 200, Session: true, FamilyID: "b"},
		{Agent: vendors.AgentClaude, Path: file("c"), ModTimeMs: 100, Session: true, FamilyID: "c"},
		{Agent: vendors.AgentClaude, Path: file("d"), ModTimeMs: 50, Session: true, FamilyID: "d"},
	}}
	ids := func(plan *discoveryPlan) []string {
		var out []string
		for _, family := range plan.families {
			out = append(out, family.id)
		}
		return out
	}
	fresh, err := planDiscovery(context.Background(), DiscoverOptions{Snapshot: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(fresh); len(got) != 4 || got[0] != "a" || got[3] != "d" {
		t.Fatalf("fresh plan = %v", got)
	}
	// The earlier pass began at 250 and stopped after b. a changed since
	// (300 >= 250), so it is revisited; b is skipped; c and d continue.
	resumed, err := planDiscovery(context.Background(), DiscoverOptions{Snapshot: snapshot, Resume: &Cursor{StartedAtMs: 250, ActivityMs: 200, Agent: vendors.AgentClaude, Family: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(resumed); len(got) != 3 || got[0] != "a" || got[1] != "c" || got[2] != "d" {
		t.Fatalf("resumed plan = %v", got)
	}
	if resumed.cursor.StartedAtMs != 250 {
		t.Fatalf("resumed cursor = %+v", resumed.cursor)
	}
	completed, err := planDiscovery(context.Background(), DiscoverOptions{Snapshot: snapshot, Resume: &Cursor{StartedAtMs: 250, Complete: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.families) != 4 || completed.cursor.StartedAtMs == 250 {
		t.Fatalf("a completed cursor did not start a fresh pass: %+v", completed.cursor)
	}
	if DecodeCursor(json.RawMessage(`{"startedAtMs":0}`)) != nil || DecodeCursor(json.RawMessage(`nope`)) != nil || DecodeCursor(nil) != nil {
		t.Fatal("invalid cursors decoded")
	}
	if cursor := DecodeCursor(json.RawMessage(`{"startedAtMs":7,"activityMs":3,"agent":"claude","family":"x"}`)); cursor == nil || cursor.Family != "x" {
		t.Fatalf("cursor = %+v", cursor)
	}
}

func TestPlanDiscoveryRestartsWhenHubPlanChanges(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	projects := filepath.Join(home, ".claude", "projects", "p")
	snapshot := &Snapshot{Files: []File{
		{Agent: vendors.AgentClaude, Path: filepath.Join(projects, "a.jsonl"), ModTimeMs: 300, Session: true, FamilyID: "a"},
		{Agent: vendors.AgentClaude, Path: filepath.Join(projects, "b.jsonl"), ModTimeMs: 200, Session: true, FamilyID: "b"},
		{Agent: vendors.AgentClaude, Path: filepath.Join(projects, "c.jsonl"), ModTimeMs: 100, Session: true, FamilyID: "c"},
	}}
	resume := &Cursor{StartedAtMs: 250, PlanVersion: 1, ActivityMs: 200, Agent: vendors.AgentClaude, Family: "b"}
	plan, err := planDiscovery(ctx, DiscoverOptions{Snapshot: snapshot, PlanVersion: 2, Resume: resume})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.families) != 3 || plan.families[0].id != "a" || plan.families[2].id != "c" {
		t.Fatalf("new plan resumed from stale cursor: %+v", plan.families)
	}
}

func TestDiscoverKeepsTranscriptActivityDespiteOldFileMtime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	cutoff := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	stale := cutoff.Add(-time.Hour)
	claudeID := "11111111-2222-3333-4444-555555555555"
	codexID := "019f4dde-db5b-7100-bdc0-09b5aaaac56f"
	claudePath := filepath.Join(home, ".claude", "projects", "p", claudeID+".jsonl")
	codexPath := filepath.Join(home, ".codex", "sessions", "2026", "09", "04", "rollout-2026-09-04T10-00-00-"+codexID+".jsonl")
	for _, file := range []struct {
		path    string
		content string
	}{
		{claudePath, `{"sessionId":"` + claudeID + `","uuid":"row-1","cwd":"/repo","timestamp":"2026-09-04T10:00:00Z","type":"user","message":{"content":"hello"}}` + "\n"},
		{codexPath, `{"timestamp":"2026-09-04T10:00:00Z","type":"session_meta","payload":{"id":"` + codexID + `","session_id":"` + codexID + `"}}` + "\n" +
			`{"timestamp":"2026-09-04T10:01:00Z","type":"event_msg","payload":{"type":"user_message","message":"hello"}}` + "\n"},
	} {
		if err := os.MkdirAll(filepath.Dir(file.path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file.path, []byte(file.content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(file.path, stale, stale); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := Scan(t.Context(), Options{Home: home, OpenCodeDB: filepath.Join(home, "opencode.db")})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := DiscoverAll(t.Context(), DiscoverOptions{Snapshot: snapshot, MinActivityMs: cutoff.UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("discovered sessions = %+v", sessions)
	}
	activities := make(map[string]int64, len(sessions))
	for _, item := range sessions {
		activities[item.Agent+"\x00"+item.ID] = item.LastActivityTime
		if item.LastActivityTime < cutoff.UnixMilli() {
			t.Errorf("%s activity = %d, want at least %d", item.Agent, item.LastActivityTime, cutoff.UnixMilli())
		}
	}
	for _, key := range []string{vendors.AgentClaude + "\x00" + claudeID, vendors.AgentCodex + "\x00" + codexID} {
		if _, ok := activities[key]; !ok {
			t.Errorf("transcript family %q was not discovered", key)
		}
	}
}

func TestDiscoverYieldsNothingForAnEmptyHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	var persisted []Cursor
	batches := 0
	for batch, err := range Discover(context.Background(), DiscoverOptions{Persist: func(cursor Cursor) error {
		persisted = append(persisted, cursor)
		return nil
	}}) {
		if err != nil {
			t.Fatal(err)
		}
		batches++
		if len(batch.Sessions) != 0 || !batch.Cursor.Complete {
			t.Fatalf("batch = %+v", batch)
		}
	}
	if batches != 1 || len(persisted) != 1 || !persisted[0].Complete {
		t.Fatalf("batches = %d, persisted = %+v", batches, persisted)
	}
}
