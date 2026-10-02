package opencode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func deleteFixture(t *testing.T) (string, string, *sql.DB, *int) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("PATH", "")
	path := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, q := range []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_created INTEGER, time_updated INTEGER, time_archived INTEGER)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)`,
		`CREATE TABLE part (id TEXT, session_id TEXT, message_id TEXT, time_updated INTEGER, data TEXT)`,
		`CREATE TABLE todo (session_id TEXT, content TEXT, status TEXT, position INTEGER)`,
		`CREATE TABLE event (id TEXT, aggregate_id TEXT, data TEXT)`,
		`CREATE TABLE event_sequence (aggregate_id TEXT, seq INTEGER)`,
		`INSERT INTO session VALUES ('ses_target',NULL,'/work','target',NULL,NULL,NULL,NULL,0,100,200,NULL), ('ses_child','ses_target','/work','child',NULL,NULL,NULL,NULL,0,100,200,NULL), ('ses_grandchild','ses_child','/work','grandchild',NULL,NULL,NULL,NULL,0,100,200,300), ('ses_neighbor',NULL,'/work','neighbor',NULL,NULL,NULL,NULL,0,100,200,NULL)`,
		`INSERT INTO message VALUES ('msg_target','ses_target',100,'{"role":"user"}'),('msg_neighbor','ses_neighbor',100,'{"role":"user"}')`,
		`INSERT INTO part VALUES ('prt_target','ses_target','msg_target',100,'{}'),('prt_neighbor','ses_neighbor','msg_neighbor',100,'{}')`,
		`INSERT INTO event VALUES ('evt_target','ses_target','{}'),('evt_neighbor','ses_neighbor','{}')`,
		`INSERT INTO event_sequence VALUES ('ses_target',1),('ses_neighbor',1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	oldProbe, oldRun := deleteProcesses, runSessionDelete
	t.Cleanup(func() { deleteProcesses, runSessionDelete = oldProbe, oldRun })
	deleteProcesses = func(context.Context) ([]tuiProcess, error) { return nil, nil }
	calls := new(int)
	runSessionDelete = func(ctx context.Context, gotHome, gotPath, id string, family map[string]bool) error {
		*calls++
		if gotHome != home || gotPath != path || id != "ses_target" {
			t.Fatalf("wrong deletion identity: %q %q %q", gotHome, gotPath, id)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded deletion")
		}
		for _, table := range []string{"part", "message", "todo", "event", "event_sequence", "session"} {
			col := "session_id"
			if table == "session" {
				col = "id"
			}
			if table == "event" || table == "event_sequence" {
				col = "aggregate_id"
			}
			if _, err := db.Exec(`DELETE FROM ` + table + ` WHERE ` + col + ` IN ('ses_target','ses_child','ses_grandchild')`); err != nil {
				return err
			}
		}
		return nil
	}
	return home, path, db, calls
}

