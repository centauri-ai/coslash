package inventory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

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
