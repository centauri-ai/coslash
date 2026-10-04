package sessionbackupproducer

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
	_ "modernc.org/sqlite"
)

const cursorTestID = "01234567-89ab-4def-8123-456789abcdef"

func cursorIDEStateDB(home string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Roaming", "Cursor", "User", "globalStorage", "state.vscdb")
	}
	return filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
}

func writeCursorBackupFixture(t *testing.T, lane string) (string, string, string) {
	t.Helper()
	home, workspace := t.TempDir(), t.TempDir()
	workspaceJSON, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
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
		state := cursorIDEStateDB(home)
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
		header := `{"name":"IDE session","workspaceIdentifier":{"uri":{"fsPath":` + string(workspaceJSON) + `}}}`
		if _, err := db.Exec(`INSERT INTO composerHeaders VALUES (?, ?, ?, ?)`, cursorTestID, header, 1_800_000_000_000, 1_800_000_001_000); err != nil {
			t.Fatal(err)
		}
	} else {
		store := filepath.Join(home, ".cursor", "chats", "workspace", cursorTestID, "store.db")
		if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(store), "meta.json"), []byte(`{"cwd":`+string(workspaceJSON)+`}`), 0o600); err != nil {
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
	state := cursorIDEStateDB(home)
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
	state := cursorIDEStateDB(home)
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

// Real Cursor CLI chat stores hold content blobs of several MiB, and IDE
// bubbles can be as large. Those rows belong to the session, and the rows
// contract carries a value up to its document bound, so they back up exactly.
func TestCursorLargeRowValuesPrepare(t *testing.T) {
	for name, tc := range map[string]struct {
		lane   string
		insert func(t *testing.T, home string)
	}{
		"cli blob": {
			lane: "cursor-cli",
			insert: func(t *testing.T, home string) {
				execCursorFixtureDB(t, filepath.Join(home, ".cursor", "chats", "workspace", cursorTestID, "store.db"),
					`CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB)`,
					`INSERT INTO blobs VALUES ('large', ?)`, make([]byte, 3<<20))
			},
		},
		"ide bubble text": {
			lane: "cursor-ide",
			insert: func(t *testing.T, home string) {
				execCursorFixtureDB(t, cursorIDEStateDB(home),
					"", `INSERT INTO cursorDiskKV VALUES (?, ?)`, "bubbleId:"+cursorTestID+":large", strings.Repeat("x", 3<<20))
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			home, _, _ := writeCursorBackupFixture(t, tc.lane)
			tc.insert(t, home)
			spool := t.TempDir()
			manager := New(Options{Root: spool, OpenSource: func(context.Context, Selection) (SourceHandle, error) {
				return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
			}})
			prepared, err := manager.Prepare(t.Context(), Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentCursor, SessionID: cursorTestID})
			if err != nil {
				t.Fatalf("large value must prepare: %v", err)
			}
			if _, err := sessionbackupv1.VerifyDirectory(filepath.Join(spool, prepared.BundleID)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Attributed rows the contract still cannot carry, such as text that is not
// UTF-8, fail the same way every time. The blocker says the rows are out of
// bounds for the contract, not that they could not be attributed.
func TestCursorUnrepresentableRowIsInvalidNotUnattributable(t *testing.T) {
	home, _, _ := writeCursorBackupFixture(t, "cursor-ide")
	execCursorFixtureDB(t, cursorIDEStateDB(home),
		"", `INSERT INTO cursorDiskKV VALUES (?, CAST(X'FF' AS TEXT))`, "bubbleId:"+cursorTestID+":binary")
	spool := t.TempDir()
	manager := New(Options{Root: spool, OpenSource: func(context.Context, Selection) (SourceHandle, error) {
		return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}})
	prepared, err := manager.Prepare(t.Context(), Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentCursor, SessionID: cursorTestID})
	var failure *PreparationError
	if prepared != nil || !errors.As(err, &failure) || len(failure.Coverage.Problems) != 1 {
		t.Fatalf("prepared=%#v error=%v", prepared, err)
	}
	want := sessionbackupv1.CaptureProblem{Code: sessionbackupv1.ProblemInvalid, MemberID: cursorTestID, Kind: sessionbackupv1.KindRawMetadataRows}
	if got := failure.Coverage.Problems[0]; got != want {
		t.Fatalf("problem = %#v, want %#v", got, want)
	}
	if entries, err := os.ReadDir(spool); err != nil || len(entries) != 0 {
		t.Fatalf("failed capture published files: %v, %v", entries, err)
	}
}

// A chat with no working folder, such as an IDE chat in an empty window or a
// CLI chat whose folder cannot be recovered, backs up with no repository.
func TestCursorChatWithoutFolderBacksUpWithoutRepository(t *testing.T) {
	for _, lane := range []string{"cursor-ide", "cursor-cli"} {
		t.Run(lane, func(t *testing.T) {
			home, _, _ := writeCursorBackupFixture(t, lane)
			if lane == "cursor-ide" {
				execCursorFixtureDB(t, cursorIDEStateDB(home),
					"", `UPDATE composerHeaders SET value = '{"name":"IDE session"}'`)
			} else if err := os.WriteFile(filepath.Join(home, ".cursor", "chats", "workspace", cursorTestID, "meta.json"), []byte(`{"schemaVersion":1}`), 0o600); err != nil {
				t.Fatal(err)
			}
			spool := t.TempDir()
			manager := New(Options{Root: spool, OpenSource: func(context.Context, Selection) (SourceHandle, error) {
				return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
			}})
			prepared, err := manager.Prepare(t.Context(), Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentCursor, SessionID: cursorTestID})
			if err != nil {
				t.Fatalf("chat without a folder must prepare: %v", err)
			}
			verified, err := sessionbackupv1.VerifyDirectory(filepath.Join(spool, prepared.BundleID))
			if err != nil {
				t.Fatal(err)
			}
			if verified.Repository != (sessionbackupv1.RepositoryIdentity{VCS: sessionbackupv1.RepositoryVCSNone}) {
				t.Fatalf("repository = %#v", verified.Repository)
			}
			for _, artifact := range verified.Artifacts {
				if artifact.Kind != sessionbackupv1.KindSessionEnrichment {
					continue
				}
				data, err := os.ReadFile(filepath.Join(spool, prepared.BundleID, filepath.FromSlash(artifact.LogicalName)))
				if err != nil {
					t.Fatal(err)
				}
				enrichment, err := sessionbackupv1.DecodeEnrichment(data)
				if err != nil || enrichment.Repository != nil || !enrichment.RepositoryLocalOnly || enrichment.FilesystemFallbackBranch != nil {
					t.Fatalf("enrichment = %#v, %v", enrichment, err)
				}
			}
		})
	}
}

// Resuming a CLI chat from another folder can leave an empty stub store for
// the same chat. The stub holds nothing, so the backup uses the real store.
func TestCursorCLIResumeStubIsLeftOut(t *testing.T) {
	home, workspace, _ := writeCursorBackupFixture(t, "cursor-cli")
	stub := filepath.Join(home, ".cursor", "chats", "resumed-elsewhere", cursorTestID, "store.db")
	if err := os.MkdirAll(filepath.Dir(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(stub), "meta.json"), []byte(`{"schemaVersion":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	execCursorFixtureDB(t, stub, `CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT); CREATE TABLE messages (ordinal INTEGER, text TEXT)`,
		`INSERT INTO meta VALUES ('0', ?)`, hex.EncodeToString([]byte(`{"agentId":"`+cursorTestID+`","name":"CLI session"}`)))
	spool := t.TempDir()
	manager := New(Options{Root: spool, OpenSource: func(context.Context, Selection) (SourceHandle, error) {
		return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}})
	prepared, err := manager.Prepare(t.Context(), Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentCursor, SessionID: cursorTestID})
	if err != nil {
		t.Fatalf("resumed chat must prepare: %v", err)
	}
	verified, err := sessionbackupv1.VerifyDirectory(filepath.Join(spool, prepared.BundleID))
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range verified.Artifacts {
		if artifact.Kind != sessionbackupv1.KindRawSidecar {
			continue
		}
		data, err := os.ReadFile(filepath.Join(spool, prepared.BundleID, filepath.FromSlash(artifact.LogicalName)))
		if err != nil || !strings.Contains(string(data), workspace) {
			t.Fatalf("sidecar is not the real store's: %v", err)
		}
	}

	// A second store that also holds rows is ambiguous and still blocks.
	execCursorFixtureDB(t, stub, "", `INSERT INTO messages VALUES (1, 'other')`)
	if _, err := manager.Prepare(t.Context(), Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentCursor, SessionID: cursorTestID}); err == nil {
		t.Fatal("two stores with rows prepared")
	}
}

func execCursorFixtureDB(t *testing.T, path, schema, insert string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if schema != "" {
		if _, err := db.Exec(schema); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(insert, args...); err != nil {
		t.Fatal(err)
	}
}