func rowCount(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDeleteSessionFamilyPreservesNeighbor(t *testing.T) {
	home, _, db, calls := deleteFixture(t)
	for _, id := range []string{"ses_target", "ses_child", "ses_grandchild", "ses_neighbor"} {
		for _, dir := range []string{"opencode-clients", "opencode-permissions"} {
			path := filepath.Join(home, ".coslash", dir, id+".json")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"sessionID":%q,"client":"cli","pid":0}`, id)), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := DeleteSession(context.Background(), home, "ses_target"); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 || rowCount(t, db, `SELECT COUNT(*) FROM session`) != 1 || rowCount(t, db, `SELECT COUNT(*) FROM message`) != 1 || rowCount(t, db, `SELECT COUNT(*) FROM part`) != 1 || rowCount(t, db, `SELECT COUNT(*) FROM event`) != 1 {
		t.Fatal("family or neighbor changed incorrectly")
	}
	for _, dir := range []string{"opencode-clients", "opencode-permissions"} {
		for _, id := range []string{"ses_target", "ses_child", "ses_grandchild"} {
			if _, err := os.Stat(filepath.Join(home, ".coslash", dir, id+".json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("sidecar survives: %s, %v", id, err)
			}
		}
		if _, err := os.Stat(filepath.Join(home, ".coslash", dir, "ses_neighbor.json")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeleteSessionRefusesBeforeWrite(t *testing.T) {
	for _, tc := range []struct {
		name, id       string
		processes      []tuiProcess
		probeErr, want error
	}{
		{name: "invalid", id: "../ses_target", want: ErrInvalidSession},
		{name: "missing", id: "ses_missing", want: ErrSessionMissing},
		{name: "active", id: "ses_target", processes: []tuiProcess{{sessionID: "ses_target"}}, want: ErrSessionActive},
		{name: "active child", id: "ses_target", processes: []tuiProcess{{sessionID: "ses_grandchild"}}, want: ErrSessionActive},
		{name: "ambiguous", id: "ses_target", processes: []tuiProcess{{pid: 1}}, want: ErrSessionUnverified},
		{name: "failed probe", id: "ses_target", probeErr: errors.New("probe failed"), want: ErrSessionUnverified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, _, db, calls := deleteFixture(t)
			deleteProcesses = func(context.Context) ([]tuiProcess, error) { return tc.processes, tc.probeErr }
			if err := DeleteSession(context.Background(), home, tc.id); !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
			if *calls != 0 || rowCount(t, db, `SELECT COUNT(*) FROM session`) != 4 {
				t.Fatal("wrote before safety refusal")
			}
		})
	}
}

func TestDeleteSessionFailureAndResidue(t *testing.T) {
	for _, tc := range []string{"failure", "session residue", "event residue", "message residue", "new child", "cancellation"} {
		t.Run(tc, func(t *testing.T) {
			home, _, db, _ := deleteFixture(t)
			good := runSessionDelete
			runSessionDelete = func(ctx context.Context, home, path, id string, family map[string]bool) error {
				switch tc {
				case "failure":
					return errors.New("database failure")
				case "session residue":
					return nil
				case "cancellation":
					return context.Canceled
				}
				if err := good(ctx, home, path, id, family); err != nil {
					return err
				}
				q := `INSERT INTO event VALUES ('evt_residue','ses_child','{}')`
				if tc == "message residue" {
					q = `INSERT INTO message VALUES ('msg_residue','ses_child',100,'{}')`
				}
				if tc == "new child" {
					q = `INSERT INTO session VALUES ('ses_new','ses_target','/work','new',NULL,NULL,NULL,NULL,0,100,200,NULL)`
				}
				_, err := db.Exec(q)
				return err
			}
			err := DeleteSession(context.Background(), home, "ses_target")
			if err == nil {
				t.Fatal("reported success with failed or partial deletion")
			}
			if tc == "cancellation" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if rowCount(t, db, `SELECT COUNT(*) FROM session WHERE id='ses_neighbor'`) != 1 {
				t.Fatal("neighbor deleted")
			}
		})
	}
}

func TestDeleteSessionCanceledBeforeWrite(t *testing.T) {
	home, _, db, calls := deleteFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := DeleteSession(ctx, home, "ses_target"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if *calls != 0 || rowCount(t, db, `SELECT COUNT(*) FROM session`) != 4 {
		t.Fatal("canceled deletion wrote")
	}
}

func TestDeleteSessionRejectsLegacyAndSymlink(t *testing.T) {
	for _, tc := range []string{"legacy", "sidecar link", "database link"} {
		t.Run(tc, func(t *testing.T) {
			home, path, db, calls := deleteFixture(t)
			switch tc {
			case "legacy":
				path = filepath.Join(filepath.Dir(path), "storage", "session", "project", "ses_target.json")
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(`{"id":"ses_target"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "sidecar link":
				outside := t.TempDir()
				if err := os.MkdirAll(filepath.Join(home, ".coslash"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(home, ".coslash", "opencode-clients")); err != nil {
					t.Skip(err)
				}
			case "database link":
				other := filepath.Join(t.TempDir(), "opencode.db")
				if err := os.Rename(path, other); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, path); err != nil {
					t.Skip(err)
				}
			}
			if err := DeleteSession(context.Background(), home, "ses_target"); !errors.Is(err, ErrSessionUnverified) {
				t.Fatalf("error=%v", err)
			}
			if *calls != 0 || rowCount(t, db, `SELECT COUNT(*) FROM session`) != 4 {
				t.Fatal("unverified layout wrote")
			}
		})
	}
}

func TestDeleteSessionIntermediateDataSymlink(t *testing.T) {
	home, path, db, calls := deleteFixture(t)
	outside := t.TempDir()
	share := filepath.Join(home, ".local", "share")
	if err := os.Rename(share, filepath.Join(outside, "share")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "share"), share); err != nil {
		t.Skip(err)
	}
	_ = path
	if err := DeleteSession(context.Background(), home, "ses_target"); !errors.Is(err, ErrSessionUnverified) {
		t.Fatalf("error=%v", err)
	}
	if *calls != 0 || rowCount(t, db, `SELECT COUNT(*) FROM session`) != 4 {
		t.Fatal("symlink layout wrote")
	}
}

