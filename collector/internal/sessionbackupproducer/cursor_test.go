package sessionbackupproducer

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
	_ "modernc.org/sqlite"
)

const cursorTestID = "01234567-89ab-4def-8123-456789abcdef"

func writeCursorBackupFixture(t *testing.T, lane string) (string, string, string) {
	t.Helper()
	home, workspace := t.TempDir(), t.TempDir()
	transcript := filepath.Join(home, ".cursor", "projects", "repo", "agent-transcripts", cursorTestID, cursorTestID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"role":"user","message":{"content":[{"type":"text","text":"Build this"}]}}` + "\n" +
		`{"role":"assistant","message":{"content":[{"type":"text","text":"Done"}]}}` + "\n" +
		`{"type":"turn_ended","status":"success"}` + "\n")
	if err := os.WriteFile(transcript, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if lane == "cursor-ide" {
		state := filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
		if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", state)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec(`CREATE TABLE composerHeaders (composerId TEXT PRIMARY KEY, value TEXT, createdAt INTEGER, lastUpdatedAt INTEGER);
			CREATE TABLE cursorDiskKV (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
			t.Fatal(err)
		}
		header := `{"name":"IDE session","workspaceIdentifier":{"uri":{"fsPath":"` + workspace + `"}}}`
		if _, err := db.Exec(`INSERT INTO composerHeaders VALUES (?, ?, ?, ?)`, cursorTestID, header, 1_800_000_000_000, 1_800_000_001_000); err != nil {
			t.Fatal(err)
		}
	} else {
		store := filepath.Join(home, ".cursor", "chats", "workspace", cursorTestID, "store.db")
		if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(store), "meta.json"), []byte(`{"cwd":"`+workspace+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", store)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT); CREATE TABLE messages (ordinal INTEGER, text TEXT)`); err != nil {
			t.Fatal(err)
		}
		value := hex.EncodeToString([]byte(`{"agentId":"` + cursorTestID + `","name":"CLI session","createdAt":1700000000000}`))
		if _, err := db.Exec(`INSERT INTO meta VALUES ('0', ?); INSERT INTO messages VALUES (1, 'exact available')`, value); err != nil {
			t.Fatal(err)
		}
	}
	return home, workspace, transcript
}

func TestCursorLocalCompleteBundleRoundTripsIDEAndCLI(t *testing.T) {
	for _, lane := range []string{"cursor-ide", "cursor-cli"} {
		t.Run(lane, func(t *testing.T) {
			home, _, _ := writeCursorBackupFixture(t, lane)
			spool := t.TempDir()
			manager := New(Options{Root: spool, OpenSource: func(context.Context, Selection) (SourceHandle, error) {
				return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
			}})
			prepared, err := manager.Prepare(t.Context(), Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentCursor, SessionID: cursorTestID})
			if err != nil {
				var failure *PreparationError
				if errors.As(err, &failure) {
					t.Fatalf("%v: %#v", err, failure.Coverage.Problems)
				}
				t.Fatal(err)
			}
			verified, err := sessionbackupv1.VerifyDirectory(filepath.Join(spool, prepared.BundleID))
			if err != nil || verified.CompleteBackupSHA256 != prepared.BundleID {
				t.Fatalf("verify = %v", err)
			}
			if len(verified.Members) != 1 || verified.Source.Agent != vendors.AgentCursor {
				t.Fatalf("manifest = %#v", verified)
			}
			var parsed, metadataRows, sidecar bool
			for _, artifact := range verified.Artifacts {
				switch artifact.Kind {
				case sessionbackupv1.KindParsedSessionRecord:
					parsed = true
					data, readErr := os.ReadFile(filepath.Join(spool, prepared.BundleID, filepath.FromSlash(artifact.LogicalName)))
					if readErr != nil {
						t.Fatal(readErr)
					}
					record, decodeErr := fullsessionv1.Decode(data)
					if decodeErr != nil || record.Session.Entrypoint == nil || *record.Session.Entrypoint != lane {
						t.Fatalf("entrypoint = %#v, %v", record.Session.Entrypoint, decodeErr)
					}
				case sessionbackupv1.KindRawMetadataRows:
					metadataRows = true
				case sessionbackupv1.KindRawSidecar:
					sidecar = true
				}
			}
			if !parsed || !metadataRows || sidecar != (lane == "cursor-cli") {
				t.Fatalf("artifact coverage = %#v", verified.Artifacts)
			}
			if _, err := New(Options{Root: spool}).Open(prepared.BundleID); err != nil {
				t.Fatalf("restart open: %v", err)
			}
		})
	}
}

func TestCursorChangedTranscriptCannotPublish(t *testing.T) {
	home, _, transcript := writeCursorBackupFixture(t, "cursor-ide")
	spool := t.TempDir()
	manager := New(Options{Root: spool, OpenSource: func(context.Context, Selection) (SourceHandle, error) {
		return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}, AfterRawCopy: func() { _ = os.WriteFile(transcript, []byte("changed"), 0o600) }})
	prepared, err := manager.Prepare(t.Context(), Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentCursor, SessionID: cursorTestID})
	var failure *PreparationError
	if prepared != nil || !errors.As(err, &failure) || failure.Coverage.Problems[0].Code != sessionbackupv1.ProblemUnstable {
		t.Fatalf("prepared=%#v error=%v", prepared, err)
	}
}

