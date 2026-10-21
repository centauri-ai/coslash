package grok

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlanFamiliesRejectsAmbiguousLineage(t *testing.T) {
	for _, scenario := range []string{"duplicate id", "two parents", "conflicting parent", "cycle", "missing parent"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			dirs := []string{filepath.Join(home, "a"), filepath.Join(home, "b")}
			writeSummary(t, dirs[0], `{"info":{"id":"a"},"chat_format_version":1}`)
			writeSummary(t, dirs[1], `{"info":{"id":"b"},"chat_format_version":1,"session_kind":"subagent"}`)
			meta := func(dir, child string) {
				t.Helper()
				path := filepath.Join(dir, "subagents", "sub")
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, "meta.json"), []byte(`{"child_session_id":"`+child+`"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "duplicate id":
				writeSummary(t, dirs[1], `{"info":{"id":"a"},"chat_format_version":1}`)
			case "two parents":
				dirs = append(dirs, filepath.Join(home, "c"))
				writeSummary(t, dirs[2], `{"info":{"id":"c"},"chat_format_version":1}`)
				meta(dirs[0], "b")
				meta(dirs[2], "b")
			case "conflicting parent":
				writeSummary(t, dirs[1], `{"info":{"id":"b"},"chat_format_version":1,"session_kind":"subagent","parent_session_id":"other"}`)
				meta(dirs[0], "b")
			case "missing parent":
				writeSummary(t, dirs[1], `{"info":{"id":"b"},"chat_format_version":1,"session_kind":"subagent","parent_session_id":"absent"}`)
			case "cycle":
				writeSummary(t, dirs[0], `{"info":{"id":"a"},"chat_format_version":1,"session_kind":"subagent","parent_session_id":"b"}`)
				writeSummary(t, dirs[1], `{"info":{"id":"b"},"chat_format_version":1,"session_kind":"subagent","parent_session_id":"a"}`)
			}
			if _, err := PlanFamiliesContext(t.Context(), dirs); err == nil {
				t.Fatal("ambiguous family accepted")
			}
		})
	}
}

func TestPlanFamiliesKeepsNestedChildrenAndIndependentForks(t *testing.T) {
	root := t.TempDir()
	dirs := []string{}
	for _, item := range []struct{ id, kind, parent string }{{"root", "", ""}, {"child", "subagent", "root"}, {"grandchild", "subagent_resume", ""}, {"fork", "fork", "root"}, {"worktree", "worktree", "root"}} {
		dir := filepath.Join(root, item.id)
		dirs = append(dirs, dir)
		writeSummary(t, dir, `{"info":{"id":"`+item.id+`"},"chat_format_version":1,"session_kind":"`+item.kind+`","parent_session_id":"`+item.parent+`"}`)
	}
	for _, link := range []struct{ parent, child string }{{"root", "child"}, {"child", "grandchild"}, {"root", "fork"}} {
		metaDir := filepath.Join(root, link.parent, "subagents", link.child)
		if err := os.MkdirAll(metaDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), []byte(`{"child_session_id":"`+link.child+`","description":"verified","output":"nested result"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	refs, err := PlanFamiliesContext(t.Context(), append(dirs, dirs[0]))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, ref := range refs {
		for _, dir := range ref.Dirs {
			got[ref.ID] = append(got[ref.ID], filepath.Base(dir))
		}
	}
	want := map[string][]string{"root": {"root", "child", "grandchild"}, "fork": {"fork"}, "worktree": {"worktree"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("families=%v", got)
	}
	for _, ref := range refs {
		if ref.ID != "root" {
			continue
		}
		selected, _, err := ParseFamiliesContext(t.Context(), []FamilyRef{ref})
		if err != nil || len(selected) != 3 || selected[0].Session.ID != "root" {
			t.Fatalf("selected family=%+v err=%v", selected, err)
		}
	}
	if _, _, err := ParseFamiliesContext(t.Context(), []FamilyRef{{ID: "root", Dirs: []string{dirs[0]}}}); err == nil {
		t.Fatal("unverified missing children accepted")
	}
	parsed, _, err := ParseDirsContext(t.Context(), dirs)
	if err != nil || len(parsed) != 5 {
		t.Fatalf("parsed=%v err=%v", parsed, err)
	}
	parents := map[string]string{}
	for _, p := range parsed {
		parents[p.Session.ID] = p.ParentID
		if p.Session.ID == "grandchild" && (p.Name != "verified" || p.Result != "nested result") {
			t.Fatalf("nested metadata=%+v", p)
		}
	}
	if !reflect.DeepEqual(parents, map[string]string{"root": "", "child": "root", "grandchild": "child", "fork": "", "worktree": ""}) {
		t.Fatalf("parents=%v", parents)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := PlanFamiliesContext(ctx, dirs); !errors.Is(err, context.Canceled) {
		t.Fatalf("plan cancel=%v", err)
	}
	if _, _, err := ParseFamiliesContext(ctx, refs); !errors.Is(err, context.Canceled) {
		t.Fatalf("planned parse cancel=%v", err)
	}
	if _, _, err := ParseDirsContext(ctx, dirs); !errors.Is(err, context.Canceled) {
		t.Fatalf("parse cancel=%v", err)
	}
}

func TestParseDirsUsesSuppliedFixtureWithoutRootRescan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	dir := filepath.Join(home, "sessions", "group", "finished")
	copyFixture(t, filepath.Join("testdata", "finished"), dir)
	writeSummary(t, filepath.Join(home, "sessions", "group", "unrelated"), `{`)
	parsed, _, err := ParseDirsContext(t.Context(), []string{dir})
	if err != nil || len(parsed) != 1 || parsed[0].Session.ID != "01a0f48a-42bb-7802-b584-f5def46d1e75" || len(parsed[0].Session.Tokens) != 1 {
		t.Fatalf("fixture=%+v err=%v", parsed, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	wrapped := &cancelOnCheck{Context: ctx, cancel: cancel, after: 1}
	if _, err := PlanFamiliesContext(wrapped, []string{dir}); !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-plan cancellation=%v", err)
	}
}

func TestDiscoveryMetadataStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := loadMetadataContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("metadata cancellation=%v", err)
	}
}