func TestDeleteSessionEffectiveDatabaseRoot(t *testing.T) {
	for _, mode := range []string{"xdg", "absolute override", "relative override"} {
		t.Run(mode, func(t *testing.T) {
			home, path, db, calls := deleteFixture(t)
			var effective string
			switch mode {
			case "xdg":
				data := t.TempDir()
				t.Setenv("XDG_DATA_HOME", data)
				effective = filepath.Join(data, "opencode", "opencode.db")
			case "absolute override":
				effective = filepath.Join(t.TempDir(), "custom.db")
				t.Setenv("OPENCODE_DB", effective)
			case "relative override":
				effective = filepath.Join(filepath.Dir(path), "custom.db")
				t.Setenv("OPENCODE_DB", "custom.db")
			}
			if err := os.MkdirAll(filepath.Dir(effective), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path, effective); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			effectiveDB, err := sql.Open("sqlite", effective)
			if err != nil {
				t.Fatal(err)
			}
			defer effectiveDB.Close()
			runSessionDelete = func(ctx context.Context, h, p, id string, family map[string]bool) error {
				*calls++
				if p != effective || h != home || id != "ses_target" {
					t.Fatalf("wrong deletion identity: %q %q %q", h, p, id)
				}
				for _, table := range []string{"session", "message", "part", "event", "event_sequence"} {
					col := "session_id"
					if table == "session" {
						col = "id"
					}
					if table == "event" || table == "event_sequence" {
						col = "aggregate_id"
					}
					if _, err := effectiveDB.Exec(`DELETE FROM ` + table + ` WHERE ` + col + ` IN ('ses_target','ses_child','ses_grandchild')`); err != nil {
						return err
					}
				}
				return nil
			}

			if err := DeleteSession(context.Background(), home, "ses_target"); err != nil {
				t.Fatal(err)
			}
			if *calls != 1 || rowCount(t, effectiveDB, `SELECT COUNT(*) FROM session`) != 1 {
				t.Fatal("wrong database deleted")
			}
		})
	}
}

func TestDeleteSessionRetainsSidecarsOnPartialFailure(t *testing.T) {
	home, _, _, _ := deleteFixture(t)
	path := filepath.Join(home, ".coslash", "opencode-clients", "ses_target.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	runSessionDelete = func(context.Context, string, string, string, map[string]bool) error { return nil }
	if err := DeleteSession(context.Background(), home, "ses_target"); !errors.Is(err, ErrSessionDeleteFailed) {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("removed sidecar before verifying database deletion")
	}
}

func TestDeleteSessionV2DuplicateAndArchivedChildren(t *testing.T) {
	home, _, db, _ := deleteFixture(t)
	for _, q := range []string{
		`CREATE TABLE session_v2 AS SELECT * FROM session`,
		`CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, data TEXT)`,
		`INSERT INTO session_message VALUES ('msg_v2','ses_child','user',1,100,'{}'),('msg_v2_neighbor','ses_neighbor','user',1,100,'{}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old := runSessionDelete
	runSessionDelete = func(ctx context.Context, h, p, id string, family map[string]bool) error {
		if err := old(ctx, h, p, id, family); err != nil {
			return err
		}
		for _, q := range []string{`DELETE FROM session_message WHERE session_id IN ('ses_target','ses_child','ses_grandchild')`, `DELETE FROM session_v2 WHERE id IN ('ses_target','ses_child','ses_grandchild')`} {
			if _, err := db.Exec(q); err != nil {
				return err
			}
		}
		return nil
	}
	if err := DeleteSession(context.Background(), home, "ses_target"); err != nil {
		t.Fatal(err)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM session_v2`) != 1 || rowCount(t, db, `SELECT COUNT(*) FROM session_message`) != 1 {
		t.Fatal("v2 family or neighbor changed incorrectly")
	}
}

func TestRootContextUsesDatabaseOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	for _, override := range []string{filepath.Join(home, "custom.db"), "custom.db"} {
		t.Setenv("OPENCODE_DB", override)
		got, err := RootContext(context.Background())
		want := override
		if !filepath.IsAbs(want) {
			want = filepath.Join(home, "data", "opencode", want)
		}
		if err != nil || got != want {
			t.Fatalf("path=%q error=%v, want %q", got, err, want)
		}
	}
}

func TestDeleteSessionRechecksLivenessBeforeCommand(t *testing.T) {
	home, _, db, calls := deleteFixture(t)
	probes := 0
	deleteProcesses = func(context.Context) ([]tuiProcess, error) {
		probes++
		if probes == 2 {
			return []tuiProcess{{sessionID: "ses_target"}}, nil
		}
		return nil, nil
	}
	if err := DeleteSession(context.Background(), home, "ses_target"); !errors.Is(err, ErrSessionActive) {
		t.Fatalf("error=%v", err)
	}
	if *calls != 0 || rowCount(t, db, `SELECT COUNT(*) FROM session`) != 4 {
		t.Fatal("newly active session was deleted")
	}
}

