package inventory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

var now = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func write(t *testing.T, path string, size int, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := now.Add(-age)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func fixtureHome(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	claudeRoot := "11111111-1111-1111-1111-111111111111"
	write(t, filepath.Join(home, ".claude", "projects", "p", claudeRoot+".jsonl"), 100, 2*time.Hour)
	write(t, filepath.Join(home, ".claude", "projects", "p", claudeRoot, "subagents", "agent-a.jsonl"), 50, 30*time.Minute)
	write(t, filepath.Join(home, ".claude", "projects", "p", claudeRoot, "subagents", "agent-a.meta.json"), 5, 30*time.Minute)
	write(t, filepath.Join(home, ".claude", "projects", "p", claudeRoot, "subagents", "workflows", "run", "journal.jsonl"), 7, time.Minute)
	write(t, filepath.Join(home, ".claude", "projects", "q", "22222222-2222-2222-2222-222222222222.jsonl"), 300, 5*24*time.Hour)
	codexID := "019f4dde-db5b-7100-bdc0-09b5aaaac56f"
	write(t, filepath.Join(home, ".codex", "sessions", "2026", "09", "29", "rollout-2026-09-29T10-00-00-"+codexID+".jsonl"), 1000, 20*24*time.Hour)
	write(t, filepath.Join(home, ".codex", "archived_sessions", "rollout-2026-09-29T10-00-00-"+codexID+".jsonl"), 999, 20*24*time.Hour)
	write(t, filepath.Join(home, ".codex", "sessions", "2026", "08", "01", "rollout-2026-08-01T10-00-00-029f4dde-db5b-7100-bdc0-09b5aaaac56f.jsonl"), 11<<20, 60*24*time.Hour)
	cursorID := "33333333-3333-4333-8333-333333333333"
	write(t, filepath.Join(home, ".cursor", "projects", "w", "agent-transcripts", cursorID, cursorID+".jsonl"), 40, 2*24*time.Hour)
	write(t, filepath.Join(home, ".cursor", "projects", "w", "agent-transcripts", cursorID, "subagents", "44444444-4444-4444-8444-444444444444.jsonl"), 60, 2*24*time.Hour)
	write(t, filepath.Join(home, ".cursor", "projects", "w", "agent-transcripts", "scratch", "scratch.jsonl"), 9, time.Hour)
	db := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	write(t, db, 500, 12*time.Hour)
	return home, db
}

func TestScanCountsOnlyDiscoverableFilesAndBucketsFamilies(t *testing.T) {
	home, db := fixtureHome(t)
	tracker := &Tracker{}
	snapshot, err := Scan(context.Background(), Options{Home: home, OpenCodeDB: db, Now: now, Tracker: tracker})
	if err != nil {
		t.Fatal(err)
	}
	if tracker.Running() || tracker.FilesSoFar() != int64(len(snapshot.Files)) {
		t.Fatalf("tracker = running %v, %d files; snapshot has %d", tracker.Running(), tracker.FilesSoFar(), len(snapshot.Files))
	}
	report := snapshot.Inventory()
	if report.Files != 8 || report.Bytes != 100+50+300+1000+(11<<20)+40+60+500 {
		t.Fatalf("files = %d, bytes = %d", report.Files, report.Bytes)
	}
	if report.LargestBytes != 11<<20 || report.FilesOver10MiB != 1 {
		t.Fatalf("largest = %d, over 10 MiB = %d", report.LargestBytes, report.FilesOver10MiB)
	}
	agents := map[string][2]int64{}
	for _, agent := range report.Agents {
		agents[agent.Agent] = [2]int64{agent.Files, agent.Bytes}
	}
	if agents[vendors.AgentClaude] != [2]int64{3, 450} || agents[vendors.AgentCodex] != [2]int64{2, 1000 + 11<<20} ||
		agents[vendors.AgentCursor] != [2]int64{2, 100} || agents[vendors.AgentOpenCode] != [2]int64{1, 500} {
		t.Fatalf("agents = %v", agents)
	}
	w := report.Windows
	// The Claude family is 30 min old (its subagent); the OpenCode database
	// counts bytes only; sessions: claude 2, codex 2 (one archived duplicate
	// skipped), cursor 1.
	if w.H24 != (windowOf(1, 150+500)) || w.D3 != windowOf(2, 150+500+100) || w.D7 != windowOf(3, 150+500+100+300) ||
		w.D10 != w.D7 ||
		w.D30 != windowOf(4, 150+500+100+300+1000) || w.All != windowOf(5, report.Bytes) {
		t.Fatalf("windows = %+v", w)
	}
	if strings.Join(snapshot.Missing, ",") != "" {
		t.Fatalf("missing = %v", snapshot.Missing)
	}
	families := snapshot.Families()
	if families[0].Agent != vendors.AgentClaude || families[0].Sessions != 1 || len(families[0].Files) != 2 {
		t.Fatalf("newest family = %+v", families[0])
	}
	for index := 1; index < len(families); index++ {
		if families[index].ActivityMs > families[index-1].ActivityMs {
			t.Fatalf("families not newest first: %+v", families)
		}
	}
}

func TestTenDayWindowBucketIncludesNineDaySessions(t *testing.T) {
	home, db := fixtureHome(t)
	write(t, filepath.Join(home, ".claude", "projects", "ten-day", "55555555-5555-4555-8555-555555555555.jsonl"), 70, 9*24*time.Hour)
	snapshot, err := Scan(context.Background(), Options{Home: home, OpenCodeDB: db, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	windows := snapshot.Inventory().Windows
	if windows.D10.Sessions != windows.D7.Sessions+1 || windows.D10.Bytes != windows.D7.Bytes+70 {
		t.Fatalf("10-day window = %+v; 7-day window = %+v", windows.D10, windows.D7)
	}
	if windows.D10.Sessions > windows.D30.Sessions || windows.D10.Bytes > windows.D30.Bytes {
		t.Fatalf("10-day bucket exceeds 30-day bucket: %+v", windows)
	}
}

func windowOf(sessions, bytes int64) struct {
	Sessions int64 `json:"sessions"`
	Bytes    int64 `json:"bytes"`
} {
	return struct {
		Sessions int64 `json:"sessions"`
		Bytes    int64 `json:"bytes"`
	}{sessions, bytes}
}

func TestScanReportsMissingRoots(t *testing.T) {
	home := t.TempDir()
	snapshot, err := Scan(context.Background(), Options{Home: home, OpenCodeDB: filepath.Join(home, "none.db"), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(snapshot.Missing, ",") != "claude,codex,cursor,opencode" || len(snapshot.Files) != 0 {
		t.Fatalf("missing = %v, files = %d", snapshot.Missing, len(snapshot.Files))
	}
	report := snapshot.Inventory()
	if report.Files != 0 || len(report.Agents) != 0 || report.Windows.All.Sessions != 0 {
		t.Fatalf("empty report = %+v", report)
	}
}

// The check-in inventory must carry counts, bytes, closed agent names and
// one timestamp: never a path, title or other free text.
func TestInventoryJSONIsContentFree(t *testing.T) {
	home, db := fixtureHome(t)
	snapshot, err := Scan(context.Background(), Options{Home: home, OpenCodeDB: db, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot.Inventory())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), home) || strings.Contains(string(encoded), ".jsonl") {
		t.Fatalf("inventory leaks paths: %s", encoded)
	}
	allowed := map[string]bool{
		"scannedAt": true, "durationMs": true, "files": true, "bytes": true, "largestBytes": true, "filesOver10MiB": true,
		"agents": true, "windows": true, "agent": true, "sessions": true, "h24": true, "d3": true, "d7": true, "d10": true, "d30": true, "all": true,
	}
	agentNames := map[string]bool{vendors.AgentClaude: true, vendors.AgentCodex: true, vendors.AgentCursor: true, vendors.AgentOpenCode: true}
	var document any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	var check func(key string, value any)
	check = func(key string, value any) {
		switch typed := value.(type) {
		case map[string]any:
			for name, child := range typed {
				if !allowed[name] {
					t.Fatalf("unexpected inventory field %q", name)
				}
				check(name, child)
			}
		case []any:
			for _, child := range typed {
				check(key, child)
			}
		case string:
			if key == "scannedAt" {
				if _, err := time.Parse(time.RFC3339, typed); err != nil {
					t.Fatalf("scannedAt = %q", typed)
				}
			} else if key != "agent" || !agentNames[typed] {
				t.Fatalf("free text in inventory: %s=%q", key, typed)
			}
		case float64:
			if typed < 0 {
				t.Fatalf("negative %s", key)
			}
		default:
			t.Fatalf("unexpected value %T for %s", value, key)
		}
	}
	check("", document)
}
