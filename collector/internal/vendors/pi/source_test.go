package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSourceCustomIdentityDedupAndConflict(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_HOME", t.TempDir())
	root := filepath.Join(agentDir, "sessions")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "unrelated filename.jsonl")
	if err := os.WriteFile(path, fixture(t), 0600); err != nil {
		t.Fatal(err)
	}
	path, _ = filepath.EvalSymlinks(path)
	t.Setenv("COSLASH_PI_SESSION_ROOTS", root+string(os.PathListSeparator)+root)
	items, _, err := Collect(0)
	if err != nil || len(items) != 1 {
		t.Fatalf("source: %v %d", err, len(items))
	}
	facts, err := GetSessionFacts("custom.session_01")
	if err != nil || facts == nil || facts.ParentID != "" || facts.Session.TranscriptPath != path || !facts.Session.TokensUnavailable || !facts.Session.CostUnavailable {
		t.Fatalf("basic source facts: %v %+v", err, facts)
	}
	family, _, err := GetSessionFamily("custom.session_01")
	if err != nil || len(family) != 1 {
		t.Fatal("fork turned into family/subagent")
	}
	older := bytes.Replace(fixture(t), []byte(`"version":3`), []byte(`"version":2`), 1)
	if err := os.WriteFile(filepath.Join(root, "older.jsonl"), older, 0600); err != nil {
		t.Fatal(err)
	}
	items, _, err = Collect(0)
	if err != nil || len(items) != 1 {
		t.Fatal("unsupported file blocked supported collection")
	}
	if health := Health(); health.Sessions != 1 || health.Missing || health.SkippedTotal != 1 || !strings.Contains(health.Skipped[0].Error, "unsupported Pi transcript schema") {
		t.Fatalf("unsupported schema health: %+v", health)
	}
	if err := os.WriteFile(filepath.Join(root, "duplicate.jsonl"), fixture(t), 0600); err != nil {
		t.Fatal(err)
	}
	items, _, err = Collect(0)
	if err != nil || len(items) != 0 {
		t.Fatal("conflicting identity selected arbitrarily")
	}
	if health := Health(); health.Sessions != 0 || health.SkippedTotal < 2 {
		t.Fatalf("missing conflict health: %+v", health)
	}
	if _, err := GetSessionFacts("custom.session_01"); err == nil {
		t.Fatal("ambiguous facts not diagnosed")
	}
}