func TestDeleteSessionRejectsOrphanPartResidue(t *testing.T) {
	home, _, db, _ := deleteFixture(t)
	if _, err := db.Exec(`ALTER TABLE part DROP COLUMN session_id`); err != nil {
		t.Fatal(err)
	}
	runSessionDelete = func(context.Context, string, string, string, map[string]bool) error {
		for _, q := range []string{`DELETE FROM session WHERE id!='ses_neighbor'`, `DELETE FROM message WHERE session_id!='ses_neighbor'`, `DELETE FROM event WHERE aggregate_id!='ses_neighbor'`, `DELETE FROM event_sequence WHERE aggregate_id!='ses_neighbor'`} {
			if _, err := db.Exec(q); err != nil {
				return err
			}
		}
		return nil
	}
	if err := DeleteSession(context.Background(), home, "ses_target"); !errors.Is(err, ErrSessionDeleteFailed) {
		t.Fatalf("error=%v", err)
	}
}

func TestDeleteSessionRejectsReplacedDatabase(t *testing.T) {
	home, path, db, _ := deleteFixture(t)
	old := runSessionDelete
	runSessionDelete = func(ctx context.Context, h, p, id string, family map[string]bool) error {
		if err := old(ctx, h, p, id, family); err != nil {
			return err
		}
		// The open reader must not prove absence against an unlinked database.
		return os.Rename(path, path+".old")
	}
	if err := DeleteSession(context.Background(), home, "ses_target"); !errors.Is(err, ErrSessionDeleteFailed) {
		t.Fatalf("error=%v", err)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM session WHERE id='ses_neighbor'`) != 1 {
		t.Fatal("neighbor deleted")
	}
}

func TestDeleteSessionScopedDatabaseTransaction(t *testing.T) {
	for _, mode := range []string{"v1", "v2", "rollback", "ignored delete"} {
		t.Run(mode, func(t *testing.T) {
			home, _, db, _ := deleteFixture(t)
			runSessionDelete = deleteDatabaseSession
			if mode == "v2" {
				for _, q := range []string{`CREATE TABLE session_v2 AS SELECT * FROM session`, `CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, data TEXT)`, `INSERT INTO session_message VALUES ('msg_child','ses_child','user',1,100,'{}')`} {
					if _, err := db.Exec(q); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "rollback" || mode == "ignored delete" {
				action := "ABORT, 'synthetic database failure'"
				if mode == "ignored delete" {
					action = "IGNORE"
				}
				if _, err := db.Exec(`CREATE TRIGGER refuse_delete BEFORE DELETE ON session WHEN OLD.id='ses_target' BEGIN SELECT RAISE(` + action + `); END`); err != nil {
					t.Fatal(err)
				}
			}
			err := DeleteSession(context.Background(), home, "ses_target")
			if mode == "rollback" || mode == "ignored delete" {
				if !errors.Is(err, ErrSessionDeleteFailed) {
					t.Fatalf("error=%v", err)
				}
				if rowCount(t, db, `SELECT COUNT(*) FROM session`) != 4 || rowCount(t, db, `SELECT COUNT(*) FROM message`) != 2 || rowCount(t, db, `SELECT COUNT(*) FROM event`) != 2 {
					t.Fatal("failed deletion did not roll back")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if rowCount(t, db, `SELECT COUNT(*) FROM session`) != 1 || rowCount(t, db, `SELECT COUNT(*) FROM message`) != 1 || rowCount(t, db, `SELECT COUNT(*) FROM part`) != 1 || rowCount(t, db, `SELECT COUNT(*) FROM event`) != 1 {
					t.Fatal("family or neighbor changed incorrectly")
				}
				if mode == "v2" && rowCount(t, db, `SELECT COUNT(*) FROM session_v2`) != 1 {
					t.Fatal("v2 residue")
				}
			}
		})
	}
}

func TestDeleteRepairPermissionRequestResidue(t *testing.T) {
	home, _, _, _ := deleteFixture(t)
	runSessionDelete = deleteDatabaseSession
	dir := filepath.Join(home, ".coslash", "opencode-permissions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "per_request.json")
	if err := os.WriteFile(path, []byte(`{"sessionID":"ses_target","pid":123}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSession(context.Background(), home, "ses_target"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("success left target permission request sidecar")
	}
}
func TestDeleteRepairRetryAfterCommitCancellation(t *testing.T) {
	home, _, db, _ := deleteFixture(t)
	path := filepath.Join(home, ".coslash", "opencode-clients", "ses_target.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"sessionID":"ses_target","client":"cli"}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runSessionDelete = func(c context.Context, h, p, id string, f map[string]bool) error {
		if err := deleteDatabaseSession(c, h, p, id, f); err != nil {
			return err
		}
		cancel()
		return nil
	}
	if err := DeleteSession(ctx, home, "ses_target"); err == nil {
		t.Fatal("expected cancellation")
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM session WHERE id='ses_target'`) != 0 {
		t.Fatal("commit not reached")
	}
	runSessionDelete = deleteDatabaseSession
	err := DeleteSession(context.Background(), home, "ses_target")
	if err != nil {
		t.Fatalf("retry cannot complete owned residue: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("sidecar survived retry")
	}
}
func TestDeleteRepairSQLiteSessionDiffUsable(t *testing.T) {
	home, path, db, _ := deleteFixture(t)
	if err := validateSchemaContext(context.Background(), db); err != nil {
		t.Fatalf("not collector supported: %v", err)
	}
	runSessionDelete = deleteDatabaseSession
	dir := filepath.Join(filepath.Dir(path), "storage", "session_diff")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ses_target", "ses_neighbor"} {
		if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(`[]`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := DeleteSession(context.Background(), home, "ses_target"); err != nil {
		t.Fatalf("supported SQLite session with native diff sidecars refused: %v", err)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM session WHERE id='ses_neighbor'`) != 1 {
		t.Fatal("neighbor changed")
	}
}

