package sessionbackupproducer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

const (
	testRootID  = "11111111-2222-3333-4444-555555555555"
	testChildID = "66666666-7777-8888-9999-aaaaaaaaaaaa"
)

type staticSynthesis map[string]synthesis.Record

func (store staticSynthesis) LoadRecord(_ string, id string) (synthesis.Record, error) {
	record, ok := store[id]
	if !ok {
		return synthesis.Record{}, fs.ErrNotExist
	}
	return record, nil
}

func TestPrepareLocalAndSSHFixturesPassIndependentVerifier(t *testing.T) {
	for _, sourceKind := range []string{sessionbackupv1.SourceLocal, sessionbackupv1.SourceSSH} {
		t.Run(sourceKind, func(t *testing.T) {
			home, workspace := writeFamilyFixture(t, 0)
			spool := t.TempDir()
			revision := time.Date(2026, 8, 18, 10, 0, 6, 0, time.UTC).UnixMilli()
			store := staticSynthesis{testRootID: {
				Revision: revision, Model: "fixture-model", GeneratedAt: revision + 1,
				Synthesis: session.SessionSynthesis{
					Goals: []string{"freeze the family"}, Outcome: "complete",
					KeyDecisions: []string{"stream raw inputs"}, NextStep: "upload",
				},
			}}
			manager := New(Options{
				Root: spool, CollectorVersion: "fixture-collector", ParserVersion: "fixture-parser",
				Synthesis: store,
				OpenSource: func(context.Context, Selection) (SourceHandle, error) {
					return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
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
			if prepared.Manifest.Family.RootMemberID != testRootID || len(prepared.Manifest.Members) != 2 {
				t.Fatalf("family = %#v", prepared.Manifest)
			}
			verified, err := sessionbackupv1.VerifyDirectory(filepath.Join(spool, prepared.BundleID))
			if err != nil {
				t.Fatalf("independent verifier: %v", err)
			}
			if verified.CompleteBackupSHA256 != prepared.BundleID || prepared.Coverage.ArtifactCount != len(prepared.Manifest.Artifacts) {
				t.Fatalf("prepared coverage = %#v", prepared)
			}
			counts := map[string]int{}
			for _, artifact := range prepared.Manifest.Artifacts {
				counts[artifact.Kind]++
				if artifact.Kind == sessionbackupv1.KindRawMetadataRows {
					t.Fatal("Codex producer emitted prohibited database rows")
				}
			}
			if counts[sessionbackupv1.KindRawTranscript] != 2 || counts[sessionbackupv1.KindRawSidecar] != 2 ||
				counts[sessionbackupv1.KindParsedSessionRecord] != 2 || counts[sessionbackupv1.KindExactChangeBody] != 4 ||
				counts[sessionbackupv1.KindSessionEnrichment] != 2 {
				t.Fatalf("artifact counts = %#v", counts)
			}
			if sourceKind == sessionbackupv1.SourceLocal && counts[sessionbackupv1.KindSynthesis] != 1 {
				t.Fatalf("local synthesis count = %d", counts[sessionbackupv1.KindSynthesis])
			}
			if sourceKind == sessionbackupv1.SourceSSH && counts[sessionbackupv1.KindSynthesis] != 0 {
				t.Fatalf("SSH synthesis count = %d", counts[sessionbackupv1.KindSynthesis])
			}
			if prepared.Manifest.Repository.Canonical != filepath.Base(workspace) {
				t.Fatalf("repository = %q", prepared.Manifest.Repository.Canonical)
			}
		})
	}
}

func TestPrepareDetectsMutationAndDoesNotPublishManifest(t *testing.T) {
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
	assertProblem(t, err, sessionbackupv1.ProblemUnstable)
	assertNoCompletedBundle(t, spool)
}

func TestPrepareRejectsUnreadableExpectedSource(t *testing.T) {
	home, _ := writeFamilyFixture(t, 0)
	denied := familyFile(home, false, testRootID)
	source := &deniedSource{ReadSource: vendors.LocalReadSource, denied: denied}
	manager := New(Options{
		Root: t.TempDir(),
		OpenSource: func(context.Context, Selection) (SourceHandle, error) {
			return SourceHandle{Source: source, Home: home}, nil
		},
	})
	_, err := manager.Prepare(t.Context(), localSelection())
	assertProblem(t, err, sessionbackupv1.ProblemUnattributable)
}

func TestPrepareRejectsPersistedSynthesisRevisionMismatch(t *testing.T) {
	home, _ := writeFamilyFixture(t, 0)
	manager := New(Options{
		Root: t.TempDir(), Synthesis: staticSynthesis{testRootID: {
			Revision: 1, Model: "fixture-model", GeneratedAt: 2,
			Synthesis: session.SessionSynthesis{Goals: []string{}, KeyDecisions: []string{}},
		}},
		OpenSource: func(context.Context, Selection) (SourceHandle, error) {
			return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
		},
	})
	_, err := manager.Prepare(t.Context(), localSelection())
	assertProblem(t, err, sessionbackupv1.ProblemInvalid)
}

func TestLargeRawArtifactStreamsAndSurvivesRestart(t *testing.T) {
	home, _ := writeFamilyFixture(t, 12<<20)
	tracking := &trackingSource{ReadSource: vendors.LocalReadSource}
	spool := t.TempDir()
	manager := New(Options{
		Root: spool,
		OpenSource: func(context.Context, Selection) (SourceHandle, error) {
			return SourceHandle{Source: tracking, Home: home}, nil
		},
	})
	prepared, err := manager.Prepare(t.Context(), localSelection())
	if err != nil {
		t.Fatal(err)
	}
	if tracking.maxRead.Load() > 128*1024 {
		t.Fatalf("largest source read = %d, want bounded streaming reads", tracking.maxRead.Load())
	}
	if prepared.Coverage.TotalBytes < 12<<20 {
		t.Fatalf("large fixture bundle = %d bytes, want at least raw input size", prepared.Coverage.TotalBytes)
	}
	t.Logf("large fixture bundle: %d bytes; maximum source read: %d bytes", prepared.Coverage.TotalBytes, tracking.maxRead.Load())
	restarted := New(Options{Root: spool})
	opened, err := restarted.Open(prepared.BundleID)
	if err != nil || opened.BundleID != prepared.BundleID {
		t.Fatalf("restart open = %#v, %v", opened, err)
	}
	var rawName string
	for _, artifact := range opened.Manifest.Artifacts {
		if artifact.MemberID == testRootID && artifact.Kind == sessionbackupv1.KindRawTranscript {
			rawName = artifact.LogicalName
			break
		}
	}
	buffer := make([]byte, 32)
	if count, err := restarted.Read(prepared.BundleID, rawName, 0, buffer); err != nil || count == 0 {
		t.Fatalf("retry read = %d, %v", count, err)
	}
	if err := restarted.Discard(prepared.BundleID); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Open(prepared.BundleID); !errors.Is(err, ErrNotPrepared) {
		t.Fatalf("open after discard = %v", err)
	}
}

func TestUnsupportedProducerIsProductVisibleBlocker(t *testing.T) {
	manager := New(Options{Root: t.TempDir()})
	_, err := manager.Prepare(t.Context(), Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: "claude", SessionID: testRootID})
	assertProblem(t, err, sessionbackupv1.ProblemUnsupported)
}

func TestCaptureLogsDoNotContainSourceDerivedContent(t *testing.T) {
	home, _ := writeFamilyFixture(t, 0)
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	manager := New(Options{
		Root: t.TempDir(),
		OpenSource: func(context.Context, Selection) (SourceHandle, error) {
			return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
		},
	})
	if _, err := manager.Prepare(t.Context(), localSelection()); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{testRootID, testChildID, home, "synthetic fixture prompt", "Fixture root"} {
		if strings.Contains(logs.String(), private) {
			t.Fatalf("source-derived content appeared in logs")
		}
	}
}

func TestStartExposesCancellablePreparingState(t *testing.T) {
	opened := make(chan struct{})
	manager := New(Options{
		Root: t.TempDir(),
		OpenSource: func(ctx context.Context, _ Selection) (SourceHandle, error) {
			close(opened)
			<-ctx.Done()
			return SourceHandle{}, ctx.Err()
		},
	})
	id, err := manager.Start(t.Context(), localSelection())
	if err != nil {
		t.Fatal(err)
	}
	<-opened
	if state, ok := manager.Status(id); !ok || state.State != StatePreparing {
		t.Fatalf("preparing state = %#v, %t", state, ok)
	}
	if !manager.Cancel(id) {
		t.Fatal("cancel did not accept preparing operation")
	}
	state, err := manager.Wait(t.Context(), id)
	if err != nil || state.State != StateCancelled || len(state.Coverage.Problems) != 1 || !state.Coverage.Problems[0].Retryable {
		t.Fatalf("cancelled state = %#v, %v", state, err)
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
	// A shared database is deliberately outside the Codex v1 inventory.
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
	paddingRows := ""
	if padding > 0 {
		paddingRows = strings.Repeat("{}\n", padding/3)
	}
	return fmt.Sprintf(`{"timestamp":"2026-08-18T10:00:00.000Z","type":"session_meta","payload":{"id":%q,"session_id":%q,"cwd":%q,"git":{"branch":"main"}%s}}
{"timestamp":"2026-08-18T10:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"synthetic fixture prompt"}}
{"timestamp":"2026-08-18T10:00:02.000Z","type":"turn_context","payload":{"model":"gpt-5"}}
{"timestamp":"2026-08-18T10:00:03.000Z","type":"event_msg","payload":{"type":"task_started"}}
{"timestamp":"2026-08-18T10:00:04.000Z","type":"event_msg","payload":{"type":"patch_apply_end","changes":{"main.go":{"type":"update","unified_diff":"@@\n-old\n+new\n"},"new.go":{"type":"add","content":"package newfile\n"}}}}
{"timestamp":"2026-08-18T10:00:05.000Z","type":"event_msg","payload":{"type":"agent_message","phase":"final_answer","message":"done"}}
{"timestamp":"2026-08-18T10:00:06.000Z","type":"event_msg","payload":{"type":"task_complete"}}
%s`, id, id, workspace, parent, paddingRows)
}

func localSelection() Selection {
	return Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentCodex, SessionID: testRootID}
}

func assertProblem(t *testing.T, err error, code string) {
	t.Helper()
	var preparation *PreparationError
	if !errors.As(err, &preparation) || len(preparation.Coverage.Problems) != 1 || preparation.Coverage.Problems[0].Code != code {
		t.Fatalf("error = %#v, want problem %q", err, code)
	}
}

func assertNoCompletedBundle(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".preparing-") {
			t.Fatalf("unexpected completed bundle %q", entry.Name())
		}
	}
}

type deniedSource struct {
	vendors.ReadSource
	denied string
}

func (source *deniedSource) Open(name string) (io.ReadCloser, error) {
	if name == source.denied {
		return nil, fs.ErrPermission
	}
	return source.ReadSource.Open(name)
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