func TestProjectSettingsDiscovery(t *testing.T) {
	agentDir, cwd, override := t.TempDir(), filepath.Join(t.TempDir(), `project\with spaces`), t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_PI_SESSION_ROOTS", "")
	t.Setenv("COSLASH_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0700); err != nil {
		t.Fatal(err)
	}
	settings, _ := json.Marshal(map[string]string{"sessionDir": override})
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "settings.json"), settings, 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(agentDir, "sessions")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	cwdJSON, err := json.Marshal(cwd)
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Replace(fixture(t), []byte(`"/project with spaces"`), cwdJSON, 1)
	if err := os.WriteFile(filepath.Join(root, "known.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	other := bytes.Replace(data, []byte("custom.session_01"), []byte("override.session"), 1)
	if err := os.WriteFile(filepath.Join(override, "override.jsonl"), other, 0600); err != nil {
		t.Fatal(err)
	}
	items, _, err := Collect(0)
	if err != nil || len(items) != 2 {
		t.Fatalf("project override discovery: %v %d", err, len(items))
	}
}

func TestUnknownOpaqueEntryCollection(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_PI_SESSION_ROOTS", "")
	t.Setenv("COSLASH_HOME", t.TempDir())
	root := filepath.Join(agentDir, "sessions")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	data := bytes.Replace(fixture(t), []byte(`"unknownPayload":{"keep":true}`), []byte(`"unknownPayload":{"keep":true},"message":"opaque text","provider":[1],"model":{"opaque":true}`), 1)
	path := filepath.Join(root, "opaque.jsonl")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	items, _, err := Collect(0)
	if err != nil || len(items) != 1 {
		t.Fatalf("unknown opaque entry excluded session: error=%v sessions=%d health=%+v", err, len(items), Health())
	}
	parsed, err := parseTranscript(path)
	if err != nil || len(parsed.Entries) != 14 || !bytes.Contains(parsed.Entries[13].Raw, []byte(`"message":"opaque text"`)) {
		t.Fatalf("opaque entry not preserved: %v", err)
	}
	if parsed.Entries[13].Usage != nil {
		t.Fatal("invented usage for unknown entry")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(data, after) {
		t.Fatal("opaque transcript changed")
	}
}

func TestRuntimeOverrideSiblingCollection(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_PI_SESSION_ROOTS", "")
	home, override := t.TempDir(), t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	observed := filepath.Join(override, "observed session.jsonl")
	if err := os.WriteFile(observed, fixture(t), 0600); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(override, "exited sibling.jsonl")
	if err := os.WriteFile(sibling, bytes.Replace(fixture(t), []byte("custom.session_01"), []byte("exited.sibling"), 1), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "pi-history"), 0700); err != nil {
		t.Fatal(err)
	}
	record := RuntimeRecord{Version: 1, RuntimeID: "observed-owner", PID: os.Getpid(), ProcessStartIdentity: "terminated", StartedAtMs: 1, SessionID: "custom.session_01", TranscriptPath: observed, WorkState: "idle", Sequence: 1, UpdatedAtMs: 1}
	data, _ := json.Marshal(runtimeEvidence{Record: record, Exited: true})
	if err := os.WriteFile(filepath.Join(home, "pi-history", "observed.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	paths, err := RuntimeTranscriptPaths()
	if err != nil || len(paths) != 1 {
		t.Fatalf("runtime fixture: %v %v", paths, err)
	}
	items, _, err := Collect(0)
	if err != nil || len(items) != 2 {
		t.Fatalf("runtime override omitted historical sibling: error=%v sessions=%d", err, len(items))
	}
	ids := map[string]bool{}
	for _, item := range items {
		ids[item.Session.ID] = true
	}
	if !ids["custom.session_01"] || !ids["exited.sibling"] {
		t.Fatalf("missing identity: %v", ids)
	}
	files, err := Files()
	if err != nil || len(files) != 2 {
		t.Fatalf("runtime path and root not deduplicated: %v %v", files, err)
	}
}

func TestPiCollectionHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := CollectContext(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("collection cancellation = %v", err)
	}
	if _, err := parseTranscriptContext(ctx, "missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("parse cancellation = %v", err)
	}
}

func TestProjectedSourceCollection(t *testing.T) {
	root := userRow("r", "", "shared", "2026-01-01T00:00:01Z")
	parent := projectionFixture(t, "collectedParent", "", root, assistantRow("a", "r", "old", 2), assistantRow("b", "r", "chosen", 3))
	clone := projectionFixture(t, "collectedClone", parent.Path, root, assistantRow("b", "r", "chosen", 3), assistantRow("new", "b", "new", 7))
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("COSLASH_HOME", t.TempDir())
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_PI_SESSION_ROOTS", strings.Join([]string{filepath.Dir(parent.Path), filepath.Dir(clone.Path)}, string(os.PathListSeparator)))
	items, _, err := Collect(0)
	if err != nil || len(items) != 2 {
		t.Fatalf("collection %d: %v", len(items), err)
	}
	for _, p := range items {
		if p.Session.ID == clone.Header.ID {
			if p.Session.CostUnavailable || *p.RecordedCost != 7 || *p.Session.Summary != "new" || len(p.Session.Digest) != 3 || p.ParentID != "" {
				t.Fatalf("projected fork %#v", p)
			}
		}
	}
	facts, err := GetSessionFacts(clone.Header.ID)
	if err != nil || facts == nil || *facts.RecordedCost != 7 {
		t.Fatalf("facts %v %v", facts, err)
	}
}

func TestRuntimeLeafChangesCollectedCurrentStateOnly(t *testing.T) {
	root := userRow("r", "", "shared", "2026-01-01T00:00:01Z")
	tr := projectionFixture(t, "runtimeBranches", "", root, assistantRow("a", "r", "earlier branch", 2), assistantRow("b", "r", "last branch", 3))
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_PI_SESSION_ROOTS", filepath.Dir(tr.Path))
	record := testRecord()
	record.SessionID = tr.Header.ID
	record.TranscriptPath = tr.Path
	identity, err := ProcessStartIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	record.ProcessStartIdentity = identity
	if err := os.MkdirAll(filepath.Join(home, "pi-runtime"), 0700); err != nil {
		t.Fatal(err)
	}
	for n, leaf := range []string{"a", "b"} {
		record.LeafID = &leaf
		record.Sequence = int64(n + 1)
		bytes, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "pi-runtime", "owner.json"), bytes, 0600); err != nil {
			t.Fatal(err)
		}
		facts, err := GetSessionFacts(tr.Header.ID)
		if err != nil || facts == nil {
			t.Fatalf("facts %v", err)
		}
		want := "earlier branch"
		if leaf == "b" {
			want = "last branch"
		}
		if *facts.Session.Summary != want || *facts.RecordedCost != 5 || len(facts.Session.Digest) != 3 {
			t.Fatalf("leaf %s: %#v", leaf, facts.Session)
		}
		for _, row := range facts.Session.Digest {
			if row.Active == nil || *row.Active != (row.SourceEntryID == "r" || row.SourceEntryID == leaf) {
				t.Fatalf("active row %#v", row)
			}
		}
	}
}

