package opencode

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
	_ "modernc.org/sqlite"
)

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
	insertMessage(t, db, 2, `{"role":"user","time":{"created":300}}`)
	insertPart(t, db, "message-2", `{"type":"text","text":"Continue"}`)
	parsed := parseDB(t, db)

	if !parsed.InTurn || parsed.StatusHint == nil || *parsed.StatusHint != "busy" {
		t.Fatalf("new user turn = InTurn %t, hint %v; want busy", parsed.InTurn, parsed.StatusHint)
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

	if parsed.InTurn || parsed.StatusHint != nil {
		t.Fatalf("superseded task = InTurn %t, hint %v; want inactive", parsed.InTurn, parsed.StatusHint)
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
