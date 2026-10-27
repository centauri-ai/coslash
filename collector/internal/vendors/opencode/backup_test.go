package opencode

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestCaptureFamilyAcceptsRootAndChildrenInBothSessionTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	t.Setenv("OPENCODE_DB", path)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE session (id TEXT, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_created INTEGER, time_updated INTEGER, time_archived INTEGER)`,
		`CREATE TABLE session_v2 (id TEXT, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_created INTEGER, time_updated INTEGER, time_archived INTEGER)`,
		`CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER DEFAULT 0, data TEXT)`,
		`CREATE TABLE message (id TEXT, session_id TEXT, time_created INTEGER, data TEXT)`,
		`CREATE TABLE part (id TEXT, message_id TEXT, time_updated INTEGER, data TEXT)`,
		`CREATE TABLE todo (session_id TEXT, content TEXT, status TEXT, position INTEGER)`,
	}
	ids := []string{"ses_z_root"}
	for i := range 39 {
		ids = append(ids, fmt.Sprintf("ses_a_child_%02d", i))
	}
	// OpenCode can store a root row after its children, so the root must not rely on insertion order.
	for i := len(ids) - 1; i >= 0; i-- {
		id := ids[i]
		parent := "'ses_z_root'"
		if i == 0 {
			parent = "NULL"
		}
		tables := []string{"session", "session_v2"}
		if i%2 == 1 {
			tables = []string{"session"}
		}
		for _, table := range tables {
			statements = append(statements, fmt.Sprintf(`INSERT INTO %s VALUES ('%s', %s, '/work', 'title', NULL, NULL, NULL, NULL, 0, 50, %d, NULL)`, table, id, parent, 100+i))
		}
		if i%2 == 0 {
			statements = append(statements,
				fmt.Sprintf(`INSERT INTO session_message (id, session_id, type, seq, time_created, data) VALUES ('u%d', '%s', 'user', 1, 100, '{"text":"hello","time":{"created":100}}')`, i, id))
		}
	}
	for _, statement := range statements {
		if _, err := writer.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	capture, err := CaptureFamilyContext(t.Context(), "ses_z_root")
	if err != nil {
		t.Fatalf("capture = %v, want the family that the list path accepts", err)
	}
	defer capture.Close()
	if len(capture.Parsed) != len(ids) || capture.Parsed[0].Session.ID != "ses_z_root" {
		t.Fatalf("parsed %d members, first %q", len(capture.Parsed), capture.Parsed[0].Session.ID)
	}
}
