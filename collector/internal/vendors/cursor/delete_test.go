package cursor

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const deleteID = "a1111111-1111-4111-8111-111111111111"
const deleteChild = "22222222-2222-4222-8222-222222222222"
const deleteNeighbor = "33333333-3333-4333-8333-333333333333"

func closedCursor(context.Context) error { return nil }

func deleteFixture(t *testing.T, home, id, parent string) string {
	t.Helper()
	path := filepath.Join(home, ".cursor", "chats", "workspace", id, "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE meta(key TEXT, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(map[string]any{"agentId": id, "subagentInfo": map[string]string{"parentAgentId": parent}})
	if _, err := db.Exec(`INSERT INTO meta VALUES('0', ?)`, hex.EncodeToString(value)); err != nil {
		t.Fatal(err)
	}
	return path
}

func deleteFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"role":"user","message":{"content":[{"type":"text","text":"disposable"}]}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteCursorFamily(t *testing.T) {
	home := t.TempDir()
	target := deleteFixture(t, home, deleteID, "")
	child := deleteFixture(t, home, deleteChild, deleteID)
	neighbor := deleteFixture(t, home, deleteNeighbor, "")
	dir := filepath.Join(ProjectsRoot(home), "workspace", "agent-transcripts", deleteID)
	transcript := filepath.Join(dir, deleteID+".jsonl")
	embedded := filepath.Join(dir, "subagents", deleteChild+".jsonl")
	separate := filepath.Join(ProjectsRoot(home), "workspace", "agent-transcripts", deleteChild, deleteChild+".jsonl")
	flat := filepath.Join(ProjectsRoot(home), "legacy", "agent-transcripts", deleteID+".jsonl")
	other := filepath.Join(ProjectsRoot(home), "workspace", "agent-transcripts", deleteNeighbor, deleteNeighbor+".jsonl")
	config := filepath.Join(home, ".cursor", "config.json")
	for _, path := range []string{transcript, embedded, separate, flat, other, config, target + "-shm"} {
		deleteFile(t, path)
	}
	if err := deleteSession(context.Background(), home, strings.ToUpper(deleteID), closedCursor, os.Remove); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{target, child, transcript, embedded, separate, flat, target + "-shm"} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("artifact remains: %s: %v", path, err)
		}
	}
	for _, path := range []string{neighbor, other, config} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	if stores := cursorChatStores(home, []string{deleteID, deleteChild}); len(stores) != 0 {
		t.Fatalf("resume stores remain: %v", stores)
	}
	if db, err := openCursorDB(target); !errors.Is(err, os.ErrNotExist) {
		if db != nil {
			db.Close()
		}
		t.Fatalf("target still readable: %v", err)
	}
	if _, err := parseTranscript(transcript); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transcript still readable: %v", err)
	}
	db, err := openCursorDB(neighbor)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM meta`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("neighbor changed: %d %v", count, err)
	}
	db.Close()
	for _, path := range []string{other, config} {
		contents, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(contents), "disposable") {
			t.Fatalf("neighbor content changed: %q %v", contents, err)
		}
	}

	files, err := FilesSourceContext(context.Background(), vendors.LocalReadSource, ProjectsRoot(home))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if IDFromPath(path) == deleteID || IDFromPath(path) == deleteChild {
			t.Fatalf("still discoverable: %s", path)
		}
	}
	if err := deleteSession(context.Background(), home, deleteID, closedCursor, os.Remove); !errors.Is(err, ErrDeleteMissing) {
		t.Fatalf("missing: %v", err)
	}
}

func TestDeleteCursorRefusesBeforeMutation(t *testing.T) {
	for _, cause := range []error{ErrDeleteActive, ErrDeleteUnverified, context.Canceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			home := t.TempDir()
			path := deleteFixture(t, home, deleteID, "")
			err := deleteSession(context.Background(), home, deleteID, func(context.Context) error { return cause }, os.Remove)
			if !errors.Is(err, cause) {
				t.Fatalf("wrong refusal cause: %v, want %v", err, cause)
			}
			if cause == ErrDeleteActive && errors.Is(err, ErrDeleteUnverified) {
				t.Fatalf("ambiguous API cause: %v", err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDeleteCursorIDEReference(t *testing.T) {
	for _, location := range []string{"global", "workspace", "search", "linux"} {
		t.Run(location, func(t *testing.T) {
			home := t.TempDir()
			target := deleteFixture(t, home, deleteID, "")
			path := filepath.Join(cursorGlobalStorage(home), "state.vscdb")
			if location == "workspace" {
				path = filepath.Join(filepath.Dir(cursorGlobalStorage(home)), "workspaceStorage", "one", "state.vscdb")
			}
			if location == "search" {
				path = filepath.Join(cursorGlobalStorage(home), "conversation-search.db")
			}
			if location == "linux" {
				path = filepath.Join(home, ".config", "Cursor", "User", "globalStorage", "state.vscdb")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`CREATE TABLE cursorDiskKV(key TEXT,value TEXT); INSERT INTO cursorDiskKV VALUES(?, '{}')`, "bubbleId:"+deleteID+":one"); err != nil {
				t.Fatal(err)
			}
			db.Close()
			if err := deleteSession(context.Background(), home, deleteID, closedCursor, os.Remove); !errors.Is(err, ErrDeleteUnverified) {
				t.Fatalf("IDE reference: %v", err)
			}
			if _, err := os.Stat(target); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDeleteCursorInvalidAndSymlinks(t *testing.T) {
	for _, id := range []string{"", "../" + deleteID, deleteID + "/x", "*"} {
		if err := DeleteSession(context.Background(), t.TempDir(), id); !errors.Is(err, ErrDeleteInvalid) {
			t.Fatalf("invalid %q: %v", id, err)
		}
	}
	for _, location := range []string{"root", "workspace", "target", "child"} {
		t.Run(location, func(t *testing.T) {
			home := t.TempDir()
			target := deleteFixture(t, home, deleteID, "")
			outside := t.TempDir()
			sentinel := filepath.Join(outside, "keep")
			deleteFile(t, sentinel)
			path := filepath.Dir(target)
			switch location {
			case "root":
				path = filepath.Join(home, ".cursor")
			case "workspace":
				path = filepath.Dir(path)
			case "child":
				path = filepath.Join(path, "link")
			}
			if location != "child" {
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(outside, path); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			if err := deleteSession(context.Background(), home, deleteID, closedCursor, os.Remove); !errors.Is(err, ErrDeleteUnverified) {
				t.Fatalf("symlink: %v", err)
			}
			if _, err := os.Stat(sentinel); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDeleteCursorPartialFailureAndVerification(t *testing.T) {
	for _, silent := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial", true: "remaining"}[silent], func(t *testing.T) {
			home := t.TempDir()
			target := deleteFixture(t, home, deleteID, "")
			neighbor := deleteFixture(t, home, deleteNeighbor, "")
			calls := 0
			remove := func(path string) error {
				calls++
				if silent {
					return nil
				}
				if calls == 2 {
					return os.ErrPermission
				}
				return os.Remove(path)
			}
			if err := deleteSession(context.Background(), home, deleteID, closedCursor, remove); !errors.Is(err, ErrDeleteFailed) {
				t.Fatalf("partial: %v", err)
			}
			if _, err := os.Stat(neighbor); err != nil {
				t.Fatal(err)
			}
			if silent {
				if _, err := os.Stat(target); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestCursorDeletionProcessProbe(t *testing.T) {
	for _, test := range []struct {
		output string
		want   error
	}{
		{"12 /usr/bin/node /home/u/.local/share/cursor-agent/versions/one/index.js", ErrDeleteActive},
		{"12 /Applications/Cursor.app/Contents/MacOS/Cursor", ErrDeleteActive},
		{"12 /usr/local/bin/agent --resume " + deleteID, ErrDeleteActive},
		{"12 /usr/bin/node unrelated.js\n13 /bin/bash", nil},
		{"12", ErrDeleteUnverified},
		{"", ErrDeleteUnverified},
		{"oops malformed", ErrDeleteUnverified},
	} {
		if err := cursorDeletionProcesses(test.output); !errors.Is(err, test.want) {
			t.Errorf("%q: %v, want %v", test.output, err, test.want)
		}
	}
}

func TestDeleteCursorDatabaseUncertainty(t *testing.T) {
	for _, scenario := range []string{"corrupt-cli", "bad-meta", "wal", "unknown-ide", "neighbor-ide", "child-ide", "orphan-transcript"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			target := deleteFixture(t, home, deleteID, "")
			switch scenario {
			case "corrupt-cli":
				if err := os.WriteFile(target, []byte("not sqlite"), 0600); err != nil {
					t.Fatal(err)
				}
			case "bad-meta":
				db, _ := sql.Open("sqlite", target)
				if _, err := db.Exec(`UPDATE meta SET value='not hex'`); err != nil {
					t.Fatal(err)
				}
				db.Close()
			case "wal":
				deleteFile(t, target+"-wal")
			case "unknown-ide", "neighbor-ide", "child-ide":
				path := filepath.Join(cursorGlobalStorage(home), "state.vscdb")
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				db, _ := sql.Open("sqlite", path)
				query := `CREATE TABLE cursorDiskKV(key TEXT,value TEXT); INSERT INTO cursorDiskKV VALUES(?, '{}')`
				id := deleteNeighbor
				if scenario == "child-ide" {
					id = deleteChild
					deleteFixture(t, home, deleteChild, deleteID)
				}
				if scenario == "unknown-ide" {
					query = `CREATE TABLE futureCursorSchema(key TEXT,value TEXT); INSERT INTO futureCursorSchema VALUES(?, '{}')`
				}
				if _, err := db.Exec(query, "composerData:"+id); err != nil {
					t.Fatal(err)
				}
				db.Close()
			case "orphan-transcript":
				deleteFile(t, filepath.Join(ProjectsRoot(home), "one", "agent-transcripts", deleteID, "subagents", deleteChild+".jsonl"))
				deleteFile(t, filepath.Join(ProjectsRoot(home), "two", "agent-transcripts", deleteChild, deleteChild+".jsonl"))
			}
			err := deleteSession(context.Background(), home, deleteID, closedCursor, os.Remove)
			if scenario == "neighbor-ide" || scenario == "orphan-transcript" {
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "orphan-transcript" {
					if _, err := os.Stat(filepath.Join(ProjectsRoot(home), "two", "agent-transcripts", deleteChild)); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("child remains: %v", err)
					}
				}
			} else {
				if !errors.Is(err, ErrDeleteUnverified) {
					t.Fatalf("unsafe database: %v", err)
				}
				if _, err := os.Stat(target); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestDeleteCursorConflictingLineage(t *testing.T) {
	for _, scenario := range []string{"root-as-child", "cycle"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			target := deleteFixture(t, home, deleteID, "")
			if scenario == "cycle" {
				db, _ := sql.Open("sqlite", target)
				value, _ := json.Marshal(map[string]any{"agentId": deleteID, "subagentInfo": map[string]string{"parentAgentId": deleteChild}})
				if _, err := db.Exec(`UPDATE meta SET value=?`, hex.EncodeToString(value)); err != nil {
					t.Fatal(err)
				}
				db.Close()
				deleteFixture(t, home, deleteChild, deleteID)
			} else {
				deleteFixture(t, home, deleteChild, "")
				deleteFile(t, filepath.Join(ProjectsRoot(home), "one", "agent-transcripts", deleteID, "subagents", deleteChild+".jsonl"))
			}
			if err := deleteSession(context.Background(), home, deleteID, closedCursor, os.Remove); !errors.Is(err, ErrDeleteUnverified) {
				t.Fatalf("lineage: %v", err)
			}
			if _, err := os.Stat(target); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDeleteCursorCancelledBeforeMutation(t *testing.T) {
	home := t.TempDir()
	target := deleteFixture(t, home, deleteID, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := deleteSession(ctx, home, deleteID, closedCursor, os.Remove)
	if !errors.Is(err, ErrDeleteUnverified) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal(err)
	}
}

func TestCursorDeletionWindowsProbe(t *testing.T) {
	for _, test := range []struct {
		output string
		want   error
	}{
		{`[]`, nil},
		{`[{"Name":"node.exe","ExecutablePath":"C:\\tools\\node.exe","CommandLine":"node.exe app.js"}]`, nil},
		{`[{"Name":"Cursor.exe","ExecutablePath":null,"CommandLine":null}]`, ErrDeleteActive},
		{`[{"Name":"node.exe","ExecutablePath":"C:\\Users\\test\\AppData\\Local\\cursor-agent\\versions\\one\\node.exe","CommandLine":"node.exe index.js"}]`, ErrDeleteActive},
		{`[{"Name":"node.exe","ExecutablePath":null,"CommandLine":null}]`, ErrDeleteUnverified},
		{``, ErrDeleteUnverified},
		{`garbage`, ErrDeleteUnverified},
	} {
		if err := cursorDeletionWindowsProcesses(test.output); !errors.Is(err, test.want) {
			t.Errorf("%s: got %v, want %v", test.output, err, test.want)
		}
	}
}

func retainedDeleteWAL(t *testing.T, path, statement string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatal(err)
	}
	main, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wal, err := os.ReadFile(path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, main, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", wal, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteCursorRetainedWAL(t *testing.T) {
	home := t.TempDir()
	scratch := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, scratch)
	}
	target := deleteFixture(t, home, deleteID, "")
	child := deleteFixture(t, home, deleteChild, "")
	neighbor := deleteFixture(t, home, deleteNeighbor, "")
	value, _ := json.Marshal(map[string]any{"agentId": deleteChild, "subagentInfo": map[string]string{"parentAgentId": deleteID}})
	retainedDeleteWAL(t, child, `UPDATE meta SET value=?`, hex.EncodeToString(value))
	retainedDeleteWAL(t, target, `CREATE TABLE disposable_data(value TEXT); INSERT INTO disposable_data VALUES('target transcript')`)
	retainedDeleteWAL(t, neighbor, `CREATE TABLE disposable_data(value TEXT); INSERT INTO disposable_data VALUES('neighbor transcript')`)
	originals := map[string][]byte{}
	for _, path := range []string{neighbor, neighbor + "-wal"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		originals[path] = data
	}
	if err := deleteSession(context.Background(), home, deleteID, func(context.Context) error { return ErrDeleteActive }, os.Remove); !errors.Is(err, ErrDeleteActive) {
		t.Fatalf("active WAL: %v", err)
	}
	if _, err := os.Stat(target + "-shm"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("active probe touched storage: %v", err)
	}
	entriesBefore, err := os.ReadDir(scratch)
	if err != nil || len(entriesBefore) != 0 {
		t.Fatalf("active probe inspected WAL: %v %v", entriesBefore, err)
	}
	probeCalls := 0
	err = deleteSession(context.Background(), home, deleteID, func(context.Context) error { probeCalls++; return nil }, os.Remove)
	if err != nil {
		t.Fatal(err)
	}
	if probeCalls != 2 {
		t.Fatalf("need pre-read and pre-remove probes: %d", probeCalls)
	}
	for _, path := range []string{target, target + "-wal", child, child + "-wal"} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("WAL artifact remains: %s: %v", path, err)
		}
	}
	for path, before := range originals {
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(before) {
			t.Fatalf("neighbor changed: %s %v", path, err)
		}
	}
	if _, err := os.Stat(neighbor + "-shm"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("neighbor shm was created: %v", err)
	}
	entries, err := os.ReadDir(scratch)
	if err != nil || len(entries) != 0 {
		t.Fatalf("inspection snapshots remain: %v %v", entries, err)
	}
}

func TestDeleteCursorReviewPartialRetryLosesChild(t *testing.T) {
	home := deleteReviewHome(t)
	deleteFixture(t, home, deleteID, "")
	child := deleteFixture(t, home, deleteChild, deleteID)
	residue := filepath.Join(filepath.Dir(child), "zz-output.txt")
	deleteFile(t, residue)
	err := deleteSession(context.Background(), home, deleteID, closedCursor, func(path string) error {
		if path == residue {
			return os.ErrPermission
		}
		return os.Remove(path)
	})
	if !errors.Is(err, ErrDeleteFailed) {
		t.Fatalf("first deletion = %v", err)
	}
	if _, err := os.Stat(child); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected removed child lineage: %v", err)
	}
	err = deleteSession(context.Background(), home, deleteID, closedCursor, os.Remove)
	t.Logf("retry returned %v", err)
	if _, err := os.Stat(residue); err == nil {
		t.Fatalf("retry left child family residue while reporting success")
	}
}
func TestDeleteCursorReviewReplacementBeforeMutation(t *testing.T) {
	home := deleteReviewHome(t)
	target := deleteFixture(t, home, deleteID, "")
	neighbor := deleteFixture(t, home, deleteNeighbor, "")
	saved := filepath.Join(home, "original-target.db")
	calls := 0
	err := deleteSession(context.Background(), home, deleteID, func(context.Context) error {
		calls++
		if calls == 2 {
			if err := os.Rename(target, saved); err != nil {
				return err
			}
			if err := os.Rename(neighbor, target); err != nil {
				return err
			}
		}
		return nil
	}, os.Remove)
	t.Logf("replacement deletion returned %v", err)
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted replacement database owned by neighboring session")
	}
}
func TestDeleteCursorReviewEmptyRetainedWAL(t *testing.T) {
	home := deleteReviewHome(t)
	target := deleteFixture(t, home, deleteID, "")
	if err := os.WriteFile(target+"-wal", nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := deleteSession(context.Background(), home, deleteID, closedCursor, os.Remove); err != nil {
		t.Fatalf("empty WAL refuses closed store: %v", err)
	}
}
func TestDeleteCursorReviewInvalidWALVersion(t *testing.T) {
	home := deleteReviewHome(t)
	target := deleteFixture(t, home, deleteID, "")
	header := make([]byte, 32)
	binary.BigEndian.PutUint32(header, 0x377f0682)
	binary.BigEndian.PutUint32(header[4:], 1)
	if err := os.WriteFile(target+"-wal", header, 0600); err != nil {
		t.Fatal(err)
	}
	err := deleteSession(context.Background(), home, deleteID, closedCursor, os.Remove)
	if !errors.Is(err, ErrDeleteUnverified) {
		t.Fatalf("invalid WAL accepted: %v", err)
	}
}
func TestDeleteCursorReviewAbsentRootsTranscriptOnly(t *testing.T) {
	home := deleteReviewHome(t)
	transcript := filepath.Join(ProjectsRoot(home), "workspace", "agent-transcripts", deleteID, deleteID+".jsonl")
	deleteFile(t, transcript)
	if err := deleteSession(context.Background(), home, deleteID, closedCursor, os.Remove); err != nil {
		t.Fatal(err)
	}
}
func TestDeleteCursorReviewIntermediateSymlinkReplacement(t *testing.T) {
	home := deleteReviewHome(t)
	target := deleteFixture(t, home, deleteID, "")
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "keep")
	deleteFile(t, sentinel)
	calls := 0
	err := deleteSession(context.Background(), home, deleteID, func(context.Context) error {
		calls++
		if calls == 2 {
			workspace := filepath.Dir(filepath.Dir(target))
			if err := os.Rename(workspace, workspace+"-saved"); err != nil {
				return err
			}
			return os.Symlink(outside, workspace)
		}
		return nil
	}, os.Remove)
	if !errors.Is(err, ErrDeleteFailed) {
		t.Fatalf("replacement symlink not refused: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal(err)
	}
}

func deleteReviewHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return home
}

func TestDeleteCursorReviewPartialRetryLeavesFlatChildTranscript(t *testing.T) {
	home := deleteReviewHome(t)
	deleteFixture(t, home, deleteID, "")
	deleteFixture(t, home, deleteChild, deleteID)
	transcript := filepath.Join(ProjectsRoot(home), "workspace", "agent-transcripts", deleteChild+".txt")
	deleteFile(t, transcript)
	err := deleteSession(context.Background(), home, deleteID, closedCursor, func(path string) error {
		if path == transcript {
			return os.ErrPermission
		}
		return os.Remove(path)
	})
	if !errors.Is(err, ErrDeleteFailed) {
		t.Fatalf("first deletion = %v", err)
	}
	err = deleteSession(context.Background(), home, deleteID, closedCursor, os.Remove)
	t.Logf("retry returned %v", err)
	if data, e := os.ReadFile(transcript); e == nil {
		t.Fatalf("child transcript still readable after successful retry: %s", data)
	}
}

func TestDeleteCursorReviewClosedWALNeighborPreserved(t *testing.T) {
	home := deleteReviewHome(t)
	deleteFixture(t, home, deleteID, "")
	neighbor := deleteFixture(t, home, deleteNeighbor, "")
	db, err := sql.Open("sqlite", neighbor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL; CREATE TABLE extra(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(neighbor + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("precondition: sidecar %s: %v", suffix, err)
		}
	}
	err = deleteSession(context.Background(), home, deleteID, closedCursor, os.Remove)
	t.Logf("delete returned %v", err)
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(neighbor + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("inspection created neighbor sidecar %s: %v", suffix, err)
		}
	}
}

func TestDeleteCursorReviewClosedWALTargetWithoutSidecars(t *testing.T) {
	home := deleteReviewHome(t)
	target := deleteFixture(t, home, deleteID, "")
	db, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL; CREATE TABLE extra(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(target + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("precondition sidecar %s: %v", suffix, err)
		}
	}
	err = deleteSession(context.Background(), home, deleteID, closedCursor, os.Remove)
	t.Logf("delete returned %v", err)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, e := os.Stat(target + suffix)
		size := int64(-1)
		if info != nil {
			size = info.Size()
		}
		t.Logf("suffix %q: stat %v size %d", suffix, e, size)
	}
	if err != nil {
		t.Fatalf("closed WAL store deletion failed: %v", err)
	}
}

func TestDeleteCursorSharedWALInspectionDoesNotMutate(t *testing.T) {
	for _, zero := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "empty"}[zero], func(t *testing.T) {
			home := deleteReviewHome(t)
			target := deleteFixture(t, home, deleteID, "")
			path := filepath.Join(cursorGlobalStorage(home), "state.vscdb")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE cursorDiskKV(key TEXT,value TEXT); INSERT INTO cursorDiskKV VALUES(?, '{}')`, "composerData:"+deleteNeighbor); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if zero {
				if err := os.WriteFile(path+"-wal", nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := deleteSession(context.Background(), home, deleteID, closedCursor, os.Remove); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) {
				t.Fatalf("shared database changed: %v", err)
			}
			for _, suffix := range []string{"-wal", "-shm"} {
				info, err := os.Stat(path + suffix)
				if zero && suffix == "-wal" {
					if err != nil || info.Size() != 0 {
						t.Fatalf("empty WAL changed: %v", err)
					}
				} else if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("created shared sidecar %s: %v", suffix, err)
				}
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("target remains: %v", err)
			}
		})
	}
}

