package cursor

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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