func TestDeleteRepairReplacedDatabaseBeforeWrite(t *testing.T) {
	home, path, db, _ := deleteFixture(t)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	runSessionDelete = func(c context.Context, h, p, id string, f map[string]bool) error {
		if err := os.Rename(path, path+".original"); err != nil {
			return err
		}
		if err := os.WriteFile(path, original, 0600); err != nil {
			return err
		}
		return deleteDatabaseSession(c, h, p, id, f)
	}
	if err := DeleteSession(context.Background(), home, "ses_target"); !errors.Is(err, ErrSessionDeleteFailed) {
		t.Fatalf("error %v", err)
	}
	replacement, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if rowCount(t, db, `SELECT COUNT(*) FROM session WHERE id='ses_target'`) != 1 {
		t.Fatal("old DB unexpectedly changed")
	}
	if rowCount(t, replacement, `SELECT COUNT(*) FROM session WHERE id='ses_target'`) != 1 {
		t.Fatal("deletion mutated replacement DB before detecting file replacement")
	}
}
func TestDeleteRepairAbsoluteDBIntermediateSymlink(t *testing.T) {
	home, path, db, _ := deleteFixture(t)
	root := t.TempDir()
	real := filepath.Join(root, "real", "subdir")
	if err := os.MkdirAll(real, 0700); err != nil {
		t.Fatal(err)
	}
	actual := filepath.Join(real, "custom.db")
	if err := os.Rename(path, actual); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	t.Setenv("OPENCODE_DB", filepath.Join(root, "link", "subdir", "custom.db"))
	runSessionDelete = deleteDatabaseSession
	if err := DeleteSession(context.Background(), home, "ses_target"); !errors.Is(err, ErrSessionUnverified) {
		t.Fatalf("intermediate override symlink was followed: %v", err)
	}
}
func TestDeleteRepairWALAndNativeOwnership(t *testing.T) {
	home, _, db, _ := deleteFixture(t)
	runSessionDelete = deleteDatabaseSession
	for _, q := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA foreign_keys=ON`,
		`DROP TABLE event`, `DROP TABLE event_sequence`,
		`CREATE TABLE event_sequence(aggregate_id TEXT PRIMARY KEY, seq INTEGER, owner_id TEXT)`,
		`CREATE TABLE event(id TEXT PRIMARY KEY, aggregate_id TEXT REFERENCES event_sequence(aggregate_id) ON DELETE CASCADE, seq INTEGER, type TEXT, data TEXT)`,
		`CREATE TABLE session_input(id TEXT PRIMARY KEY,session_id TEXT REFERENCES session(id) ON DELETE CASCADE,prompt TEXT,delivery TEXT,admitted_seq INTEGER,promoted_seq INTEGER,time_created INTEGER)`,
		`CREATE TABLE session_context_epoch(session_id TEXT PRIMARY KEY REFERENCES session(id) ON DELETE CASCADE,baseline TEXT,snapshot TEXT,baseline_seq INTEGER)`,
		`CREATE TABLE session_share(session_id TEXT PRIMARY KEY REFERENCES session(id) ON DELETE CASCADE,id TEXT,secret TEXT,url TEXT)`,
		`INSERT INTO todo VALUES('ses_child','owned','pending',0),('ses_neighbor','neighbor','pending',0)`,
		`INSERT INTO event_sequence VALUES('ses_child',1,NULL),('ses_neighbor',1,NULL)`,
		`INSERT INTO event VALUES('evt_child','ses_child',1,'session.created','{}'),('evt_neighbor','ses_neighbor',1,'session.created','{}')`,
		`INSERT INTO session_input VALUES('msg_input','ses_child','{}','steer',1,NULL,1),('msg_neighbor_input','ses_neighbor','{}','steer',1,NULL,1)`,
		`INSERT INTO session_context_epoch VALUES('ses_child','owned','{}',1),('ses_neighbor','neighbor','{}',1)`,
		`INSERT INTO session_share VALUES('ses_child','share_child','secret','url'),('ses_neighbor','share_neighbor','secret','url')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := DeleteSession(context.Background(), home, "ses_target"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"session", "message", "part", "todo", "event", "event_sequence", "session_input", "session_context_epoch", "session_share"} {
		if rowCount(t, db, `SELECT COUNT(*) FROM `+table) != 1 {
			t.Fatalf("incorrect ownership cleanup in %s", table)
		}
	}
}
func TestDeleteRepairLockedWriter(t *testing.T) {
	home, _, db, _ := deleteFixture(t)
	runSessionDelete = deleteDatabaseSession
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), `ROLLBACK`)
	if err := DeleteSession(context.Background(), home, "ses_target"); !errors.Is(err, ErrSessionDeleteFailed) {
		t.Fatalf("expected safe lock failure: %v", err)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM session`) != 4 || rowCount(t, db, `SELECT COUNT(*) FROM message`) != 2 {
		t.Fatal("locked deletion altered sessions")
	}
}

func TestDeleteRepairSQLiteMigrationMarkerUsable(t *testing.T) {
	home, path, db, _ := deleteFixture(t)
	runSessionDelete = deleteDatabaseSession
	if err := validateSchemaContext(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(filepath.Dir(path), "storage")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "migration"), []byte("2"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSession(context.Background(), home, "ses_target"); err != nil {
		t.Fatalf("benign native migration marker blocks supported SQLite session: %v", err)
	}
}

func TestDeleteRepairPureV2Layout(t *testing.T) {
	home, _, db, _ := deleteFixture(t)
	runSessionDelete = deleteDatabaseSession
	for _, q := range []string{
		`CREATE TABLE session_v2(id TEXT PRIMARY KEY,parent_id TEXT,directory TEXT,title TEXT,summary_files INTEGER,summary_diffs TEXT,agent TEXT,model TEXT,cost REAL,time_created INTEGER,time_updated INTEGER,time_archived INTEGER)`,
		`INSERT INTO session_v2 SELECT * FROM session`,
		`CREATE TABLE session_message(id TEXT PRIMARY KEY,session_id TEXT REFERENCES session_v2(id) ON DELETE CASCADE,type TEXT,seq INTEGER,time_created INTEGER,data TEXT)`,
		`INSERT INTO session_message VALUES('msg_owned','ses_child','user',1,1,'{}'),('msg_neighbor','ses_neighbor','user',1,1,'{}')`,
		`DROP TABLE part`, `DROP TABLE message`, `DROP TABLE todo`, `DROP TABLE session`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := DeleteSession(context.Background(), home, "ses_target"); err != nil {
		t.Fatal(err)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM session_v2`) != 1 || rowCount(t, db, `SELECT COUNT(*) FROM session_message`) != 1 {
		t.Fatal("pure v2 cleanup scope incorrect")
	}
}