func TestDeleteCursorAbsentRootRetryAndFinalVerification(t *testing.T) {
	home := deleteReviewHome(t)
	target := deleteFixture(t, home, deleteID, "")
	child := deleteFixture(t, home, deleteChild, deleteID)
	neighbor := deleteFixture(t, home, deleteNeighbor, "")
	transcript := filepath.Join(ProjectsRoot(home), "one", "agent-transcripts", deleteChild+".txt")
	deleteFile(t, transcript)
	recordPath := filepath.Join(home, ".coslash", "deletions", "cursor", deleteID+".json")
	err := deleteSession(context.Background(), home, deleteID, closedCursor, func(path string) error {
		if path == transcript {
			return nil
		}
		return os.Remove(path)
	})
	if !errors.Is(err, ErrDeleteFailed) {
		t.Fatalf("false removal succeeded: %v", err)
	}
	for _, path := range []string{target, child} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("root/child store survived: %s: %v", path, err)
		}
	}
	if _, err := os.Stat(recordPath); err != nil {
		t.Fatalf("durable evidence missing: %v", err)
	}
	if err := deleteSession(context.Background(), home, deleteID, closedCursor, nil); err != nil {
		t.Fatalf("rootless retry failed: %v", err)
	}
	for _, path := range []string{transcript, recordPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("family/record remains: %s: %v", path, err)
		}
	}
	if _, err := os.Stat(neighbor); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteCursorIdentityRevalidation(t *testing.T) {
	for _, scenario := range []string{"same-id-replacement", "in-place", "between-removals"} {
		t.Run(scenario, func(t *testing.T) {
			home := deleteReviewHome(t)
			target := deleteFixture(t, home, deleteID, "")
			first := filepath.Join(filepath.Dir(target), "aa-output.txt")
			next := filepath.Join(filepath.Dir(target), "zz-output.txt")
			deleteFile(t, first)
			deleteFile(t, next)
			replace := func(path string) error {
				saved := filepath.Join(home, filepath.Base(path)+"-saved")
				if err := os.Rename(path, saved); err != nil {
					return err
				}
				data, err := os.ReadFile(saved)
				if err != nil {
					return err
				}
				return os.WriteFile(path, data, 0600)
			}
			calls := 0
			probe := func(context.Context) error {
				calls++
				if calls == 2 {
					switch scenario {
					case "same-id-replacement":
						return replace(target)
					case "in-place":
						return os.WriteFile(next, []byte("neighbor replacement"), 0600)
					}
				}
				return nil
			}
			remove := func(path string) error {
				if err := os.Remove(path); err != nil {
					return err
				}
				if scenario == "between-removals" && path == first {
					return replace(next)
				}
				return nil
			}
			err := deleteSession(context.Background(), home, deleteID, probe, remove)
			if !errors.Is(err, ErrDeleteFailed) {
				t.Fatalf("replacement accepted: %v", err)
			}
			preserved := target
			if scenario != "same-id-replacement" {
				preserved = next
			}
			if _, err := os.Stat(preserved); err != nil {
				t.Fatalf("replacement deleted: %v", err)
			}
		})
	}
}

