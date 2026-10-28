package cursor

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func assertSkills(t *testing.T, parsed *vendors.ParsedSession, want []session.Skill) {
	t.Helper()
	if !reflect.DeepEqual(parsed.Session.Skills, want) {
		t.Fatalf("skills = %#v, want %#v", parsed.Session.Skills, want)
	}
}

func skillBlob(kind, id, name string, value any) string {
	block := map[string]any{"type": kind, "toolCallId": id, "toolName": name}
	if kind == "tool-call" {
		block["args"] = value
	} else {
		block["result"] = value
	}
	return mustJSON(map[string]any{"content": []any{block}})
}

func TestSkillsLiveAndFrozen(t *testing.T) {
	for _, lane := range []string{entrypointCLI, entrypointIDE} {
		t.Run(lane, func(t *testing.T) {
			home := outputsTestHome(t)
			body := "# Instructions\n\n  Keep formatting.\n"
			var blobs []string
			var db *sql.DB
			path := filepath.Join(home, ".cursor", "chats", "workspace", outputsTestID, "store.db")
			label, table := "cli-store", "blobs"
			if lane == entrypointIDE {
				path = filepath.Join(GlobalStorage(home), "state.vscdb")
				label, table = "ide-state", "cursorDiskKV"
				if err := createMetadataTestDB(path); err != nil {
					t.Fatal(err)
				}
				var err error
				db, err = sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
			}
			for i, item := range []struct {
				name, path, body string
				completed        bool
			}{
				{"Read", "/skills/review/SKILL.md", body, true},
				{"ReadFile", "/other/review/SKILL.md", body, true},
				{"Read", "/skills/review/SKILL.md", "new body\n", true},
				{"Read", "/skills/unused/SKILL.md", "", false},
				{"Write", "/skills/written/SKILL.md", "written", true},
				{"Read", "/skills/review/README.md", "mention SKILL.md", true},
				{"List", "/skills/listed/SKILL.md", "listed", true},
				{"Read", "/skills/empty/SKILL.md", "", true},
			} {
				id := fmt.Sprint(i)
				if lane == entrypointCLI {
					blobs = append(blobs, skillBlob("tool-call", id, item.name, map[string]string{"path": item.path}))
					if item.completed {
						blobs = append(blobs, skillBlob("tool-result", id, item.name, item.body))
					}
				} else {
					name := "write_file"
					if item.name == "Read" {
						name = "read_file_v2"
					} else if item.name == "ReadFile" {
						name = "read_file"
					}
					status := "completed"
					if !item.completed {
						status = "loading"
					}
					value := mustJSON(map[string]any{"createdAt": fmt.Sprintf("2026-10-27T10:00:%02dZ", i), "toolFormerData": map[string]any{"name": name, "status": status, "params": mustJSON(map[string]string{"targetFile": item.path}), "result": item.body}})
					if _, err := db.Exec(`INSERT INTO cursorDiskKV(key,value) VALUES (?,?)`, fmt.Sprintf("bubbleId:%s:%02d", outputsTestID, 9-i), value); err != nil {
						t.Fatal(err)
					}
				}
			}
			if lane == entrypointCLI {
				blobs = append(blobs, skillBlob("tool-call", "failed", "Read", map[string]string{"path": "/skills/failed/SKILL.md"}),
					mustJSON(map[string]any{"content": []any{map[string]any{"type": "tool-result", "toolCallId": "failed", "toolName": "Read", "isError": true, "result": "denied"}}}))
			}
			if db != nil {
				if _, err := db.Exec(`INSERT INTO composerHeaders(composerId,value) VALUES (?, '{}')`, outputsTestID); err != nil {
					t.Fatal(err)
				}
				db.Close()
			} else {
				writeCLIStore(t, home, outputsTestID, blobs...)
				store, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.Exec(`CREATE TABLE meta (key TEXT,value TEXT)`); err != nil {
					t.Fatal(err)
				}
				if _, err := store.Exec(`INSERT INTO meta(key,value) VALUES ('0',?)`, fmt.Sprintf("%x", []byte(mustJSON(map[string]string{"agentId": outputsTestID})))); err != nil {
					t.Fatal(err)
				}
				store.Close()
			}
			metadata := vendors.EmptySessionMetadata()
			metadata.Session(outputsTestID).Entrypoint = lane
			parsed := parseShellTranscript(t, outputsTestID)
			parentID := "00000000-0000-4000-8000-0000000000b1"
			childPath := filepath.Join(filepath.Dir(filepath.Dir(parsed.LogPath)), parentID, "subagents", outputsTestID+".jsonl")
			if err := os.MkdirAll(filepath.Dir(childPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(parsed.LogPath, childPath); err != nil {
				t.Fatal(err)
			}
			parsed, err := parseTranscript(childPath)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.ParentID != parentID {
				t.Fatalf("parent = %q", parsed.ParentID)
			}
			want := []session.Skill{{Name: "review", Content: body}, {Name: "review", Content: "new body\n"}, {Name: "empty", Content: ""}}
			if err := applyCommandOutputs(context.Background(), home, []*vendors.ParsedSession{parsed}, metadata); err != nil {
				t.Fatal(err)
			}
			assertSkills(t, parsed, want)
			live, _, err := ParseFilesContext(context.Background(), []string{parsed.LogPath}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(live) != 1 {
				t.Fatalf("live sessions = %d", len(live))
			}
			assertSkills(t, live[0], want)
			frozen, _, err := projectDatabase(context.Background(), path, label, outputsTestID, []tableFilter{{table: table}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			rows := map[string][]BackupRows{outputsTestID: {{SourceKey: label, Bytes: frozen}}}
			backup, err := ParseBackupFilesContext(context.Background(), vendors.LocalReadSource, map[string][]string{outputsTestID: {parsed.LogPath}}, metadata, rows)
			if err != nil {
				t.Fatal(err)
			}
			assertSkills(t, backup[0], want)
			parsed = parseShellTranscript(t, outputsTestID)
			ambiguous := vendors.EmptySessionMetadata()
			ambiguous.Session(outputsTestID).Entrypoint = ""
			if err := applyBackupCommandOutputs([]*vendors.ParsedSession{parsed}, ambiguous, rows); err != nil {
				t.Fatal(err)
			}
			assertSkills(t, parsed, nil)
		})
	}
}

func TestLegacyCLIProjectionDoesNotInventSkillOrder(t *testing.T) {
	blob := mustJSON(map[string]any{"content": []any{
		map[string]any{"type": "tool-call", "toolCallId": "read", "toolName": "Read", "args": map[string]string{"path": "/skills/review/SKILL.md"}},
		map[string]any{"type": "tool-result", "toolCallId": "read", "toolName": "Read", "result": "captured"},
	}})
	frozen, err := sessionbackupv1.FreezeDatabaseRows(sessionbackupv1.DatabaseRows{
		Database: "cursor-cli-store", Tables: []sessionbackupv1.DatabaseTable{{Name: "blobs", Columns: []string{"data"}, Rows: []sessionbackupv1.DatabaseRow{{MemberID: outputsTestID, Values: []sessionbackupv1.DatabaseValue{{Type: "text", Value: blob}}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed := parseShellTranscript(t, outputsTestID)
	metadata := vendors.EmptySessionMetadata()
	metadata.Session(outputsTestID).Entrypoint = entrypointCLI
	if err := applyBackupCommandOutputs([]*vendors.ParsedSession{parsed}, metadata, map[string][]BackupRows{outputsTestID: {{SourceKey: "cli-store", Bytes: frozen}}}); err != nil {
		t.Fatal(err)
	}
	assertSkills(t, parsed, nil)
}
