package opencode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
	_ "modernc.org/sqlite"
)

func TestLoadContextStopsBeforeOpeningTransaction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	parsed, skipped, err := loadContext(ctx, nil, "unused")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if parsed != nil || skipped != nil {
		t.Fatalf("parsed = %#v, skipped = %#v; want nil results", parsed, skipped)
	}
}

func TestNewSessionFactsLoaderFromDBIndexesActiveRoots(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec(`CREATE TABLE session (id TEXT, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_updated INTEGER, time_archived INTEGER)`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO session (id, parent_id, directory) VALUES ('root', NULL, '/work')`,
		`INSERT INTO session (id, parent_id, directory) VALUES ('child', 'root', '/work')`,
		`INSERT INTO session (id, parent_id, directory, time_archived) VALUES ('archived', NULL, '/work', 1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	load, err := newSessionFactsLoader(db)
	if err != nil {
		t.Fatal(err)
	}
	root, err := load("root")
	if err != nil {
		t.Fatal(err)
	}
	if root == nil || root.Session.ID != "root" || root.Session.WorkingDirectory != "/work" {
		t.Fatalf("root = %#v", root)
	}
	for _, id := range []string{"child", "archived", "missing"} {
		found, err := load(id)
		if err != nil {
			t.Fatal(err)
		}
		if found != nil {
			t.Fatalf("load(%q) = %#v, want nil", id, found)
		}
	}
}

func TestMixedOpenCodeSchemasPreferV2AndKeepV1(t *testing.T) {
	db := testDB(t)
	for _, statement := range []string{
		`CREATE TABLE session (id TEXT, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_updated INTEGER, time_archived INTEGER)`,
		`CREATE TABLE session_v2 (id TEXT, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_updated INTEGER, time_archived INTEGER)`,
		`CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, data TEXT)`,
		`INSERT INTO session VALUES ('legacy', NULL, '/v1', 'old', NULL, NULL, NULL, NULL, 0, 100, NULL)`,
		`INSERT INTO session VALUES ('shared', NULL, '/stale', 'stale', NULL, NULL, NULL, NULL, 0, 100, NULL)`,
		`INSERT INTO session_v2 VALUES ('shared', NULL, '/current', 'current', NULL, NULL, NULL, NULL, 2, 300, NULL)`,
		`INSERT INTO message VALUES ('m1', 'legacy', 100, '{"role":"user","time":{"created":100}}')`,
		`INSERT INTO part VALUES ('p1', 'm1', 100, '{"type":"text","text":"old prompt"}')`,
		`INSERT INTO session_message VALUES ('m2', 'shared', 'user', 1, 200, '{"text":"new prompt","time":{"created":200}}')`,
		`INSERT INTO session_message VALUES ('m3', 'shared', 'assistant', 2, 250, '{"model":{"providerID":"openai","id":"gpt"},"content":[{"type":"text","text":"new answer"}],"finish":"stop","time":{"created":250,"completed":300}}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	parsed, skipped, err := load(db, activeFamiliesQuery)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 || len(parsed) != 2 {
		t.Fatalf("parsed %d, skipped %v", len(parsed), skipped)
	}
	byID := map[string]*vendors.ParsedSession{}
	for _, item := range parsed {
		byID[item.Session.ID] = item
	}
	if byID["legacy"] == nil || byID["legacy"].Session.WorkingDirectory != "/v1" {
		t.Fatalf("v1 session = %#v", byID["legacy"])
	}
	shared := byID["shared"]
	if shared == nil || shared.Session.WorkingDirectory != "/current" || shared.Session.SessionDetails.FirstPrompt == nil || *shared.Session.SessionDetails.FirstPrompt != "new prompt" {
		t.Fatalf("v2 session = %#v", shared)
	}
	if shared.Session.Summary == nil || *shared.Session.Summary != "new answer" || shared.RecordedCost == nil || *shared.RecordedCost != 2 {
		t.Fatalf("v2 summary and cost = %#v", shared)
	}
}

func TestV2OnlyOpenCodeDatabase(t *testing.T) {
	db := testDB(t)
	for _, statement := range []string{
		`CREATE TABLE session_v2 (id TEXT, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_updated INTEGER, time_archived INTEGER)`,
		`CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, data TEXT)`,
		`INSERT INTO session_v2 VALUES ('v2', NULL, '/work', 'current', NULL, NULL, NULL, NULL, 0, 300, NULL)`,
		`INSERT INTO session_message VALUES ('m1', 'v2', 'user', 1, 100, '{"text":"hello","time":{"created":100}}')`,
		`INSERT INTO session_message VALUES ('m2', 'v2', 'assistant', 2, 200, '{"content":[{"type":"text","text":"hi"}],"finish":"stop","time":{"created":200,"completed":300}}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	parsed, skipped, err := load(db, activeFamiliesQuery)
	if err != nil || len(skipped) != 0 || len(parsed) != 1 {
		t.Fatalf("parsed %d, skipped %v, error %v", len(parsed), skipped, err)
	}
	if parsed[0].Session.SessionDetails.Turns != 1 || parsed[0].Session.StartedAt != 100 {
		t.Fatalf("v2 session = %#v", parsed[0].Session)
	}
}

func TestEarliestMessageTimeIgnoresMissingTimestamps(t *testing.T) {
	messages := make([]storedMessage, 4)
	messages[0].Time.Created = 0
	messages[1].Time.Created = 500
	messages[2].Time.Created = 200
	messages[3].Time.Created = -1

	if got := earliestMessageTime(messages); got != 200 {
		t.Fatalf("earliest message time = %d; want 200", got)
	}
}

func TestParseIgnoresIncompleteAssistantFromSupersededTurn(t *testing.T) {
	parsed := parseMessages(t,
		`{"role":"user","time":{"created":100}}`,
		`{"role":"assistant","time":{"created":200}}`,
		`{"role":"user","time":{"created":300}}`,
		`{"role":"assistant","finish":"stop","time":{"created":400,"completed":500}}`,
	)

	if parsed.InTurn {
		t.Fatal("superseded incomplete assistant left session in turn")
	}
	if parsed.StatusHint != nil {
		t.Fatalf("status hint = %q; want nil", *parsed.StatusHint)
	}
}

func TestParseMarksNewUserTurnBusyBeforeAssistantIsPersisted(t *testing.T) {
	db := testDB(t)
	insertMessage(t, db, 0, `{"role":"user","time":{"created":100}}`)
	insertMessage(t, db, 1, `{"role":"assistant","time":{"created":200}}`)
	now := time.Now().UnixMilli()
	insertMessage(t, db, 2, fmt.Sprintf(`{"role":"user","time":{"created":%d}}`, now))
	insertPart(t, db, "message-2", `{"type":"text","text":"Continue"}`)
	parsed := parseDB(t, db)

	if !parsed.InTurn || parsed.StatusHint == nil || *parsed.StatusHint != "busy" {
		t.Fatalf("new user turn = InTurn %t, hint %v; want busy", parsed.InTurn, parsed.StatusHint)
	}
}

func TestParseExpiresAbandonedPromptBeforeAssistantIsPersisted(t *testing.T) {
	db := testDB(t)
	insertMessage(t, db, 0, `{"role":"user","time":{"created":100}}`)
	insertPart(t, db, "message-0", `{"type":"text","text":"Continue"}`)
	parsed := parseDB(t, db)

	if parsed.InTurn || parsed.StatusHint != nil {
		t.Fatalf("abandoned user turn = InTurn %t, hint %v; want inactive", parsed.InTurn, parsed.StatusHint)
	}
}

func TestParseIgnoresRunningQuestionFromSupersededTurn(t *testing.T) {
	db := testDB(t)
	insertMessage(t, db, 0, `{"role":"user","time":{"created":100}}`)
	insertMessage(t, db, 1, `{"role":"assistant","time":{"created":200}}`)
	insertPart(t, db, "message-1", `{"type":"tool","tool":"question","state":{"status":"running","input":{"questions":[{"question":"Continue?"}]}}}`)
	insertMessage(t, db, 2, `{"role":"user","time":{"created":300}}`)
	insertMessage(t, db, 3, `{"role":"assistant","finish":"stop","time":{"created":400,"completed":500}}`)
	parsed := parseDB(t, db)

	if parsed.InTurn || parsed.Session.Status != nil || parsed.StatusHint != nil {
		t.Fatalf(
			"superseded question = InTurn %t, status %v, hint %v; want inactive",
			parsed.InTurn, parsed.Session.Status, parsed.StatusHint,
		)
	}
}

func TestParseMarksCurrentIncompleteAssistantBusy(t *testing.T) {
	parsed := parseMessages(t,
		`{"role":"user","time":{"created":100}}`,
		`{"role":"assistant","time":{"created":200}}`,
	)

	if !parsed.InTurn || parsed.StatusHint == nil || *parsed.StatusHint != "busy" {
		t.Fatalf("current incomplete assistant = InTurn %t, hint %v; want busy", parsed.InTurn, parsed.StatusHint)
	}
}

func TestParseMarksCurrentRunningQuestionWaiting(t *testing.T) {
	db := testDB(t)
	insertMessage(t, db, 0, `{"role":"user","time":{"created":100}}`)
	insertMessage(t, db, 1, `{"role":"assistant","time":{"created":200}}`)
	insertPart(t, db, "message-1", `{"type":"tool","tool":"question","state":{"status":"running","input":{"questions":[{"question":"Continue?"}]}}}`)
	parsed := parseDB(t, db)

	if !parsed.InTurn || parsed.Session.Status == nil || *parsed.Session.Status != "waiting" {
		t.Fatalf("current question = InTurn %t, status %v; want waiting", parsed.InTurn, parsed.Session.Status)
	}
	if parsed.StatusHint != nil {
		t.Fatalf("current question busy hint = %q; want nil", *parsed.StatusHint)
	}
}

func TestParseIgnoresRunningTaskFromSupersededTurn(t *testing.T) {
	db := testDB(t)
	insertMessage(t, db, 0, `{"role":"user","time":{"created":100}}`)
	insertMessage(t, db, 1, `{"role":"assistant","time":{"created":200}}`)
	insertPart(t, db, "message-1", `{"type":"tool","tool":"task","state":{"status":"running","metadata":{"sessionId":"child"}}}`)
	insertMessage(t, db, 2, `{"role":"user","time":{"created":300}}`)
	insertMessage(t, db, 3, `{"role":"assistant","finish":"stop","time":{"created":400,"completed":500}}`)
	parsed := parseDB(t, db)

	spawn := parsed.Spawns["child"]
	if parsed.InTurn || parsed.StatusHint != nil || !spawn.Completed {
		t.Fatalf(
			"superseded task = InTurn %t, hint %v, completed %t; want inactive returned spawn",
			parsed.InTurn, parsed.StatusHint, spawn.Completed,
		)
	}
}

func TestParseMarksCurrentRunningTaskBusy(t *testing.T) {
	db := testDB(t)
	insertMessage(t, db, 0, `{"role":"user","time":{"created":100}}`)
	insertMessage(t, db, 1, `{"role":"assistant","time":{"created":200,"completed":300}}`)
	insertPart(t, db, "message-1", `{"type":"tool","tool":"task","state":{"status":"running","metadata":{"sessionId":"child"}}}`)
	parsed := parseDB(t, db)

	if !parsed.InTurn || parsed.StatusHint == nil || *parsed.StatusHint != "busy" {
		t.Fatalf("current task = InTurn %t, hint %v; want busy", parsed.InTurn, parsed.StatusHint)
	}
}

func parseMessages(t *testing.T, messages ...string) *vendors.ParsedSession {
	t.Helper()
	db := testDB(t)
	for index, message := range messages {
		insertMessage(t, db, index, message)
	}
	return parseDB(t, db)
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, statement := range []string{
		`CREATE TABLE message (id TEXT, session_id TEXT, time_created INTEGER, data TEXT)`,
		`CREATE TABLE part (id TEXT, message_id TEXT, time_updated INTEGER, data TEXT)`,
		`CREATE TABLE todo (session_id TEXT, content TEXT, status TEXT, position INTEGER)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func insertMessage(t *testing.T, db *sql.DB, index int, data string) {
	t.Helper()
	id := fmt.Sprintf("message-%d", index)
	if _, err := db.Exec(
		`INSERT INTO message (id, session_id, time_created, data) VALUES (?, 'session', ?, ?)`,
		id, index, data,
	); err != nil {
		t.Fatal(err)
	}
}

func insertPart(t *testing.T, db *sql.DB, messageID, data string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO part (id, message_id, time_updated, data) VALUES ('part', ?, 0, ?)`,
		messageID, data,
	); err != nil {
		t.Fatal(err)
	}
}

func parseDB(t *testing.T, db *sql.DB) *vendors.ParsedSession {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	parsed, err := parse(tx, storedSession{id: "session", directory: "/tmp", title: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return parsed.transcript
}
