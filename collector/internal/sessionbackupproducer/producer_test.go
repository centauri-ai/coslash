package sessionbackupproducer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

const (
	testRootID  = "11111111-2222-3333-4444-555555555555"
	testChildID = "66666666-7777-8888-9999-aaaaaaaaaaaa"
)

func TestPrepareProducesVerifiedBoundedLocalAndSSHBundle(t *testing.T) {
	for _, sourceKind := range []string{sessionbackupv1.SourceLocal, sessionbackupv1.SourceSSH} {
		t.Run(sourceKind, func(t *testing.T) {
			home, workspace := writeFamilyFixture(t, 1<<20)
			tracking := &trackingSource{ReadSource: vendors.LocalReadSource}
			spool := t.TempDir()
			manager := New(Options{
				Root: spool, CollectorVersion: "fixture-collector", ParserVersion: "fixture-parser",
				OpenSource: func(context.Context, Selection) (SourceHandle, error) {
					return SourceHandle{Source: tracking, Home: home}, nil
				},
			})
			sourceID := "local"
			if sourceKind == sessionbackupv1.SourceSSH {
				sourceID = "remote-fixture"
			}
			prepared, err := manager.Prepare(t.Context(), Selection{
				SourceKind: sourceKind, SourceID: sourceID, Agent: vendors.AgentCodex, SessionID: testRootID,
			})
			if err != nil {
				t.Fatal(err)
			}
			verified, err := sessionbackupv1.VerifyDirectory(filepath.Join(spool, prepared.BundleID))
			if err != nil || verified.CompleteBackupSHA256 != prepared.BundleID {
				t.Fatalf("independent verification = %#v, %v", verified, err)
			}
			if prepared.Manifest.Family.RootMemberID != testRootID || len(prepared.Manifest.Members) != 2 ||
				prepared.Manifest.Repository.Canonical != filepath.Base(workspace) {
				t.Fatalf("prepared manifest = %#v", prepared.Manifest)
			}
			counts := map[string]int{}
			for _, artifact := range prepared.Manifest.Artifacts {
				counts[artifact.Kind]++
			}
			if counts[sessionbackupv1.KindRawMetadataRows] != 0 || counts[sessionbackupv1.KindRawTranscript] != 2 ||
				counts[sessionbackupv1.KindParsedSessionRecord] != 2 || counts[sessionbackupv1.KindExactChangeBody] != 4 {
				t.Fatalf("artifact counts = %#v", counts)
			}
			if tracking.maxRead.Load() > 128*1024 {
				t.Fatalf("largest source read = %d", tracking.maxRead.Load())
			}
			if reopened, err := New(Options{Root: spool}).Open(prepared.BundleID); err != nil || reopened.BundleID != prepared.BundleID {
				t.Fatalf("restart open = %#v, %v", reopened, err)
			}
		})
	}
}

func TestPrepareRejectsMutatedSourceWithoutPublishing(t *testing.T) {
	home, _ := writeFamilyFixture(t, 0)
	rootFile := familyFile(home, false, testRootID)
	spool := t.TempDir()
	manager := New(Options{
		Root: spool,
		OpenSource: func(context.Context, Selection) (SourceHandle, error) {
			return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
		},
		AfterRawCopy: func() {
			file, err := os.OpenFile(rootFile, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = file.WriteString(" \n")
			_ = file.Close()
		},
	})
	_, err := manager.Prepare(t.Context(), localSelection())
	var preparation *PreparationError
	if !errors.As(err, &preparation) || len(preparation.Coverage.Problems) != 1 ||
		preparation.Coverage.Problems[0].Code != sessionbackupv1.ProblemUnstable {
		t.Fatalf("error = %#v", err)
	}
	entries, err := os.ReadDir(spool)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".preparing-") {
			t.Fatalf("unexpected completed bundle %q", entry.Name())
		}
	}
}

func writeFamilyFixture(t *testing.T, padding int) (string, string) {
	t.Helper()
	home := t.TempDir()
	workspace := filepath.Join(home, "fixture-repository")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	writeRollout(t, familyFile(home, false, testRootID), completeRollout(testRootID, "", workspace, padding))
	writeRollout(t, familyFile(home, true, testChildID), completeRollout(testChildID, testRootID, workspace, 0))
	indexPath := codex.SessionIndexPath(home)
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o700); err != nil {
		t.Fatal(err)
	}
	index := fmt.Sprintf("{\"id\":%q,\"thread_name\":\"Fixture root\"}\n{\"id\":%q,\"thread_name\":\"Fixture child\"}\n", testRootID, testChildID)
	if err := os.WriteFile(indexPath, []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "state.sqlite"), []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, workspace
}

func familyFile(home string, archived bool, id string) string {
	directory := filepath.Join(home, ".codex", "sessions", "2026", "08", "18")
	if archived {
		directory = filepath.Join(home, ".codex", "archived_sessions")
	}
	return filepath.Join(directory, "rollout-2026-08-18T10-00-00-"+id+".jsonl")
}

func writeRollout(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func completeRollout(id, parentID, workspace string, padding int) string {
	parent := ""
	if parentID != "" {
		parent = fmt.Sprintf(",\"parent_thread_id\":%q", parentID)
	}
	return fmt.Sprintf(`{"timestamp":"2026-08-18T10:00:00.000Z","type":"session_meta","payload":{"id":%q,"session_id":%q,"cwd":%q,"git":{"branch":"main"}%s}}
{"timestamp":"2026-08-18T10:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"synthetic fixture prompt"}}
{"timestamp":"2026-08-18T10:00:02.000Z","type":"turn_context","payload":{"model":"gpt-5"}}
{"timestamp":"2026-08-18T10:00:03.000Z","type":"event_msg","payload":{"type":"task_started"}}
{"timestamp":"2026-08-18T10:00:04.000Z","type":"event_msg","payload":{"type":"patch_apply_end","changes":{"main.go":{"type":"update","unified_diff":"@@\n-old\n+new\n"},"new.go":{"type":"add","content":"package newfile\n"}}}}
{"timestamp":"2026-08-18T10:00:05.000Z","type":"event_msg","payload":{"type":"agent_message","phase":"final_answer","message":"done"}}
{"timestamp":"2026-08-18T10:00:06.000Z","type":"event_msg","payload":{"type":"task_complete"}}
%s`, id, id, workspace, parent, strings.Repeat("{}\n", padding/3))
}

func localSelection() Selection {
	return Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentCodex, SessionID: testRootID}
}

type trackingSource struct {
	vendors.ReadSource
	maxRead atomic.Int64
}

func (source *trackingSource) Open(name string) (io.ReadCloser, error) {
	reader, err := source.ReadSource.Open(name)
	if err != nil {
		return nil, err
	}
	return &trackingReader{ReadCloser: reader, maximum: &source.maxRead}, nil
}

type trackingReader struct {
	io.ReadCloser
	maximum *atomic.Int64
}

func (reader *trackingReader) Read(destination []byte) (int, error) {
	for current := reader.maximum.Load(); int64(len(destination)) > current; current = reader.maximum.Load() {
		if reader.maximum.CompareAndSwap(current, int64(len(destination))) {
			break
		}
	}
	return reader.ReadCloser.Read(destination)
}