func TestDeleteRepairRetryPreservesExactChildArtifacts(t *testing.T) {
	home, path, db, _ := deleteFixture(t)
	for _, id := range []string{"ses_target", "ses_child", "ses_grandchild", "ses_neighbor"} {
		for _, dir := range []string{filepath.Join(home, ".coslash", "opencode-clients"), filepath.Join(filepath.Dir(path), "storage", "session_diff")} {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(`[]`), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	permissions := filepath.Join(home, ".coslash", "opencode-permissions")
	if err := os.MkdirAll(permissions, 0700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ses_child", "ses_neighbor"} {
		if err := os.WriteFile(filepath.Join(permissions, "per_"+id+".json"), []byte(fmt.Sprintf(`{"sessionID":%q,"pid":0}`, id)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runSessionDelete = func(c context.Context, h, p, id string, f map[string]bool) error {
		if err := deleteDatabaseSession(c, h, p, id, f); err != nil {
			return err
		}
		cancel()
		return nil
	}
	if err := DeleteSession(ctx, home, "ses_target"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM session`) != 1 {
		t.Fatal("transaction not committed")
	}
	receipt := filepath.Join(home, ".coslash", "opencode-deletions", "ses_target.json")
	var journal deletionJournal
	if found, err := readDeletionJSON(context.Background(), receipt, 16<<20, &journal); err != nil || !found || !journal.Family["ses_grandchild"] {
		t.Fatalf("durable ownership missing: %v", err)
	}
	runSessionDelete = deleteDatabaseSession
	if err := DeleteSession(context.Background(), home, "ses_target"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ses_target", "ses_child", "ses_grandchild"} {
		for _, dir := range []string{filepath.Join(home, ".coslash", "opencode-clients"), filepath.Join(filepath.Dir(path), "storage", "session_diff")} {
			if _, err := os.Stat(filepath.Join(dir, id+".json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("owned residue: %s", id)
			}
		}
	}
	for _, file := range []string{filepath.Join(home, ".coslash", "opencode-clients", "ses_neighbor.json"), filepath.Join(filepath.Dir(path), "storage", "session_diff", "ses_neighbor.json"), filepath.Join(permissions, "per_ses_neighbor.json")} {
		if _, err := os.Stat(file); err != nil {
			t.Fatal("neighbor artifact changed")
		}
	}
	if _, err := os.Stat(filepath.Join(permissions, "per_ses_child.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("child permission survives")
	}
	if _, err := os.Stat(receipt); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("completed journal survives")
	}
}

func TestDeleteRepairRetainsChangedPermissionOwner(t *testing.T) {
	home, _, db, _ := deleteFixture(t)
	path := filepath.Join(home, ".coslash", "opencode-permissions", "per_request.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"sessionID":"ses_target","pid":0}`), 0600); err != nil {
		t.Fatal(err)
	}
	runSessionDelete = func(c context.Context, h, p, id string, f map[string]bool) error {
		if err := deleteDatabaseSession(c, h, p, id, f); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(`{"sessionID":"ses_neighbor","pid":0}`), 0600)
	}
	if err := DeleteSession(context.Background(), home, "ses_target"); !errors.Is(err, ErrSessionDeleteFailed) {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("deleted a replacement neighbor record")
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM session WHERE id='ses_neighbor'`) != 1 {
		t.Fatal("neighbor row changed")
	}
	runSessionDelete = deleteDatabaseSession
	if err := DeleteSession(context.Background(), home, "ses_target"); !errors.Is(err, ErrSessionUnverified) {
		t.Fatalf("unsafe retry: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("retry deleted neighbor metadata")
	}
}

func TestDeleteRepairPermissionBoundsBeforeMutation(t *testing.T) {
	home, _, db, calls := deleteFixture(t)
	path := filepath.Join(home, ".coslash", "opencode-permissions", "per_request.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, (64<<10)+1), 0600); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSession(context.Background(), home, "ses_target"); !errors.Is(err, ErrSessionUnverified) {
		t.Fatalf("error=%v", err)
	}
	if *calls != 0 || rowCount(t, db, `SELECT COUNT(*) FROM session`) != 4 {
		t.Fatal("oversized metadata allowed mutation")
	}
}

func TestDeleteRepairExternalDataRootSymlink(t *testing.T) {
	home, path, db, _ := deleteFixture(t)
	root := t.TempDir()
	actual := filepath.Join(root, "real", "nested", "opencode")
	if err := os.MkdirAll(actual, 0700); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, filepath.Join(actual, "opencode.db")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "link", "nested"))
	if err := DeleteSession(context.Background(), home, "ses_target"); !errors.Is(err, ErrSessionUnverified) {
		t.Fatalf("error=%v", err)
	}
}

func TestDeleteRepairRechecksAfterJournalPublication(t *testing.T) {
	home, _, db, _ := deleteFixture(t)
	runSessionDelete = deleteDatabaseSession
	probes := 0
	deleteProcesses = func(context.Context) ([]tuiProcess, error) {
		probes++
		if probes == 4 {
			return []tuiProcess{{sessionID: "ses_target"}}, nil
		}
		return nil, nil
	}
	if err := DeleteSession(context.Background(), home, "ses_target"); !errors.Is(err, ErrSessionActive) {
		t.Fatalf("error=%v", err)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM session`) != 4 || rowCount(t, db, `SELECT COUNT(*) FROM message`) != 2 {
		t.Fatal("became active before DELETE but was mutated")
	}
}

func TestDeleteRetryReceiptSQLResidue(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, mode := range []string{"complete", "rollback", "ignored", "active", "late active", "unverified", "canceled", "missing receipt", "changed message owner", "changed part owner", "invalid keys", "replacement database", "unsupported layout"} {
			if legacy && mode == "changed part owner" {
				continue
			}
			t.Run(fmt.Sprintf("legacy=%v/%s", legacy, mode), func(t *testing.T) {
				home, path, db, _ := deleteFixture(t)
				if legacy {
					if _, err := db.Exec(`ALTER TABLE part DROP COLUMN session_id`); err != nil {
						t.Fatal(err)
					}
				}
				runSessionDelete = func(c context.Context, h, p, id string, f map[string]bool) error {
					if err := deleteDatabaseSession(c, h, p, id, f); err != nil {
						return err
					}
					query := `INSERT INTO part VALUES('prt_residue','ses_child','msg_target',100,'{}'),('prt_second','ses_grandchild','msg_target',100,'{}')`
					if legacy {
						query = `INSERT INTO part VALUES('prt_residue','msg_target',100,'{}'),('prt_second','msg_target',100,'{}')`
					}
					_, err := db.Exec(query)
					return err
				}
				if err := DeleteSession(context.Background(), home, "ses_target"); !errors.Is(err, ErrSessionDeleteFailed) {
					t.Fatalf("initial residue: %v", err)
				}
				if rowCount(t, db, `SELECT COUNT(*) FROM session`) != 1 {
					t.Fatal("initial transaction did not commit")
				}
				runSessionDelete = deleteDatabaseSession
				receipt := filepath.Join(home, ".coslash", "opencode-deletions", "ses_target.json")
				ctx := context.Background()
				want := ErrSessionDeleteFailed
				switch mode {
				case "complete":
					want = nil
				case "rollback", "ignored":
					action := "ABORT, 'synthetic retry failure'"
					if mode == "ignored" {
						action = "IGNORE"
					}
					if _, err := db.Exec(`CREATE TRIGGER retry_refusal BEFORE DELETE ON part WHEN OLD.id='prt_second' BEGIN SELECT RAISE(` + action + `); END`); err != nil {
						t.Fatal(err)
					}
				case "active":
					want = ErrSessionActive
					deleteProcesses = func(context.Context) ([]tuiProcess, error) { return []tuiProcess{{sessionID: "ses_child"}}, nil }
				case "late active":
					want = ErrSessionActive
					probes := 0
					deleteProcesses = func(context.Context) ([]tuiProcess, error) {
						probes++
						if probes == 4 {
							return []tuiProcess{{sessionID: "ses_child"}}, nil
						}
						return nil, nil
					}
				case "unverified":
					want = ErrSessionUnverified
					deleteProcesses = func(context.Context) ([]tuiProcess, error) { return nil, errors.New("synthetic probe failure") }
				case "canceled":
					want = context.Canceled
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				case "missing receipt":
					want = ErrSessionMissing
					if err := os.Remove(receipt); err != nil {
						t.Fatal(err)
					}
				case "changed message owner":
					want = ErrSessionUnverified
					if _, err := db.Exec(`INSERT INTO message VALUES('msg_target','ses_neighbor',100,'{}')`); err != nil {
						t.Fatal(err)
					}
				case "changed part owner":
					want = ErrSessionUnverified
					if _, err := db.Exec(`UPDATE part SET session_id='ses_neighbor' WHERE id='prt_residue'`); err != nil {
						t.Fatal(err)
					}
				case "invalid keys":
					want = ErrSessionUnverified
					var journal deletionJournal
					if _, err := readDeletionJSON(ctx, receipt, 16<<20, &journal); err != nil {
						t.Fatal(err)
					}
					for i := range journal.Keys {
						if journal.Keys[i].Column == "session_id" {
							journal.Keys[i].IDs["ses_neighbor"] = true
							break
						}
					}
					if err := saveDeletionJournal(ctx, receipt, &journal); err != nil {
						t.Fatal(err)
					}
				case "replacement database":
					runSessionDelete = func(c context.Context, h, p, id string, f map[string]bool) error {
						data, err := os.ReadFile(p)
						if err != nil {
							return err
						}
						if err := os.Rename(p, p+".original"); err != nil {
							return err
						}
						if err := os.WriteFile(p, data, 0600); err != nil {
							return err
						}
						return deleteDatabaseSession(c, h, p, id, f)
					}
				case "unsupported layout":
					want = ErrSessionUnverified
					if _, err := db.Exec(`CREATE TABLE unknown_session_store(session_id TEXT)`); err != nil {
						t.Fatal(err)
					}
				}
				err := DeleteSession(ctx, home, "ses_target")
				if (want == nil && err != nil) || (want != nil && !errors.Is(err, want)) {
					t.Fatalf("retry: %v, want %v", err, want)
				}
				wantParts := 3
				if want == nil {
					wantParts = 1
				}
				if rowCount(t, db, `SELECT COUNT(*) FROM part`) != wantParts || rowCount(t, db, `SELECT COUNT(*) FROM part WHERE id='prt_neighbor'`) != 1 || rowCount(t, db, `SELECT COUNT(*) FROM message WHERE id='msg_neighbor'`) != 1 || rowCount(t, db, `SELECT COUNT(*) FROM session WHERE id='ses_neighbor'`) != 1 {
					t.Fatal("residue/neighbor/rollback boundary changed")
				}
				if mode == "replacement database" {
					replacement, err := sql.Open("sqlite", readOnlyDatabaseDSN(path))
					if err != nil {
						t.Fatal(err)
					}
					defer replacement.Close()
					if rowCount(t, replacement, `SELECT COUNT(*) FROM part`) != 3 {
						t.Fatal("replacement database mutated")
					}
				}
				_, statErr := os.Stat(receipt)
				if want == nil || mode == "missing receipt" {
					if !errors.Is(statErr, os.ErrNotExist) {
						t.Fatal("completed or absent receipt remains")
					}
				} else if statErr != nil {
					t.Fatal("failed retry lost receipt")
				}
			})
		}
	}
}
