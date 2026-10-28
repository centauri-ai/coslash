package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestV1ObservedSkills(t *testing.T) {
	parsed := parseToolParts(t,
		toolPart("skill", `{"status":"completed","input":{"name":"review"},"output":"  # Review\n\nKeep formatting.\n"}`),
		toolPart("skill", `{"status":"completed","input":{"name":"review"},"output":"  # Review\n\nKeep formatting.\n"}`),
		toolPart("skill", `{"status":"completed","input":{"name":"review"},"output":"Different body"}`),
		toolPart("skill", `{"status":"completed","metadata":{"name":"metadata-name"},"output":"metadata body"}`),
		toolPart("skill", `{"status":"completed","metadata":{"dir":"/skills/fallback"},"output":"fallback body"}`),
		toolPart("skill", `{"status":"completed","input":{"name":"unknown"}}`),
		toolPart("read", `{"status":"completed","input":{"filePath":"/skills/read-skill/SKILL.md"},"output":"# Captured file\n"}`),
	)
	assertSkillsJSON(t, parsed.Session.SessionDetails, `[{"name":"review","content":"  # Review\n\nKeep formatting.\n"},{"name":"review","content":"Different body"},{"name":"metadata-name","content":"metadata body"},{"name":"fallback","content":"fallback body"},{"name":"unknown","content":""},{"name":"read-skill","content":"# Captured file\n"}]`)
}

func TestSkillsIgnoreUnsuccessfulAndNonLoadEvidence(t *testing.T) {
	parsed := parseToolParts(t,
		`{"type":"text","text":"Loaded /skills/mention/SKILL.md"}`,
		toolPart("skill", `{"status":"error","input":{"name":"failed"},"output":"failed"}`),
		toolPart("skill", `{"status":"running","input":{"name":"running"}}`),
		toolPart("skill", `{"status":"pending","input":{"name":"pending"}}`),
		toolPart("skill", `{"status":"completed","output":"no name"}`),
		toolPart("list", `{"status":"completed","output":"/skills/listed/SKILL.md"}`),
		toolPart("write", `{"status":"completed","input":{"filePath":"/skills/written/SKILL.md","content":"write"}}`),
		toolPart("read", `{"status":"error","input":{"filePath":"/skills/failed/SKILL.md"},"output":"denied"}`),
		toolPart("read", `{"status":"completed","input":{"filePath":"/skills/not-skill/README.md"},"output":"mention SKILL.md"}`),
		toolPart("bash", `{"status":"completed","input":{"command":"cat /skills/shell/SKILL.md"},"output":"shell"}`),
	)
	assertSkillsJSON(t, parsed.Session.SessionDetails, `null`)
	if parsed.Session.Errors != 2 {
		t.Fatalf("errors = %d, want 2", parsed.Session.Errors)
	}
}

func TestV2FamilySkillsAndBackupCapture(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, "opencode.db")
	t.Setenv("OPENCODE_DB", path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, statement := range []string{
		`CREATE TABLE session_v2 (id TEXT, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_created INTEGER, time_updated INTEGER, time_archived INTEGER)`,
		`CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`,
		`INSERT INTO session_v2 VALUES ('root', NULL, '/work', 'root', NULL, NULL, NULL, NULL, 0, 1, 2, NULL)`,
		`INSERT INTO session_v2 VALUES ('child', 'root', '/work', 'child', NULL, NULL, NULL, NULL, 0, 1, 2, NULL)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"root", "child"} {
		data := fmt.Sprintf(`{"content":[{"type":"tool","name":"skill","state":{"status":"streaming","input":"partial"}},{"type":"tool","name":"skill","state":{"status":"completed","input":{"name":%q},"content":[{"type":"text","text":"  first\n"},{"type":"image"},{"type":"text","text":"second\n"}]}}],"time":{"created":1,"completed":2}}`, id)
		if _, err := db.Exec(`INSERT INTO session_message VALUES (?, ?, 'assistant', 1, 1, 2, ?)`, id, id, data); err != nil {
			t.Fatal(err)
		}
	}
	parsed, skipped, err := load(db, activeFamiliesQuery+familyOrder)
	if err != nil || len(skipped) != 0 || len(parsed) != 2 {
		t.Fatalf("parsed %d, skipped %v, error %v", len(parsed), skipped, err)
	}
	for _, member := range parsed {
		assertSkillsJSON(t, member.Session.SessionDetails, fmt.Sprintf(`[{"name":%q,"content":"  first\n\nsecond\n"}]`, member.Session.ID))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	captured, err := CaptureFamilyContext(context.Background(), "root")
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if len(captured.Parsed) != len(parsed) {
		t.Fatalf("captured %d members, want %d", len(captured.Parsed), len(parsed))
	}
	for i, member := range captured.Parsed {
		if !json.Valid(captured.Rows[member.Session.ID]) {
			t.Fatal("invalid backup rows")
		}
		if member.Session.ID != parsed[i].Session.ID || !reflect.DeepEqual(member.Session.SessionDetails, parsed[i].Session.SessionDetails) {
			t.Fatal("backup capture parser differs from family parser")
		}
	}
}

func assertSkillsJSON(t *testing.T, details session.SessionDetails, want string) {
	t.Helper()
	var expected []session.Skill
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(details.Skills, expected) {
		t.Fatalf("skills = %#v, want %s", details.Skills, want)
	}
}
