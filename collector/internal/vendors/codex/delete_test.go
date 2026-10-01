package codex

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
	_ "modernc.org/sqlite"
)

const deleteRootID = "11111111-2222-4333-8444-555555555555"
const deleteChildID = "66666666-7777-4888-8999-aaaaaaaaaaaa"
const deleteNeighborID = "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"

func closedForDelete(context.Context, []string) (map[string]struct{}, error) {
	return map[string]struct{}{}, nil
}

func deleteFixture(t *testing.T) (string, []string) {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, ".codex")
	paths := []string{
		writeDiscoveryRollout(t, filepath.Join(root, "sessions"), deleteRootID, deleteRootID, ""),
		writeDiscoveryRollout(t, filepath.Join(root, "archived_sessions"), deleteRootID, deleteRootID, ""),
		writeDiscoveryRollout(t, filepath.Join(root, "archived_sessions"), deleteChildID, deleteChildID, deleteRootID),
		writeDiscoveryRollout(t, filepath.Join(root, "sessions"), deleteNeighborID, deleteNeighborID, ""),
	}
	for _, field := range []string{"id", "session_id"} {
		name := "session_index.jsonl"
		if field == "session_id" {
			name = "history.jsonl"
		}
		content := ""
		for _, id := range []string{deleteRootID, deleteChildID, deleteNeighborID} {
			extra := `,"text":"synthetic","ts":1}`
			if field == "id" {
				extra = `,"thread_name":"synthetic","updated_at":"2026-10-01T00:00:00Z"}`
			}
			content += `{"` + field + `":"` + id + `"` + extra + "\n"
		}
		writeDeleteFile(t, filepath.Join(root, name), content)
	}
	writeDeleteFile(t, filepath.Join(root, "shell_snapshots", deleteRootID+".sh"), "true\n")
	writeDeleteFile(t, filepath.Join(root, "shell_snapshots", deleteChildID+".nonce.zsh"), "true\n")
	writeDeleteFile(t, filepath.Join(root, "shell_snapshots", deleteNeighborID+".sh"), "neighbor\n")
	writeDeleteFile(t, filepath.Join(root, "config.toml"), "# synthetic configuration\n")
	writeDeleteFile(t, filepath.Join(root, "thread-writer-locks", ".coordination.lock"), "")
	return root, paths
}

func writeDeleteFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func deleteSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			result[path] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDeleteSessionRefusesBeforeWrite(t *testing.T) {
	for _, tc := range []struct {
		name, id      string
		live          map[string]struct{}
		liveErr, want error
	}{
		{name: "invalid", id: "../" + deleteRootID, want: ErrSessionInvalid},
		{name: "missing", id: "01234567-89ab-4def-8123-456789abcdef", want: ErrSessionMissing},
		{name: "active root", id: deleteRootID, live: map[string]struct{}{deleteRootID: {}}, want: ErrSessionActive},
		{name: "active child", id: deleteRootID, live: map[string]struct{}{deleteChildID: {}}, want: ErrSessionActive},
		{name: "unverified", id: deleteRootID, liveErr: errors.New("probe failed"), want: ErrSessionUnverified},
		{name: "active neighbor", id: deleteRootID, live: map[string]struct{}{deleteNeighborID: {}}, want: ErrSessionUnverified},
		{name: "missing probe result", id: deleteRootID, want: ErrSessionUnverified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := deleteFixture(t)
			before := deleteSnapshot(t, root)
			called := false
			err := deleteSession(context.Background(), root, root, tc.id,
				func(context.Context, []string) (map[string]struct{}, error) { return tc.live, tc.liveErr },
				func(context.Context, string, string, string) error { called = true; return nil })
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if called || !reflect.DeepEqual(before, deleteSnapshot(t, root)) {
				t.Fatal("refusal changed data or invoked mutation")
			}
		})
	}
}

func TestDeleteSessionFamilyAndNeighbors(t *testing.T) {
	root, paths := deleteFixture(t)
	neighbor, _ := os.ReadFile(paths[3])
	var calls []string
	err := deleteSession(context.Background(), root, root, deleteRootID, closedForDelete,
		func(ctx context.Context, gotRoot, gotSQLite, id string) error {
			if gotRoot != root || gotSQLite != root {
				t.Fatal("wrong roots")
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("mutation context is unbounded")
			}
			calls = append(calls, id)
			return nil // Injected boundary leaves filesystem cleanup to the adapter.
		})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{deleteRootID}) {
		t.Fatalf("calls = %v", calls)
	}
	for _, path := range paths[:3] {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rollout remains: %v", err)
		}
	}
	for _, name := range []string{"session_index.jsonl", "history.jsonl"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), deleteRootID) || strings.Contains(string(data), deleteChildID) || !strings.Contains(string(data), deleteNeighborID) {
			t.Fatalf("incorrect %s cleanup", name)
		}
	}
	got, _ := os.ReadFile(paths[3])
	if string(got) != string(neighbor) {
		t.Fatal("neighbor changed")
	}
	for _, id := range []string{deleteRootID, deleteChildID} {
		matches, _ := filepath.Glob(filepath.Join(root, "shell_snapshots", id+".*"))
		if len(matches) > 0 {
			t.Fatal("snapshot remains")
		}
	}
	data, _ := os.ReadFile(filepath.Join(root, "shell_snapshots", deleteNeighborID+".sh"))
	if string(data) != "neighbor\n" {
		t.Fatal("neighbor snapshot changed")
	}
	data, _ = os.ReadFile(filepath.Join(root, "config.toml"))
	if string(data) != "# synthetic configuration\n" {
		t.Fatal("config changed")
	}
}