func TestDiscoveryBoundsAndUnreadableRoot(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_PI_SESSION_ROOTS", "")
	t.Setenv("COSLASH_HOME", t.TempDir())
	root, _ := Root()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "huge.jsonl")
	data := []byte(`{"type":"session","version":3,"id":"huge","cwd":"` + strings.Repeat("x", maxTranscriptRecordBytes) + `"}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	scan, _, err := discoverContext(context.Background())
	if err != nil || scan.SkippedTotal != 1 || !strings.Contains(scan.Skipped[0].Error, "record limit") {
		t.Fatalf("bounded discovery: %v skipped=%d", err, scan.SkippedTotal)
	}
	if err := os.Chmod(root, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0700) })
	if _, err := os.ReadDir(root); err == nil {
		t.Skip("permissions not enforced")
	}
	health := Health()
	if health.Missing || health.SkippedTotal == 0 {
		t.Fatalf("unreadable source reported missing: %+v", health)
	}
}

func TestIncrementalCollectionSkipsOldBodiesAndKeepsForkParents(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_HOME", t.TempDir())
	root := userRow("r", "", "shared", "2026-01-01T00:00:01Z")
	parent := projectionFixture(t, "oldParent", "", root, assistantRow("a", "r", "old", 6))
	fork := projectionFixture(t, "recentFork", parent.Path, root, assistantRow("a", "r", "old", 6), assistantRow("new", "a", "new", 2))
	archive := filepath.Join(filepath.Dir(parent.Path), "unneeded.jsonl")
	if err := os.WriteFile(archive, []byte(`{"type":"session","version":3,"id":"unneeded"}`+"\nmalformed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	old, recent := time.Unix(1, 0), time.Unix(10, 0)
	for _, path := range []string{parent.Path, archive} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(fork.Path, recent, recent); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COSLASH_PI_SESSION_ROOTS", strings.Join([]string{filepath.Dir(parent.Path), filepath.Dir(fork.Path)}, string(os.PathListSeparator)))
	items, _, err := Collect(5000)
	if err != nil || len(items) != 1 || items[0].Session.ID != "recentFork" || items[0].RecordedCost == nil || *items[0].RecordedCost != 2 {
		t.Fatalf("incremental fork: %v %#v", err, items)
	}
	_, scan, _, err := readSessionsSinceContext(context.Background(), 5000, nil)
	if err != nil || scan.SkippedTotal != 0 {
		t.Fatalf("old body parsed: %v %+v", err, scan)
	}
	duplicate := filepath.Join(filepath.Dir(parent.Path), "duplicate.jsonl")
	data, err := os.ReadFile(fork.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(duplicate, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(duplicate, old, old); err != nil {
		t.Fatal(err)
	}
	items, _, err = Collect(5000)
	if err != nil || len(items) != 0 {
		t.Fatalf("old duplicate identity accepted: %v %d", err, len(items))
	}
}

func TestProjectSettingsBoundsAndBareTilde(t *testing.T) {
	agent, project, home := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_PI_SESSION_ROOTS", "")
	t.Setenv("COSLASH_HOME", t.TempDir())
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(project, ".pi"), 0700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(project, ".pi", "settings.json")
	if err := os.WriteFile(settings, []byte(`{"sessionDir":"~"}`), 0600); err != nil {
		t.Fatal(err)
	}
	root, _ := Root()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	cwd, _ := json.Marshal(project)
	data := bytes.Replace(fixture(t), []byte(`"/project with spaces"`), cwd, 1)
	if err := os.WriteFile(filepath.Join(root, "known.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "home.jsonl"), bytes.Replace(data, []byte("custom.session_01"), []byte("homeSession"), 1), 0600); err != nil {
		t.Fatal(err)
	}
	items, _, err := Collect(0)
	if err != nil || len(items) != 2 {
		t.Fatalf("bare tilde collection: %v %d", err, len(items))
	}
	if err := os.WriteFile(settings, []byte(`{"sessionDir":"`+strings.Repeat("x", 1<<20)+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	health := Health()
	if health.Sessions != 1 || health.SkippedTotal != 1 || !strings.Contains(health.Skipped[0].Error, "project settings read failed") {
		t.Fatalf("settings bound: sessions=%d skipped=%d", health.Sessions, health.SkippedTotal)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readProjectSettings(ctx, settings); !errors.Is(err, context.Canceled) {
		t.Fatalf("settings cancellation: %v", err)
	}
	if _, err := readTranscriptHeader(ctx, filepath.Join(root, "known.jsonl")); !errors.Is(err, context.Canceled) {
		t.Fatalf("header cancellation: %v", err)
	}
}

func TestIncrementalCollectionKeepsExternalIntermediateAncestor(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_HOME", t.TempDir())
	r := userRow("r", "", "shared", "2026-01-01T00:00:01Z")
	g := projectionFixture(t, "grandparent", "", r, assistantRow("a", "r", "old", 6))
	p := projectionFixture(t, "externalParent", g.Path, r, assistantRow("a", "r", "old", 6), assistantRow("b", "a", "parent", 2))
	f := projectionFixture(t, "recentFork", p.Path, r, assistantRow("a", "r", "old", 6), assistantRow("b", "a", "parent", 2), assistantRow("c", "b", "own", 3))
	for _, x := range []string{g.Path, p.Path} {
		if err := os.Chtimes(x, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(f.Path, time.Unix(10, 0), time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COSLASH_PI_SESSION_ROOTS", strings.Join([]string{filepath.Dir(g.Path), filepath.Dir(f.Path)}, string(os.PathListSeparator)))
	items, _, err := Collect(5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%d", len(items))
	}
	if items[0].RecordedCost == nil || *items[0].RecordedCost != 3 {
		t.Fatalf("valid complete lineage lost: cost=%v unavailable=%v", items[0].RecordedCost, items[0].Session.CostUnavailable)
	}

	// A conflicting discovered ancestor must remain blocked even through an external parent.
	data, err := os.ReadFile(g.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(g.Path), "duplicate.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	items, _, err = Collect(5000)
	if err != nil || len(items) != 1 || !items[0].Session.CostUnavailable || items[0].RecordedCost != nil {
		t.Fatalf("conflicting ancestor bypassed: %v %#v", err, items)
	}
}