func TestCursorWrongCLIStoreCannotPublish(t *testing.T) {
	home, _, _ := writeCursorBackupFixture(t, "cursor-cli")
	store := filepath.Join(home, ".cursor", "chats", "workspace", cursorTestID, "store.db")
	db, err := sql.Open("sqlite", store)
	if err != nil {
		t.Fatal(err)
	}
	other := hex.EncodeToString([]byte(`{"agentId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"}`))
	if _, err := db.Exec(`UPDATE meta SET value = ? WHERE key = '0'`, other); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	spool := t.TempDir()
	manager := New(Options{Root: spool, OpenSource: func(context.Context, Selection) (SourceHandle, error) {
		return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}})
	prepared, err := manager.Prepare(t.Context(), Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentCursor, SessionID: cursorTestID})
	var failure *PreparationError
	if prepared != nil || !errors.As(err, &failure) || failure.Coverage.Problems[0].Code != sessionbackupv1.ProblemUnattributable {
		t.Fatalf("prepared=%#v error=%v", prepared, err)
	}
	entries, err := os.ReadDir(spool)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed capture published files: %v, %v", entries, err)
	}
}

func TestCursorChangedIDERowsCannotPublish(t *testing.T) {
	home, _, _ := writeCursorBackupFixture(t, "cursor-ide")
	state := filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
	manager := New(Options{Root: t.TempDir(), OpenSource: func(context.Context, Selection) (SourceHandle, error) {
		return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}, AfterRawCopy: func() {
		db, err := sql.Open("sqlite", state)
		if err != nil {
			t.Error(err)
			return
		}
		defer db.Close()
		if _, err := db.Exec(`UPDATE composerHeaders SET value = value || ' ' WHERE composerId = ?`, cursorTestID); err != nil {
			t.Error(err)
		}
	}})
	prepared, err := manager.Prepare(t.Context(), Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentCursor, SessionID: cursorTestID})
	var failure *PreparationError
	if prepared != nil || !errors.As(err, &failure) || failure.Coverage.Problems[0].Code != sessionbackupv1.ProblemUnstable {
		t.Fatalf("prepared=%#v error=%v", prepared, err)
	}
}

func TestCursorMissingIDERowsCannotPublish(t *testing.T) {
	home, _, _ := writeCursorBackupFixture(t, "cursor-ide")
	state := filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
	if err := os.Remove(state); err != nil {
		t.Fatal(err)
	}
	manager := New(Options{Root: t.TempDir(), OpenSource: func(context.Context, Selection) (SourceHandle, error) {
		return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}})
	prepared, err := manager.Prepare(t.Context(), Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentCursor, SessionID: cursorTestID})
	var failure *PreparationError
	if prepared != nil || !errors.As(err, &failure) || failure.Coverage.Problems[0].Code != sessionbackupv1.ProblemUnattributable {
		t.Fatalf("prepared=%#v error=%v", prepared, err)
	}
}

func writeCursorStray(t *testing.T, home string, relative ...string) {
	t.Helper()
	path := filepath.Join(append([]string{home, ".cursor", "projects"}, relative...)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"role":"user","message":{"content":[{"type":"text","text":"Draft"}]}}` + "\n" +
		`{"type":"turn_ended","status":"success"}` + "\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func prepareCursorFixture(t *testing.T, home string) (*Prepared, error) {
	t.Helper()
	manager := New(Options{Root: t.TempDir(), OpenSource: func(context.Context, Selection) (SourceHandle, error) {
		return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}})
	return manager.Prepare(t.Context(), Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentCursor, SessionID: cursorTestID})
}

// A real Cursor home held one .jsonl under agent-transcripts whose directory
// and file stem were not session IDs. It names no session, so it must not
// block every Cursor family on the machine.
func TestCursorUnrelatedStrayJSONLDoesNotBlockFamily(t *testing.T) {
	for _, lane := range []string{"cursor-ide", "cursor-cli"} {
		t.Run(lane, func(t *testing.T) {
			home, _, _ := writeCursorBackupFixture(t, lane)
			writeCursorStray(t, home, "repo", "agent-transcripts", "scratch-notes-draft", "scratch-notes-draft.jsonl")
			writeCursorStray(t, home, "other", "agent-transcripts", "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", "notes.jsonl")
			prepared, err := prepareCursorFixture(t, home)
			if err != nil {
				var failure *PreparationError
				if errors.As(err, &failure) {
					t.Fatalf("%v: %#v", err, failure.Coverage.Problems)
				}
				t.Fatal(err)
			}
			for _, artifact := range prepared.Manifest.Artifacts {
				if artifact.MemberID != cursorTestID {
					t.Fatalf("stray file entered the bundle: %#v", artifact)
				}
			}
		})
	}
}

func TestCursorStrayJSONLThatMayBelongBlocksFamily(t *testing.T) {
	for name, relative := range map[string][]string{
		"unrecognized file in root directory": {"repo", "agent-transcripts", cursorTestID, "subagents", "helper.jsonl"},
		"root ID in flat layout":              {"repo", "agent-transcripts", cursorTestID + ".jsonl"},
		"root ID in another project":          {"other", cursorTestID + ".jsonl"},
		"unaccepted ID version":               {"repo", "agent-transcripts", "01890a5d-ac96-774b-bcce-b302099a8057", "01890a5d-ac96-774b-bcce-b302099a8057.jsonl"},
	} {
		t.Run(name, func(t *testing.T) {
			home, _, _ := writeCursorBackupFixture(t, "cursor-ide")
			writeCursorStray(t, home, relative...)
			prepared, err := prepareCursorFixture(t, home)
			var failure *PreparationError
			if prepared != nil || !errors.As(err, &failure) || failure.Coverage.Problems[0].Code != sessionbackupv1.ProblemUnattributable {
				t.Fatalf("prepared=%#v error=%v", prepared, err)
			}
		})
	}
}