func TestDeleteSessionCancellationAndMutationFailure(t *testing.T) {
	for _, cancelBefore := range []bool{false, true} {
		t.Run(map[bool]string{false: "mutation failure", true: "cancelled"}[cancelBefore], func(t *testing.T) {
			root, _ := deleteFixture(t)
			before := deleteSnapshot(t, root)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelBefore {
				cancel()
			}
			failure := errors.New("mutation failure")
			err := deleteSession(ctx, root, root, deleteRootID, closedForDelete, func(context.Context, string, string, string) error { return failure })
			want := failure
			if cancelBefore {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
			if !cancelBefore {
				data, err := os.ReadFile(filepath.Join(root, ".coslash-delete-"+deleteRootID+".json"))
				if err != nil {
					t.Fatal(err)
				}
				before[filepath.Join(root, ".coslash-delete-"+deleteRootID+".json")] = string(data)
			}
			if !reflect.DeepEqual(before, deleteSnapshot(t, root)) {
				t.Fatal("failure changed files")
			}
		})
	}
	root, _ := deleteFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	before := deleteSnapshot(t, root)
	err := deleteSession(ctx, root, root, deleteRootID, closedForDelete, func(ctx context.Context, _, _, _ string) error { cancel(); return ctx.Err() })
	data, readErr := os.ReadFile(filepath.Join(root, ".coslash-delete-"+deleteRootID+".json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	before[filepath.Join(root, ".coslash-delete-"+deleteRootID+".json")] = string(data)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(before, deleteSnapshot(t, root)) {
		t.Fatal("mutation cancellation continued cleanup")
	}
}

func TestDeleteSessionRejectsUnsafeInventory(t *testing.T) {
	for _, name := range []string{"malformed header", "malformed index", "symlink", "compressed only", "unknown database", "database child"} {
		t.Run(name, func(t *testing.T) {
			root, paths := deleteFixture(t)
			switch name {
			case "malformed header":
				writeDeleteFile(t, paths[2], "{}\n")
			case "malformed index":
				writeDeleteFile(t, filepath.Join(root, "history.jsonl"), "{}\n")
			case "symlink":
				outside := t.TempDir()
				writeDeleteFile(t, filepath.Join(outside, "outside.jsonl"), "outside\n")
				if err := os.Symlink(outside, filepath.Join(root, "sessions", "linked")); err != nil {
					t.Skip(err)
				}
			case "compressed only":
				writeDeleteFile(t, filepath.Join(root, "archived_sessions", "rollout-"+deleteChildID+".jsonl.zst"), "compressed")
				if err := os.Remove(paths[2]); err != nil {
					t.Fatal(err)
				}
			case "unknown database":
				writeDeleteFile(t, filepath.Join(root, "state_999.sqlite"), "unknown")
			case "database child":
				db := deleteDB(t, root, "state_5.sqlite", `CREATE TABLE threads (id TEXT, rollout_path TEXT); CREATE TABLE thread_spawn_edges(parent_thread_id TEXT,child_thread_id TEXT);`)
				_, err := db.Exec(`INSERT INTO thread_spawn_edges VALUES (?,?)`, deleteRootID, "01234567-89ab-4def-8123-456789abcdef")
				if err != nil {
					t.Fatal(err)
				}
				db.Close()
			}
			before := deleteSnapshot(t, root)
			called := false
			err := deleteSession(context.Background(), root, root, deleteRootID, closedForDelete, func(context.Context, string, string, string) error { called = true; return nil })
			if !errors.Is(err, ErrSessionUnverified) || called || !reflect.DeepEqual(before, deleteSnapshot(t, root)) {
				t.Fatalf("unsafe inventory accepted: %v", err)
			}
		})
	}
}

func deleteDB(t *testing.T, root, name, schema string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestDeleteSessionPartialDatabaseResidue(t *testing.T) {
	root, _ := deleteFixture(t)
	db := deleteDB(t, root, "state_5.sqlite", `CREATE TABLE threads(id TEXT,rollout_path TEXT);`)
	if _, err := db.Exec(`INSERT INTO threads(id) VALUES (?)`, deleteRootID); err != nil {
		t.Fatal(err)
	}
	err := deleteSession(context.Background(), root, root, deleteRootID, closedForDelete, func(context.Context, string, string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := deleteDatabaseFiles(context.Background(), root, map[string]bool{deleteRootID: true}, nil, true, false); err != nil {
		t.Fatal("direct cleanup left database residue")
	}
}

func TestDeleteSessionEffectiveRoots(t *testing.T) {
	home := t.TempDir()
	root, _ := deleteFixture(t)
	t.Setenv("CODEX_HOME", root)
	t.Setenv("CODEX_SQLITE_HOME", "")
	got, sqliteRoot, err := deleteDataRoots(home)
	if err != nil || got != mustCanonicalRoot(t, root) || sqliteRoot != mustCanonicalRoot(t, root) {
		t.Fatalf("effective roots = %q, %q, %v", got, sqliteRoot, err)
	}
	sqlite := t.TempDir()
	t.Setenv("CODEX_SQLITE_HOME", sqlite)
	_, sqliteRoot, err = deleteDataRoots(home)
	if err != nil || sqliteRoot != sqlite {
		t.Fatal("sqlite override ignored")
	}
	t.Setenv("CODEX_HOME", "relative")
	if _, _, err = deleteDataRoots(home); !errors.Is(err, ErrSessionUnverified) || !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing relative vendor root accepted")
	}
}

func TestDeleteSessionRechecksResidueLiveness(t *testing.T) {
	root, _ := deleteFixture(t)
	before := deleteSnapshot(t, root)
	probes := 0
	err := deleteSession(context.Background(), root, root, deleteRootID,
		func(context.Context, []string) (map[string]struct{}, error) {
			probes++
			if probes == 1 {
				return map[string]struct{}{}, nil
			}
			return map[string]struct{}{deleteChildID: {}}, nil
		}, func(context.Context, string, string, string) error { return nil })
	if !errors.Is(err, ErrSessionActive) || !reflect.DeepEqual(before, deleteSnapshot(t, root)) {
		t.Fatalf("active residue was removed: %v", err)
	}
}

func TestDeleteSessionBoundsHeader(t *testing.T) {
	root, paths := deleteFixture(t)
	writeDeleteFile(t, paths[0], `{"type":"session_meta","payload":{"id":"`+deleteRootID+`","cwd":"`+strings.Repeat("x", 2<<20)+`"}}`+"\n")
	called := false
	err := deleteSession(context.Background(), root, root, deleteRootID, closedForDelete, func(context.Context, string, string, string) error { called = true; return nil })
	if !errors.Is(err, ErrSessionUnverified) || called {
		t.Fatalf("oversized header accepted: %v", err)
	}
}

func TestDeleteSessionPreservesConcurrentMetadata(t *testing.T) {
	root, _ := deleteFixture(t)
	path := filepath.Join(root, "history.jsonl")
	before, after, err := filterDeleteRows(context.Background(), path, "session_id", map[string]bool{deleteRootID: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	current := strings.Replace(string(before), "synthetic", "changed", 1) + `{"session_id":"` + deleteNeighborID + `","text":"new neighbor"}` + "\n"
	writeDeleteFile(t, path, current)
	if err := replaceDeleteRows(context.Background(), path, before, after, nil); err == nil {
		t.Fatal("concurrent metadata overwritten")
	}
	got, _ := os.ReadFile(path)
	if string(got) != current {
		t.Fatal("concurrent row lost")
	}
}

func TestDeleteSessionRemovalFailure(t *testing.T) {
	root, _ := deleteFixture(t)
	err := deleteSession(context.Background(), root, root, deleteRootID, closedForDelete, func(context.Context, string, string, string) error {
		path := filepath.Join(root, "shell_snapshots", deleteRootID+".sh")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		writeDeleteFile(t, filepath.Join(path, "residue"), "synthetic")
		return nil
	})
	if !errors.Is(err, ErrSessionDeleteFailed) {
		t.Fatalf("partial removal succeeded: %v", err)
	}
}

func TestDeleteSessionNewDescendantRefused(t *testing.T) {
	root, _ := deleteFixture(t)
	err := deleteSession(context.Background(), root, root, deleteRootID, closedForDelete, func(context.Context, string, string, string) error {
		writeDiscoveryRollout(t, filepath.Join(root, "sessions"), "01234567-89ab-4def-8123-456789abcdef", "01234567-89ab-4def-8123-456789abcdef", deleteRootID)
		return nil
	})
	if !errors.Is(err, ErrSessionDeleteFailed) {
		t.Fatalf("changed family succeeded: %v", err)
	}
}

func TestDeleteOutputBound(t *testing.T) {
	var output boundedDeleteOutput
	n, err := output.Write(make([]byte, 2<<20))
	if err != nil || n != 2<<20 || output.Len() != 1<<20 || !output.overflow {
		t.Fatal("diagnostics unbounded")
	}
}

func deleteTestCommand(t *testing.T, name, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix command fixture")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	writeDeleteFile(t, path, "#!/bin/sh\n"+script)
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestDeleteDatabaseTransactionFailureAndRetry(t *testing.T) {
	root, paths := deleteFixture(t)
	db := deleteDB(t, root, "state_5.sqlite", `CREATE TABLE threads(id TEXT PRIMARY KEY,rollout_path TEXT);`)
	if _, err := db.Exec(`INSERT INTO threads VALUES (?,?),(?,?)`, deleteRootID, paths[0], deleteNeighborID, paths[3]); err != nil {
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
	err = deleteSession(context.Background(), root, root, deleteRootID, closedForDelete, nil)
	if !errors.Is(err, ErrSessionDeleteFailed) {
		t.Fatalf("locked transaction accepted: %v", err)
	}
	var count int
	if err := conn.QueryRowContext(context.Background(), `SELECT count(*) FROM threads`).Scan(&count); err != nil || count != 2 {
		t.Fatal("transaction failure lost a session")
	}
	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatal("transaction failure continued rollout removal")
	}
	if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	if err := deleteSession(context.Background(), root, root, deleteRootID, closedForDelete, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM threads WHERE id=?`, deleteNeighborID).Scan(&count); err != nil || count != 1 {
		t.Fatal("retry changed neighbor")
	}
}

func TestDeleteLiveProbeErrors(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		wantError    bool
	}{
		{"no handles", "exit 1\n", false},
		{"diagnostic", "printf 'probe failed\\n' >&2\nexit 1\n", true},
		{"failure", "exit 2\n", true},
		{"partial", "printf 'p123\\nn/unknown.jsonl\\n'\nexit 1\n", true},
		{"unexpected path", "printf 'p123\\nn/unknown.jsonl\\n'\nexit 0\n", true},
		{"incomplete fields", "printf 'p123\\n'\nexit 0\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, paths := deleteFixture(t)
			before := deleteSnapshot(t, root)
			deleteTestCommand(t, "lsof", tc.script)
			result, err := deletePlatformLiveSessions(context.Background(), paths)
			if (err != nil) != tc.wantError {
				t.Fatalf("probe result = %v, %v", result, err)
			}
			if !reflect.DeepEqual(before, deleteSnapshot(t, root)) {
				t.Fatal("probe changed files")
			}
		})
	}
}

func TestDeleteWindowsOpenFilesRefusesUnknownAndFindsChildren(t *testing.T) {
	files := []string{"rollout-" + deleteRootID + ".jsonl", "rollout-" + deleteChildID + ".jsonl"}
	probe := func(group []string) ([]uint32, error) {
		for _, file := range group {
			if strings.Contains(file, deleteChildID) {
				return []uint32{1}, nil
			}
		}
		return nil, nil
	}
	live, err := deleteWindowsLiveSessions(context.Background(), files, probe)
	if err != nil || len(live) != 1 {
		t.Fatalf("Windows live = %v, %v", live, err)
	}
	if _, ok := live[deleteChildID]; !ok {
		t.Fatal("child handle missed")
	}
	failure := errors.New("Restart Manager failed")
	_, err = deleteWindowsLiveSessions(context.Background(), files, func([]string) ([]uint32, error) { return nil, failure })
	if !errors.Is(err, failure) {
		t.Fatalf("unverified probe accepted: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = deleteWindowsLiveSessions(ctx, files, func([]string) ([]uint32, error) { t.Fatal("cancelled probe ran"); return nil, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
}
func TestReviewNeighborHistoryHandle(t *testing.T) {
	root, _ := deleteFixture(t)
	path := filepath.Join(root, "history.jsonl")
	writer, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	err = deleteSession(context.Background(), root, root, deleteRootID, closedForDelete,
		func(context.Context, string, string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	row := `{"session_id":"` + deleteNeighborID + `","text":"CONCURRENT_NEIGHBOR_PROMPT"}` + "\n"
	if _, err = writer.WriteString(row); err != nil {
		t.Fatal(err)
	}
	if err = writer.Sync(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "CONCURRENT_NEIGHBOR_PROMPT") {
		t.Fatal("adapter returned success but a neighbor append through an already-open history handle was lost")
	}
}

func TestReviewRetryAfterPartialMutationFailure(t *testing.T) {
	root, paths := deleteFixture(t)
	failure := errors.New("local mutation failed after rollout removal")
	err := deleteSession(context.Background(), root, root, deleteRootID, closedForDelete,
		func(context.Context, string, string, string) error {
			for _, p := range paths[:3] {
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
			}
			return failure
		})
	if !errors.Is(err, ErrSessionDeleteFailed) {
		t.Fatal(err)
	}
	called := false
	err = deleteSession(context.Background(), root, root, deleteRootID, closedForDelete,
		func(context.Context, string, string, string) error { called = true; return nil })
	data, _ := os.ReadFile(filepath.Join(root, "history.jsonl"))
	if err != nil || strings.Contains(string(data), deleteRootID) || strings.Contains(string(data), deleteChildID) {
		t.Fatalf("retry did not finish attributable cleanup: %v (mutation called: %v)", err, called)
	}
}
func TestReviewRejectUnknownKnownTableSchema(t *testing.T) {
	root, _ := deleteFixture(t)
	db := deleteDB(t, root, "logs_2.sqlite", `CREATE TABLE logs (session_id TEXT, body TEXT);`)
	if _, err := db.Exec(`INSERT INTO logs VALUES (?, 'target transcript')`, deleteRootID); err != nil {
		t.Fatal(err)
	}
	db.Close()
	err := deleteDatabaseFiles(context.Background(), root, map[string]bool{deleteRootID: true}, nil, true, false)
	if err == nil {
		t.Fatal("absence verification succeeded against a logs table without its ownership column")
	}
}

func TestReviewSupportedMessageBoardDatabase(t *testing.T) {
	root, _ := deleteFixture(t)
	db := deleteDB(t, root, "agent_message_board_1.sqlite", `
CREATE TABLE deleted_boards (board TEXT PRIMARY KEY NOT NULL);
CREATE TABLE channels (board TEXT NOT NULL,name TEXT NOT NULL,name_search TEXT NOT NULL,created_at TEXT NOT NULL,timestamp INTEGER NOT NULL,author TEXT NOT NULL,PRIMARY KEY(board,name));
CREATE TABLE posts (seq INTEGER PRIMARY KEY AUTOINCREMENT,board TEXT NOT NULL,id TEXT NOT NULL,channel TEXT NOT NULL,root TEXT NOT NULL,author TEXT NOT NULL,timestamp INTEGER NOT NULL,body_search TEXT NOT NULL,payload TEXT NOT NULL,request_id TEXT NOT NULL,request TEXT NOT NULL,UNIQUE(board,id),UNIQUE(board,request_id));
CREATE TABLE subscriptions (board TEXT NOT NULL,target TEXT NOT NULL,agent TEXT NOT NULL,PRIMARY KEY(board,target,agent));
CREATE TABLE subscription_opt_outs (board TEXT NOT NULL,target TEXT NOT NULL,agent TEXT NOT NULL,PRIMARY KEY(board,target,agent));`)
	defer db.Close()
	for _, id := range []string{deleteRootID, deleteChildID, deleteNeighborID} {
		for _, query := range []string{
			`INSERT INTO channels VALUES (?, 'synthetic','synthetic','synthetic',1,'synthetic')`,
			`INSERT INTO posts (board,id,channel,root,author,timestamp,body_search,payload,request_id,request) VALUES (?, 'synthetic','synthetic','synthetic','synthetic',1,'synthetic','{}','synthetic','{}')`,
			`INSERT INTO subscriptions VALUES (?, 'synthetic','synthetic')`,
			`INSERT INTO subscription_opt_outs VALUES (?, 'synthetic','synthetic')`,
		} {
			if _, err := db.Exec(query, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	err := deleteSession(context.Background(), root, root, deleteRootID, closedForDelete, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"channels", "posts", "subscriptions", "subscription_opt_outs"} {
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM `+table+` WHERE board=?`, deleteNeighborID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("neighbor board changed in %s: %d %v", table, count, err)
		}
	}
	var tombstones int
	if err := db.QueryRow(`SELECT count(*) FROM deleted_boards`).Scan(&tombstones); err != nil || tombstones != 2 {
		t.Fatalf("tombstones: %d %v", tombstones, err)
	}
}
func TestReviewMigrationCursorIsNotSessionData(t *testing.T) {
	root, _ := deleteFixture(t)
	db := deleteDB(t, root, "state_5.sqlite", `CREATE TABLE rollout_migration_state(migration_id TEXT PRIMARY KEY,last_checked_thread_created_at INTEGER,last_checked_thread_id TEXT,updated_at INTEGER NOT NULL);`)
	if _, err := db.Exec(`INSERT INTO rollout_migration_state VALUES ('synthetic',1,?,1)`, deleteRootID); err != nil {
		t.Fatal(err)
	}
	db.Close()
	err := deleteSession(context.Background(), root, root, deleteRootID, closedForDelete, func(context.Context, string, string, string) error { return nil })
	if err != nil {
		t.Fatalf("non-owning migration watermark makes a fully removed family fail: %v", err)
	}
}

func TestDeleteSessionWriterCoordinationRefusesBeforeWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		want error
	}{
		{".coordination", ErrSessionUnverified}, {deleteRootID, ErrSessionActive}, {deleteNeighborID, ErrSessionUnverified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := deleteFixture(t)
			path := filepath.Join(root, "thread-writer-locks", tc.name+".lock")
			writeDeleteFile(t, path, "")
			file, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if err := tryDeleteFileLock(file); err != nil {
				t.Fatal(err)
			}
			before := deleteSnapshot(t, root)
			err = deleteSession(context.Background(), root, root, deleteRootID, closedForDelete, func(context.Context, string, string, string) error {
				t.Fatal("mutation ran while a writer owns storage")
				return nil
			})
			if !errors.Is(err, tc.want) || (tc.want == ErrSessionUnverified && errors.Is(err, ErrSessionActive)) || !reflect.DeepEqual(before, deleteSnapshot(t, root)) {
				t.Fatalf("coordination refusal changed storage: %v", err)
			}
		})
	}
}

func TestDeleteSessionHistoryLockFailureCanRetry(t *testing.T) {
	root, _ := deleteFixture(t)
	file, err := os.OpenFile(filepath.Join(root, "history.jsonl"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := tryDeleteFileLock(file); err != nil {
		t.Fatal(err)
	}
	run := func(context.Context, string, string, string) error { return nil }
	err = deleteSession(context.Background(), root, root, deleteRootID, closedForDelete, run)
	if !errors.Is(err, ErrSessionUnverified) {
		t.Fatalf("history lock failure: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := deleteSession(context.Background(), root, root, deleteRootID, closedForDelete, run); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteDatabaseRejectsUnknownBookkeepingSchema(t *testing.T) {
	root, _ := deleteFixture(t)
	db := deleteDB(t, root, "state_5.sqlite", `CREATE TABLE rollout_migration_state (thread_id TEXT);`)
	db.Close()
	if err := deleteDatabaseFiles(context.Background(), root, map[string]bool{deleteRootID: true}, nil, false, false); err == nil {
		t.Fatal("unsupported bookkeeping schema admitted")
	}
}

func TestDeleteIndexPreservesLengthInodeAndNeighborAppend(t *testing.T) {
	root, _ := deleteFixture(t)
	path := filepath.Join(root, "session_index.jsonl")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	err = deleteSession(context.Background(), root, root, deleteRootID, closedForDelete, func(context.Context, string, string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() {
		t.Fatal("index inode or length changed")
	}
	row := `{"id":"` + deleteNeighborID + `","thread_name":"appended"}` + "\n"
	if _, err := writer.WriteString(row); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.HasSuffix(string(data), row) {
		t.Fatal("neighbor append handle lost")
	}

	rows, present, err := ReadSessionIndexRowsAtPathContext(context.Background(), vendors.LocalReadSource, path, map[string]bool{deleteRootID: true, deleteNeighborID: true})
	if err != nil || !present || len(rows[deleteRootID]) != 0 || len(rows[deleteNeighborID]) != 2 {
		t.Fatal("index reader did not preserve neighbor or ignore blanked target")
	}

}

func TestDeleteSessionDirectDatabaseFamilyAndNeighbors(t *testing.T) {
	root, paths := deleteFixture(t)
	db := deleteDB(t, root, "state_5.sqlite", `CREATE TABLE threads (id TEXT PRIMARY KEY,rollout_path TEXT); CREATE TABLE thread_dynamic_tools(thread_id TEXT REFERENCES threads(id),name TEXT); CREATE TABLE thread_spawn_edges(parent_thread_id TEXT,child_thread_id TEXT);`)
	for i, id := range []string{deleteRootID, deleteChildID, deleteNeighborID} {
		path := paths[0]
		if i == 1 {
			path = paths[2]
		}
		if i == 2 {
			path = paths[3]
		}
		if _, err := db.Exec(`INSERT INTO threads VALUES (?,?); INSERT INTO thread_dynamic_tools VALUES (?,'synthetic')`, id, path, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO thread_spawn_edges VALUES (?,?)`, deleteRootID, deleteChildID); err != nil {
		t.Fatal(err)
	}
	err := deleteSession(context.Background(), root, root, deleteRootID, closedForDelete, func(context.Context, string, string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM threads WHERE id=?`, deleteNeighborID).Scan(&count); err != nil || count != 1 {
		t.Fatal("neighbor database row changed")
	}
	if err := db.QueryRow(`SELECT count(*) FROM thread_dynamic_tools WHERE thread_id=?`, deleteNeighborID).Scan(&count); err != nil || count != 1 {
		t.Fatal("neighbor tool row changed")
	}
}

func TestDeleteSessionDetectsTargetIndexReappearanceAndReplacement(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "target reappearance", true: "inode replacement"}[replacement], func(t *testing.T) {
			root, _ := deleteFixture(t)
			probes := 0
			live := func(context.Context, []string) (map[string]struct{}, error) {
				probes++
				if probes == 4 {
					path := filepath.Join(root, "session_index.jsonl")
					if replacement {
						data, err := os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
						temp := path + ".tmp"
						writeDeleteFile(t, temp, string(data))
						if err := os.Rename(temp, path); err != nil {
							t.Fatal(err)
						}
					} else {
						file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
						if err != nil {
							t.Fatal(err)
						}
						_, err = file.WriteString(`{"id":"` + deleteRootID + `","thread_name":"reappeared"}` + "\n")
						file.Close()
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				return map[string]struct{}{}, nil
			}
			if err := deleteSession(context.Background(), root, root, deleteRootID, live, nil); !errors.Is(err, ErrSessionDeleteFailed) {
				t.Fatalf("concurrent index change accepted: %v", err)
			}
			if probes != 4 {
				t.Fatalf("missing final probe: %d", probes)
			}
		})
	}
}

func TestDeleteSessionJournalFreeResidueIsUnverified(t *testing.T) {
	root, paths := deleteFixture(t)
	for _, path := range paths[:3] {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	before := deleteSnapshot(t, root)
	if err := deleteSession(context.Background(), root, root, deleteRootID, closedForDelete, nil); !errors.Is(err, ErrSessionUnverified) {
		t.Fatalf("known residue reported missing: %v", err)
	}
	if !reflect.DeepEqual(before, deleteSnapshot(t, root)) {
		t.Fatal("uncertain family ownership changed storage")
	}
}

func TestDeleteMetadataPreservesAppendBeforePublication(t *testing.T) {
	for _, entry := range []struct{ name, field string }{{"history.jsonl", "session_id"}, {"session_index.jsonl", "id"}} {
		t.Run(entry.name, func(t *testing.T) {
			root, _ := deleteFixture(t)
			path := filepath.Join(root, entry.name)
			before, after, err := filterDeleteRows(context.Background(), path, entry.field, map[string]bool{deleteRootID: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				t.Fatal(err)
			}
			row := `{"` + entry.field + `":"` + deleteNeighborID + `","text":"appended"}` + "\n"
			if _, err := file.WriteString(row); err != nil {
				t.Fatal(err)
			}
			file.Close()
			if err := replaceDeleteRows(context.Background(), path, before, after, nil); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != string(after)+row {
				t.Fatal("neighbor append before publication changed")
			}
		})
	}
}

func TestDeleteSessionPublicAPIUsesOnlyLocalStorage(t *testing.T) {
	root, paths := deleteFixture(t)
	t.Setenv("CODEX_HOME", root)
	t.Setenv("CODEX_SQLITE_HOME", root)
	deleteTestCommand(t, "lsof", "exit 1\n")
	db := deleteDB(t, root, "state_5.sqlite", `CREATE TABLE threads(id TEXT PRIMARY KEY,rollout_path TEXT);`)
	if _, err := db.Exec(`INSERT INTO threads VALUES (?,?),(?,?)`, deleteRootID, paths[0], deleteNeighborID, paths[3]); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSession(context.Background(), filepath.Dir(root), deleteRootID); err != nil {
		t.Fatal(err)
	}
	files, err := FilesContext(context.Background())
	if err != nil || len(files) != 1 || SessionIDFromRollout(files[0]) != deleteNeighborID {
		t.Fatal("deleted family remains discoverable")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM threads WHERE id=?`, deleteRootID).Scan(&count); err != nil || count != 0 {
		t.Fatal("persisted target remains readable")
	}
	if err := db.QueryRow(`SELECT count(*) FROM threads WHERE id=?`, deleteNeighborID).Scan(&count); err != nil || count != 1 {
		t.Fatal("neighbor persisted read failed")
	}
	if err := DeleteSession(context.Background(), filepath.Dir(root), deleteRootID); !errors.Is(err, ErrSessionMissing) {
		t.Fatalf("absent family: %v", err)
	}
}

func TestDeleteFileLockFailureIsUnverified(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "closed-lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tryDeleteFileLock(file); err == nil || errors.Is(err, ErrSessionActive) {
		t.Fatalf("unverifiable handle classified active: %v", err)
	}
}

func TestDeleteDatabaseRejectsUnknownMigrationSchema(t *testing.T) {
	root, _ := deleteFixture(t)
	db := deleteDB(t, root, "state_5.sqlite", `CREATE TABLE _sqlx_migrations(thread_id TEXT);`)
	db.Close()
	if err := deleteDatabaseFiles(context.Background(), root, map[string]bool{deleteRootID: true}, nil, false, false); err == nil {
		t.Fatal("unsupported migration metadata schema admitted")
	}
}

func TestDeleteSessionCanonicalAndRelativeRootsPreserveDuplicateNeighbors(t *testing.T) {
	for _, kind := range []string{"symlink parent", "relative"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			defaultRoot, _ := deleteFixture(t)
			defaultBefore := deleteSnapshot(t, defaultRoot)
			actualFixture, _ := deleteFixture(t)
			actual := filepath.Join(base, "target", "data")
			if err := os.MkdirAll(filepath.Join(base, "target", "subdir"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(actualFixture, actual); err != nil {
				t.Fatal(err)
			}
			decoyFixture, _ := deleteFixture(t)
			decoy := filepath.Join(base, "data")
			if err := os.Rename(decoyFixture, decoy); err != nil {
				t.Fatal(err)
			}
			decoyBefore := deleteSnapshot(t, decoy)
			override := "target/data"
			t.Chdir(base)
			if kind == "symlink parent" {
				link := filepath.Join(base, "link")
				if err := os.Symlink(filepath.Join(base, "target", "subdir"), link); err != nil {
					t.Skip(err)
				}
				override = link + string(filepath.Separator) + ".." + string(filepath.Separator) + "data"
			}
			t.Setenv("CODEX_HOME", override)
			t.Setenv("CODEX_SQLITE_HOME", "")
			root, sqliteRoot, err := deleteDataRoots(filepath.Dir(defaultRoot))
			if err != nil {
				t.Fatal(err)
			}
			if root != mustCanonicalRoot(t, actual) || sqliteRoot != root {
				t.Fatalf("roots=%q,%q", root, sqliteRoot)
			}

			rawFiles, err := filesForDataRootSourceContext(t.Context(), vendors.LocalReadSource, actual)
			if err != nil {
				t.Fatal(err)
			}
			db := deleteDB(t, actual, "state_5.sqlite", `CREATE TABLE threads(id TEXT PRIMARY KEY,rollout_path TEXT);`)
			for _, path := range rawFiles {
				if _, err := db.Exec(`INSERT INTO threads VALUES (?,?)`, SessionIDFromRollout(path), path); err != nil {
					t.Fatal(err)
				}
			}

			before := deleteSnapshot(t, actual)
			if err := deleteSession(t.Context(), root, sqliteRoot, deleteRootID, closedForDelete, nil); err != nil {
				t.Fatal(err)
			}
			var remaining int
			if err := db.QueryRow(`SELECT count(*) FROM threads WHERE id=?`, deleteNeighborID).Scan(&remaining); err != nil || remaining != 1 {
				t.Fatal("database neighbor changed")
			}
			if err := db.QueryRow(`SELECT count(*) FROM threads WHERE id IN (?,?)`, deleteRootID, deleteChildID).Scan(&remaining); err != nil || remaining != 0 {
				t.Fatal("owned database rows remain")
			}
			after := deleteSnapshot(t, actual)
			for path, content := range before {
				if strings.Contains(path, deleteNeighborID) && after[path] != content {
					t.Fatal("neighbor artifact changed")
				}
			}
			files, err := filesForDataRootSourceContext(t.Context(), vendors.LocalReadSource, root)
			if err != nil || len(files) != 1 || SessionIDFromRollout(files[0]) != deleteNeighborID {
				t.Fatal("wrong family removed")
			}
			for _, name := range []string{"history.jsonl", "session_index.jsonl"} {
				data, err := os.ReadFile(filepath.Join(actual, name))
				if err != nil || !strings.Contains(string(data), deleteNeighborID) || strings.Contains(string(data), deleteRootID) || strings.Contains(string(data), deleteChildID) {
					t.Fatal("shared rows incorrect")
				}
			}
			if !reflect.DeepEqual(defaultBefore, deleteSnapshot(t, defaultRoot)) || !reflect.DeepEqual(decoyBefore, deleteSnapshot(t, decoy)) {
				t.Fatal("duplicate-ID storage neighbor changed")
			}
		})
	}
}

func TestDeleteSessionReceiptRefusesExpandedOwnershipBeforeWrite(t *testing.T) {
	for _, kind := range []string{"new child", "new path for same member"} {
		t.Run(kind, func(t *testing.T) {
			root, paths := deleteFixture(t)
			stop := errors.New("retain receipt")
			if err := deleteSession(t.Context(), root, root, deleteRootID, closedForDelete, func(context.Context, string, string, string) error { return stop }); !errors.Is(err, stop) {
				t.Fatal(err)
			}
			original := deleteSnapshot(t, root)
			id := deleteRootID
			name := "rollout-extra-" + id + ".jsonl"
			if kind == "new child" {
				id = "01234567-89ab-4def-8123-456789abcdef"
				name = "rollout-extra-" + id + ".jsonl"
			}
			extra := filepath.Join(root, "sessions", name)
			if kind == "new child" {
				extra = writeDiscoveryRollout(t, filepath.Dir(extra), "extra-"+id, id, deleteRootID)
				writeDeleteFile(t, filepath.Join(root, "shell_snapshots", id+".sh"), "synthetic extra")
				for _, entry := range []struct{ name, field string }{{"history.jsonl", "session_id"}, {"session_index.jsonl", "id"}} {
					path := filepath.Join(root, entry.name)
					writeDeleteFile(t, path, original[path]+`{"`+entry.field+`":"`+id+`","text":"synthetic extra"}`+"\n")
				}
			} else {
				if err := os.Rename(paths[0], extra); err != nil {
					t.Fatal(err)
				}
			}
			before := deleteSnapshot(t, root)
			err := deleteSession(t.Context(), root, root, deleteRootID, closedForDelete, func(context.Context, string, string, string) error {
				t.Fatal("mutation admitted stale receipt")
				return nil
			})
			if !errors.Is(err, ErrSessionUnverified) || !reflect.DeepEqual(before, deleteSnapshot(t, root)) {
				t.Fatalf("expanded ownership changed storage: %v", err)
			}
			if kind == "new child" {
				if err := os.Remove(extra); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(root, "shell_snapshots", id+".sh")); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"history.jsonl", "session_index.jsonl"} {
					path := filepath.Join(root, name)
					writeDeleteFile(t, path, original[path])
				}
			} else if err := os.Rename(extra, paths[0]); err != nil {
				t.Fatal(err)
			}
			if err := deleteSession(t.Context(), root, root, deleteRootID, closedForDelete, nil); err != nil {
				t.Fatalf("unchanged-family retry failed: %v", err)
			}
		})
	}
}

func TestDeleteRowsHeldHistoryHandle(t *testing.T) {
	for _, scenario := range []string{"position and ownership", "closed handle", "inode replacement", "oversized row"} {
		t.Run(scenario, func(t *testing.T) {
			root, _ := deleteFixture(t)
			path := filepath.Join(root, "history.jsonl")
			if scenario == "oversized row" {
				writeDeleteFile(t, path, strings.Repeat(" ", maxSessionIndexRowBytes+1)+"\n")
			}
			file, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if err := tryDeleteFileLock(file); err != nil {
				t.Fatal(err)
			}
			if _, err := file.Seek(17, 0); err != nil {
				t.Fatal(err)
			}
			if scenario == "closed handle" {
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "inode replacement" {
				if err := os.Rename(path, path+".previous"); err != nil {
					t.Skip(err)
				}
				writeDeleteFile(t, path, `{"session_id":"`+deleteNeighborID+`","text":"replacement"}`+"\n")
			}
			before, after, err := filterDeleteRows(t.Context(), path, "session_id", map[string]bool{deleteRootID: true, deleteChildID: true}, file)
			if scenario != "position and ownership" {
				if err == nil {
					t.Fatal("owned handle ignored")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			position, err := file.Seek(0, 1)
			if err != nil || position != 17 {
				t.Fatal("bounded read moved owned handle position")
			}
			if len(before) == 0 || len(before) != len(after) {
				t.Fatal("fixed-length filter failed")
			}
			if err := replaceDeleteRows(t.Context(), path, before, after, file); err != nil {
				t.Fatal(err)
			}
			verified, filtered, err := filterDeleteRows(t.Context(), path, "session_id", map[string]bool{deleteRootID: true, deleteChildID: true}, file)
			if err != nil || !reflect.DeepEqual(verified, filtered) || strings.Contains(string(verified), deleteRootID) || !strings.Contains(string(verified), deleteNeighborID) {
				t.Fatal("owned verification failed")
			}
			position, err = file.Seek(0, 1)
			if err != nil || position != 17 {
				t.Fatal("write/verification moved owned handle position")
			}
			other, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			if err := tryDeleteFileLock(other); !errors.Is(err, ErrSessionActive) {
				t.Fatalf("owned lock released during filtering: %v", err)
			}
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			if _, _, err := filterDeleteRows(canceled, path, "session_id", map[string]bool{deleteRootID: true}, file); !errors.Is(err, context.Canceled) {
				t.Fatal("owned read ignored cancellation")
			}
		})
	}
}