func TestDeleteCursorRejectsInvalidPendingOwnership(t *testing.T) {
	for _, scenario := range []string{"disconnected-family", "outside-path", "changed-residue", "changed-owner"} {
		t.Run(scenario, func(t *testing.T) {
			home := deleteReviewHome(t)
			deleteFixture(t, home, deleteID, "")
			child := deleteFixture(t, home, deleteChild, deleteID)
			residue := filepath.Join(ProjectsRoot(home), "one", "agent-transcripts", deleteChild+".txt")
			deleteFile(t, residue)
			err := deleteSession(context.Background(), home, deleteID, closedCursor, func(path string) error {
				if path == residue {
					return os.ErrPermission
				}
				return os.Remove(path)
			})
			if !errors.Is(err, ErrDeleteFailed) {
				t.Fatal(err)
			}
			recordPath := filepath.Join(home, ".coslash", "deletions", "cursor", deleteID+".json")
			if scenario == "changed-residue" {
				if err := os.WriteFile(residue, []byte("new unrelated content"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if scenario == "changed-owner" {
				deleteFixture(t, home, deleteChild, "")
			} else {
				data, err := os.ReadFile(recordPath)
				if err != nil {
					t.Fatal(err)
				}
				var record cursorDeleteRecord
				if err := json.Unmarshal(data, &record); err != nil {
					t.Fatal(err)
				}
				if scenario == "disconnected-family" {
					record.Family[deleteChild] = ""
				} else {
					record.Files[0].Path = filepath.Join(home, "keep.json")
					deleteFile(t, record.Files[0].Path)
				}
				data, err = json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(recordPath, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := deleteSession(context.Background(), home, deleteID, closedCursor, os.Remove); !errors.Is(err, ErrDeleteUnverified) {
				t.Fatalf("invalid retry accepted: %v", err)
			}
			if _, err := os.Stat(residue); err != nil {
				t.Fatalf("residue removed before validation: %v", err)
			}
			if scenario == "changed-owner" {
				if _, err := os.Stat(child); err != nil {
					t.Fatalf("new owner deleted: %v", err)
				}
			}
		})
	}
}

func TestDeleteCursorRetryRejectsIdenticalRegularReplacement(t *testing.T) {
	home := deleteReviewHome(t)
	deleteFixture(t, home, deleteID, "")
	deleteFixture(t, home, deleteChild, deleteID)
	residue := filepath.Join(ProjectsRoot(home), "one", "agent-transcripts", deleteChild+".txt")
	deleteFile(t, residue)
	err := deleteSession(context.Background(), home, deleteID, closedCursor, func(path string) error {
		if path == residue {
			return os.ErrPermission
		}
		return os.Remove(path)
	})
	if !errors.Is(err, ErrDeleteFailed) {
		t.Fatal(err)
	}
	original, err := os.Stat(residue)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(ProjectsRoot(home), "one", "agent-transcripts", deleteNeighbor+".txt")
	deleteFile(t, replacement)
	if err := os.Chtimes(replacement, original.ModTime(), original.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(residue, filepath.Join(home, "saved-child.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, residue); err != nil {
		t.Fatal(err)
	}
	if err := deleteSession(context.Background(), home, deleteID, closedCursor, os.Remove); !errors.Is(err, ErrDeleteUnverified) {
		t.Fatalf("identical replacement accepted on retry: %v", err)
	}
	if _, err := os.Stat(residue); err != nil {
		t.Fatalf("replacement was deleted: %v", err)
	}
}

func TestDeleteCursorHotJournalOwnership(t *testing.T) {
	home := deleteReviewHome(t)
	deleteFixture(t, home, deleteID, "")
	neighbor := deleteFixture(t, home, deleteNeighbor, "")
	db, err := sql.Open("sqlite", neighbor)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL; PRAGMA cache_size=1; CREATE TABLE payload(value BLOB); INSERT INTO payload VALUES(zeroblob(8192));`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"agentId": deleteNeighbor, "subagentInfo": map[string]string{"parentAgentId": deleteID}})
	if _, err := tx.Exec(`UPDATE meta SET value=?`, hex.EncodeToString(data)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		if _, err := tx.Exec(`INSERT INTO payload VALUES(zeroblob(8192))`); err != nil {
			t.Fatal(err)
		}
	}
	main, err := os.ReadFile(neighbor)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := os.ReadFile(neighbor + "-journal")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(neighbor, main, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(neighbor+"-journal", journal, 0600); err != nil {
		t.Fatal(err)
	}
	child, parent, err := cursorDeleteCLIParent(context.Background(), neighbor)
	if !errors.Is(err, ErrDeleteUnverified) || child != "" || parent != "" {
		t.Fatalf("uncertain ownership accepted: %s %s %v", child, parent, err)
	}
	t.Logf("private copy sees child=%s parent=%s err=%v journal-size=%d", child, parent, err, len(journal))
	control := filepath.Join(t.TempDir(), "store.db")
	if e := os.WriteFile(control, main, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(control+"-journal", journal, 0600); e != nil {
		t.Fatal(e)
	}
	recovered, e := sql.Open("sqlite", control)
	if e != nil {
		t.Fatal(e)
	}
	var recoveredHex string
	if e := recovered.QueryRow(`SELECT value FROM meta WHERE key='0'`).Scan(&recoveredHex); e != nil {
		t.Fatal(e)
	}
	recovered.Close()
	recoveredJSON, e := hex.DecodeString(recoveredHex)
	if e != nil {
		t.Fatal(e)
	}
	var meta struct {
		SubagentInfo struct {
			ParentAgentID string `json:"parentAgentId"`
		} `json:"subagentInfo"`
	}
	if e := json.Unmarshal(recoveredJSON, &meta); e != nil {
		t.Fatal(e)
	}
	t.Logf("normal writable SQLite recovery sees parent=%q", meta.SubagentInfo.ParentAgentID)
	if meta.SubagentInfo.ParentAgentID != "" {
		t.Fatal("control recovery did not restore committed neighbor ownership")
	}
	err = deleteSession(context.Background(), home, deleteID, closedCursor, func(string) error { t.Fatal("mutation before journal refusal"); return nil })
	if !errors.Is(err, ErrDeleteUnverified) {
		t.Fatal(err)
	}
	t.Logf("delete returned %v", err)
	for _, path := range []string{neighbor, neighbor + "-journal", filepath.Join(home, ".cursor", "chats", "workspace", deleteID, "store.db")} {
		if _, e := os.Stat(path); e != nil {
			t.Fatal(e)
		}
	}
	after, e := os.ReadFile(neighbor)
	if e != nil || string(after) != string(main) {
		t.Fatalf("neighbor database changed: %v", e)
	}
	after, e = os.ReadFile(neighbor + "-journal")
	if e != nil || string(after) != string(journal) {
		t.Fatalf("neighbor journal changed: %v", e)
	}
}

func TestDeleteCursorRollbackJournalBoundary(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, journal := range []string{"absent", "empty", "retained"} {
			t.Run(fmt.Sprintf("shared=%t/%s", shared, journal), func(t *testing.T) {
				home := deleteReviewHome(t)
				target := deleteFixture(t, home, deleteID, "")
				path := deleteFixture(t, home, deleteNeighbor, "")
				if shared {
					path = filepath.Join(cursorGlobalStorage(home), "state.vscdb")
					if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
						t.Fatal(err)
					}
					db, err := sql.Open("sqlite", path)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := db.Exec(`CREATE TABLE cursorDiskKV(key TEXT,value TEXT)`); err != nil {
						t.Fatal(err)
					}
					if err := db.Close(); err != nil {
						t.Fatal(err)
					}
				}
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if journal != "absent" {
					data := []byte(nil)
					if journal == "retained" {
						data = []byte("uncertain rollback state")
					}
					if err := os.WriteFile(path+"-journal", data, 0600); err != nil {
						t.Fatal(err)
					}
				}
				err = deleteSession(context.Background(), home, deleteID, closedCursor, nil)
				if journal == "absent" {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					if !errors.Is(err, ErrDeleteUnverified) {
						t.Fatalf("journal accepted: %v", err)
					}
					if _, err := os.Stat(target); err != nil {
						t.Fatal(err)
					}
				}
				after, err := os.ReadFile(path)
				if err != nil || string(before) != string(after) {
					t.Fatalf("source changed: %v", err)
				}
			})
		}
	}
}
