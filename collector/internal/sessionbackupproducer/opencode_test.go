package sessionbackupproducer

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	backup "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
	_ "modernc.org/sqlite"
)

func openCodeBackupDB(t *testing.T) *sql.DB {
	t.Helper()
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	path := filepath.Join(dataHome, "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, statement := range []string{
		`CREATE TABLE session (id TEXT, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_created INTEGER, time_updated INTEGER, time_archived INTEGER, future_field TEXT)`,
		`CREATE TABLE message (id TEXT, session_id TEXT, time_created INTEGER, data TEXT)`,
		`CREATE TABLE part (id TEXT, message_id TEXT, time_updated INTEGER, data TEXT)`,
		`CREATE TABLE todo (session_id TEXT, content TEXT, status TEXT, position INTEGER)`,
		`INSERT INTO session VALUES ('root', NULL, '/tmp', 'Root', NULL, NULL, NULL, NULL, 1.25, 100, 300, NULL, 'future-value')`,
		`INSERT INTO session VALUES ('child', 'root', '/tmp', 'Child', NULL, NULL, NULL, NULL, 0.5, 110, 250, NULL, NULL)`,
		`INSERT INTO session VALUES ('unrelated', NULL, '/tmp', 'Other', NULL, NULL, NULL, NULL, 9, 100, 300, NULL, 'secret-other')`,
		`INSERT INTO message VALUES ('m-root', 'root', 100, '{"role":"user","time":{"created":100}}')`,
		`INSERT INTO part VALUES ('p-root', 'm-root', 100, '{"type":"text","text":"hello"}')`,
		`INSERT INTO todo VALUES ('root', 'repeat', 'pending', 1)`,
		`INSERT INTO todo VALUES ('root', 'repeat', 'pending', 1)`,
		`INSERT INTO message VALUES ('m-child', 'child', 110, '{"role":"user","time":{"created":110}}')`,
		`INSERT INTO part VALUES ('p-child', 'm-child', 110, '{"type":"text","text":"child prompt"}')`,
		`INSERT INTO message VALUES ('m-other', 'unrelated', 100, '{"role":"user","time":{"created":100}}')`,
		`INSERT INTO part VALUES ('p-other', 'm-other', 100, '{"type":"text","text":"secret-other"}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestOpenCodeCompleteRoundTripAndDedupe(t *testing.T) {
	_ = openCodeBackupDB(t)
	spool := t.TempDir()
	selection := Selection{SourceKind: backup.SourceLocal, SourceID: "local", Agent: vendors.AgentOpenCode, SessionID: "root"}
	manager := New(Options{Root: spool})
	prepared, err := manager.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := backup.VerifyDirectory(filepath.Join(spool, prepared.BundleID))
	if err != nil || verified.CompleteBackupSHA256 != prepared.BundleID {
		t.Fatalf("verified = %#v, %v", verified, err)
	}
	if len(verified.Members) != 2 || verified.Source.Agent != vendors.AgentOpenCode {
		t.Fatalf("manifest = %#v", verified)
	}
	for _, artifact := range verified.Artifacts {
		data, err := os.ReadFile(filepath.Join(spool, prepared.BundleID, filepath.FromSlash(artifact.LogicalName)))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("secret-other")) {
			t.Fatalf("unrelated source row included in %s", artifact.LogicalName)
		}
		if artifact.Kind == backup.KindRawMetadataRows {
			rows, err := backup.DecodeDatabaseRows(data, artifact.MemberID)
			if err != nil {
				t.Fatal(err)
			}
			foundCost, foundFuture := false, false
			for _, table := range rows.Tables {
				if artifact.MemberID == "root" && table.Name == "todo" && len(table.Rows) != 2 {
					t.Fatalf("duplicate source rows lost: %#v", table.Rows)
				}
				if table.Name != "session" {
					continue
				}
				for index, column := range table.Columns {
					if column == "cost" && table.Rows[0].Values[index].Type == "real" {
						foundCost = true
					}
					if artifact.MemberID == "root" && column == "future_field" && table.Rows[0].Values[index].Value == "future-value" {
						foundFuture = true
					}
				}
			}
			if !foundCost || (artifact.MemberID == "root" && !foundFuture) {
				t.Fatalf("lost source data in %s", artifact.LogicalName)
			}
		}
		if artifact.Kind == backup.KindParsedSessionRecord && artifact.MemberID == "root" {
			record, err := fullsessionv1.Decode(data)
			if err != nil || record.Session.CostMicroUSD == nil || *record.Session.CostMicroUSD != 1250000 {
				t.Fatalf("record cost = %#v, %v", record.Session.CostMicroUSD, err)
			}
		}
	}
	restarted := New(Options{Root: spool})
	second, err := restarted.Prepare(t.Context(), selection)
	if err != nil || second.BundleID != prepared.BundleID {
		t.Fatalf("dedupe = %#v, %v", second, err)
	}
	opened, err := restarted.Open(prepared.BundleID)
	if err != nil || opened.Manifest.CompleteBackupSHA256 != prepared.BundleID {
		t.Fatalf("restart = %#v, %v", opened, err)
	}
}

func TestOpenCodeUnattributableRowsCannotComplete(t *testing.T) {
	db := openCodeBackupDB(t)
	if _, err := db.Exec(`UPDATE message SET data = '{invalid' WHERE session_id = 'root'`); err != nil {
		t.Fatal(err)
	}
	manager := New(Options{Root: t.TempDir()})
	prepared, err := manager.Prepare(t.Context(), Selection{SourceKind: backup.SourceLocal, SourceID: "local", Agent: vendors.AgentOpenCode, SessionID: "root"})
	var failure *PreparationError
	if prepared != nil || !errors.As(err, &failure) || failure.Coverage.Problems[0].Code != backup.ProblemUnattributable {
		t.Fatalf("prepared=%#v error=%v", prepared, err)
	}
}

// A row value of several MiB is carried exactly: the rows contract bounds a
// value only by its document. (Message text still meets the parsed record's
// own per-string bound.)
func TestOpenCodeLargeRowPrepares(t *testing.T) {
	db := openCodeBackupDB(t)
	if _, err := db.Exec(`UPDATE session SET future_field = ? WHERE id = 'root'`, strings.Repeat("x", 3<<20)); err != nil {
		t.Fatal(err)
	}
	manager := New(Options{Root: t.TempDir()})
	if _, err := manager.Prepare(t.Context(), Selection{SourceKind: backup.SourceLocal, SourceID: "local", Agent: vendors.AgentOpenCode, SessionID: "root"}); err != nil {
		t.Fatalf("large row must prepare: %v", err)
	}
}

// Rows the rows contract cannot carry, such as text that is not UTF-8, fail
// the same way every time, so they must not be reported as a retryable read
// problem.
func TestOpenCodeUnrepresentableRowIsInvalidNotRetryable(t *testing.T) {
	db := openCodeBackupDB(t)
	if _, err := db.Exec(`UPDATE session SET title = CAST(X'FF' AS TEXT) WHERE id = 'root'`); err != nil {
		t.Fatal(err)
	}
	manager := New(Options{Root: t.TempDir()})
	prepared, err := manager.Prepare(t.Context(), Selection{SourceKind: backup.SourceLocal, SourceID: "local", Agent: vendors.AgentOpenCode, SessionID: "root"})
	var failure *PreparationError
	if prepared != nil || !errors.As(err, &failure) || len(failure.Coverage.Problems) != 1 {
		t.Fatalf("prepared=%#v error=%v", prepared, err)
	}
	if problem := failure.Coverage.Problems[0]; problem.Code != backup.ProblemInvalid || problem.Kind != backup.KindRawMetadataRows || problem.Retryable {
		t.Fatalf("problem=%+v", problem)
	}
}

func TestOpenCodeV2RowsAndRecordedCost(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	path := filepath.Join(dataHome, "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE session_v2 (id TEXT, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_created INTEGER, time_updated INTEGER, time_archived INTEGER, future_v2 TEXT)`,
		`CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`,
		`INSERT INTO session_v2 VALUES ('v2-root', NULL, '/tmp', 'V2 root', NULL, NULL, NULL, NULL, 2.75, 100, 300, NULL, 'retained')`,
		`INSERT INTO session_message VALUES ('m1', 'v2-root', 'user', 1, 100, 100, '{"text":"hello","time":{"created":100}}')`,
		`INSERT INTO session_message VALUES ('m2', 'v2-root', 'assistant', 2, 200, 200, '{"content":[{"type":"text","text":"done"}],"finish":"stop","time":{"created":200,"completed":300}}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	spool := t.TempDir()
	prepared, err := New(Options{Root: spool}).Prepare(t.Context(), Selection{SourceKind: backup.SourceLocal, SourceID: "local", Agent: vendors.AgentOpenCode, SessionID: "v2-root"})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := backup.VerifyDirectory(filepath.Join(spool, prepared.BundleID))
	if err != nil {
		t.Fatal(err)
	}
	if len(verified.Members) != 1 {
		t.Fatalf("members = %#v", verified.Members)
	}
	for _, artifact := range verified.Artifacts {
		data, err := os.ReadFile(filepath.Join(spool, prepared.BundleID, filepath.FromSlash(artifact.LogicalName)))
		if err != nil {
			t.Fatal(err)
		}
		switch artifact.Kind {
		case backup.KindRawMetadataRows:
			rows, err := backup.DecodeDatabaseRows(data, "v2-root")
			if err != nil || len(rows.Tables) != 2 || !bytes.Contains(data, []byte("retained")) {
				t.Fatalf("v2 rows = %#v, %v", rows, err)
			}
		case backup.KindParsedSessionRecord:
			record, err := fullsessionv1.Decode(data)
			if err != nil || record.Session.CostMicroUSD == nil || *record.Session.CostMicroUSD != 2750000 {
				t.Fatalf("v2 cost = %#v, %v", record.Session.CostMicroUSD, err)
			}
		}
	}
}

func TestOpenCodeChangingSourceCannotComplete(t *testing.T) {
	db := openCodeBackupDB(t)
	spool := t.TempDir()
	manager := New(Options{Root: spool, AfterRawCopy: func() {
		if _, err := db.Exec(`UPDATE session SET cost = 2 WHERE id = 'root'`); err != nil {
			t.Fatal(err)
		}
	}})
	prepared, err := manager.Prepare(t.Context(), Selection{SourceKind: backup.SourceLocal, SourceID: "local", Agent: vendors.AgentOpenCode, SessionID: "root"})
	var failure *PreparationError
	if prepared != nil || !errors.As(err, &failure) || failure.Coverage.Problems[0].Code != backup.ProblemUnstable {
		t.Fatalf("prepared=%#v error=%v", prepared, err)
	}
	entries, err := os.ReadDir(spool)
	if err != nil || len(entries) != 0 {
		t.Fatalf("spool entries = %d, %v", len(entries), err)
	}
}
