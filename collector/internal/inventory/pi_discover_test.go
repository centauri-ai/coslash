package inventory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/vendors/pi"
)

func piDiscoveryHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_HOME", filepath.Join(home, "coslash"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_PI_SESSION_ROOTS", "")
	t.Setenv("OPENCODE_DB", filepath.Join(home, "missing.db"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("GROK_HOME", filepath.Join(home, "grok"))
	t.Chdir(home)
	return home
}

func piDiscoveryFile(t *testing.T, home, id, parent string, age time.Duration, rows ...string) string {
	t.Helper()
	root, err := pi.Root()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	header, err := json.Marshal(map[string]any{"type": "session", "version": 3, "id": id, "cwd": home, "parentSession": parent, "timestamp": now.Add(-age).Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	data := append(header, '\n')
	for _, row := range rows {
		data = append(data, []byte(row+"\n")...)
	}
	path := filepath.Join(root, "file-for-"+id+".jsonl")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := now.Add(-age)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	return path
}

const piDiscoveryPrompt = `{"type":"message","id":"u","timestamp":"2026-09-29T12:00:01Z","message":{"role":"user","content":"synthetic Pi session"}}`
const piDiscoveryResponse = `{"type":"message","id":"a","parentId":"u","timestamp":"2026-09-29T12:00:02Z","message":{"role":"assistant","model":"m","content":"copied","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"cost":{"total":1}}}}`

func TestPiDiscoveryKeepsForkEvidenceOutsideWindowAndBatch(t *testing.T) {
	home := piDiscoveryHome(t)
	oldPrompt := strings.ReplaceAll(piDiscoveryPrompt, "2026-09-29", "2026-09-19")
	oldResponse := strings.ReplaceAll(piDiscoveryResponse, "2026-09-29", "2026-09-19")
	parent := piDiscoveryFile(t, home, "parent", "", 10*24*time.Hour, oldPrompt, oldResponse)
	piDiscoveryFile(t, home, "fork.custom_01", parent, time.Hour, oldPrompt, oldResponse,
		`{"type":"message","id":"b","parentId":"a","timestamp":"2026-09-29T12:00:03Z","message":{"role":"assistant","model":"m","content":"new","usage":{"input":2,"output":1,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.2}}}}`)
	sources, err := pi.PlanSourcesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for batch, err := range Discover(t.Context(), DiscoverOptions{Pi: sources, MinActivityMs: now.Add(-3 * 24 * time.Hour).UnixMilli(), BatchFamilies: 1}) {
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range batch.Sessions {
			count++
			if s.Agent != "pi" || s.ID != "fork.custom_01" || s.ParentSessionID != "" || s.Cost == nil || *s.Cost != 0.2 || len(s.Subagents) != 0 {
				t.Fatalf("independent fork = %+v", s)
			}
		}
		if len(batch.Sessions) > 0 && (len(batch.PiInputs) != 1 || batch.PiInputs[0].ID != "fork.custom_01" || len(batch.PiInputs[0].Files) != 2 || batch.ContentBytes["pi\x00fork.custom_01"] <= 0) {
			t.Fatalf("lost source planning context: %+v", batch)
		}
	}
	if count != 1 {
		t.Fatalf("fork count = %d", count)
	}
}

func TestPiDiscoveryUsesRecordedActivityInsteadOfMtimeForWindow(t *testing.T) {
	home := piDiscoveryHome(t)
	piDiscoveryFile(t, home, "restored", "", 10*24*time.Hour, piDiscoveryPrompt, piDiscoveryResponse)
	piDiscoveryFile(t, home, "actually-old", "", 10*24*time.Hour,
		strings.ReplaceAll(piDiscoveryPrompt, "2026-09-29", "2026-09-19"),
		strings.ReplaceAll(piDiscoveryResponse, "2026-09-29", "2026-09-19"))
	sources, err := pi.PlanSourcesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cutoff := now.Add(-3 * 24 * time.Hour).UnixMilli()
	var ids []string
	for batch, err := range Discover(t.Context(), DiscoverOptions{Pi: sources, MinActivityMs: cutoff, BatchFamilies: 1}) {
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range batch.Sessions {
			ids = append(ids, item.ID)
			if item.LastActivityTime != 1790683202000 || len(batch.PiInputs) != 1 || batch.PiInputs[0].ID != item.ID {
				t.Fatalf("wrong recorded-activity source = %+v, inputs=%+v", item, batch.PiInputs)
			}
		}
	}
	if len(ids) != 1 || ids[0] != "restored" {
		t.Fatalf("recorded-activity window selected %v", ids)
	}
}

func TestPiDiscoveryRevisitsUnchangedMtimeAfterCursor(t *testing.T) {
	home := piDiscoveryHome(t)
	piDiscoveryFile(t, home, "root", "", time.Hour, piDiscoveryPrompt)
	sources, err := pi.PlanSourcesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	resume := &Cursor{StartedAtMs: now.UnixMilli(), ActivityMs: now.Add(-time.Hour).UnixMilli(), Agent: "pi", Family: "root"}
	sessions, err := DiscoverAll(t.Context(), DiscoverOptions{Pi: sources, Resume: resume})
	if err != nil || len(sessions) != 1 || sessions[0].ID != "root" {
		t.Fatalf("leaf-only refresh could be hidden by cursor: %+v, %v", sessions, err)
	}
}

func TestPiDiscoveryFailureKeepsHealthySourceAndIncompleteCursor(t *testing.T) {
	home := piDiscoveryHome(t)
	piDiscoveryFile(t, home, "bad", "", 0, `{invalid}`)
	piDiscoveryFile(t, home, "good", "", time.Hour, piDiscoveryPrompt)
	sources, err := pi.PlanSourcesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var failures, healthy int
	for batch, err := range Discover(t.Context(), DiscoverOptions{Pi: sources, BatchFamilies: 1}) {
		if err != nil {
			failures++
			continue
		}
		if batch.Cursor.Complete {
			t.Fatal("malformed selected source made discovery authoritative")
		}
		for _, s := range batch.Sessions {
			if s.ID == "good" {
				healthy++
			}
		}
	}
	if failures != 1 || healthy != 1 {
		t.Fatalf("healthy=%d failures=%d", healthy, failures)
	}
}
