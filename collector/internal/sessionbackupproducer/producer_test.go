package sessionbackupproducer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/remote"
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

func TestPrepareProducesVerifiedBoundedLocalAndSSHBundle(t *testing.T) {
	for _, sourceKind := range []string{sessionbackupv1.SourceLocal, sessionbackupv1.SourceSSH} {
		t.Run(sourceKind, func(t *testing.T) {
			home, workspace := writeFamilyFixture(t, 1<<20)
			tracking := &trackingSource{ReadSource: vendors.LocalReadSource}
			spool := t.TempDir()
			repository := "github.com/fixture/project"
			manager := New(Options{
				Root: spool, CollectorVersion: "fixture-collector", ParserVersion: "fixture-parser",
				OpenSource: func(context.Context, Selection) (SourceHandle, error) {
					handle := SourceHandle{Source: tracking, Home: home}
					if sourceKind == sessionbackupv1.SourceSSH {
						handle.Enrichment = map[string]remote.BackupSessionEnrichment{
							testRootID: {Repository: &repository}, testChildID: {Repository: &repository},
						}
					}
					return handle, nil
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
			wantRepository := filepath.Base(workspace)
			if sourceKind == sessionbackupv1.SourceSSH {
				wantRepository = repository
			}
			if prepared.Manifest.Family.RootMemberID != testRootID || len(prepared.Manifest.Members) != 2 ||
				prepared.Manifest.Repository.Canonical != wantRepository {
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
			if sourceKind == sessionbackupv1.SourceSSH {
				var rawBytes int64
				for _, file := range []string{familyFile(home, false, testRootID), familyFile(home, true, testChildID)} {
					info, err := os.Stat(file)
					if err != nil {
						t.Fatal(err)
					}
					rawBytes += info.Size()
				}
				if tracking.totalRead.Load() >= 2*rawBytes {
					t.Fatalf("SSH source read %d bytes for %d raw bytes", tracking.totalRead.Load(), rawBytes)
				}
			}
			for _, artifact := range prepared.Manifest.Artifacts {
				if sourceKind != sessionbackupv1.SourceSSH || artifact.MemberID != testRootID || artifact.Kind != sessionbackupv1.KindSessionEnrichment {
					continue
				}
				data, err := os.ReadFile(filepath.Join(spool, prepared.BundleID, filepath.FromSlash(artifact.LogicalName)))
				if err != nil {
					t.Fatal(err)
				}
				enrichment, err := sessionbackupv1.DecodeEnrichment(data)
				if err != nil || enrichment.Repository == nil || *enrichment.Repository != repository || enrichment.RepositoryLocalOnly {
					t.Fatalf("SSH enrichment = %#v, %v", enrichment, err)
				}
			}
			if reopened, err := New(Options{Root: spool}).Open(prepared.BundleID); err != nil || reopened.BundleID != prepared.BundleID {
				t.Fatalf("restart open = %#v, %v", reopened, err)
			}
		})
	}
}

func TestStartOutlivesRequestAndExplicitCancelStopsPreparation(t *testing.T) {
	home, _ := writeFamilyFixture(t, 0)
	reached := make(chan struct{})
	release := make(chan struct{})
	manager := New(Options{
		Root: t.TempDir(),
		OpenSource: func(context.Context, Selection) (SourceHandle, error) {
			return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
		},
		AfterRawCopy: func() { close(reached); <-release },
	})
	requestContext, cancelRequest := context.WithCancel(t.Context())
	id, err := manager.Start(requestContext, localSelection())
	if err != nil {
		t.Fatal(err)
	}
	<-reached
	cancelRequest()
	close(release)
	prepared, err := manager.Wait(t.Context(), id)
	if err != nil || prepared.State != StateReady {
		t.Fatalf("request cancellation result = %#v, %v", prepared, err)
	}

	started := make(chan struct{})
	blocked := make(chan struct{})
	manager = New(Options{
		Root: t.TempDir(),
		OpenSource: func(ctx context.Context, _ Selection) (SourceHandle, error) {
			close(started)
			<-ctx.Done()
			close(blocked)
			return SourceHandle{}, ctx.Err()
		},
	})
	id, err = manager.Start(t.Context(), localSelection())
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if !manager.Cancel(id) {
		t.Fatalf("cancel start = %q", id)
	}
	<-blocked
	cancelled, err := manager.Wait(t.Context(), id)
	if err != nil || cancelled.State != StateCancelled {
		t.Fatalf("explicit cancellation result = %#v, %v", cancelled, err)
	}
}

func TestTerminalOperationsExpireAndWaitConsumes(t *testing.T) {
	now := time.Unix(100, 0)
	manager := New(Options{Root: t.TempDir(), Now: func() time.Time { return now }})
	ready := &operation{state: Preparation{ID: "ready", State: StateReady}, done: make(chan struct{}), completedAt: now}
	close(ready.done)
	manager.operations["ready"] = ready
	if _, err := manager.Wait(t.Context(), "ready"); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.Status("ready"); ok {
		t.Fatal("Wait retained a consumed operation")
	}
	manager.operations["expired"] = &operation{state: Preparation{ID: "expired", State: StateFailed}, completedAt: now}
	now = now.Add(operationRetention)
	if _, ok := manager.Status("expired"); ok {
		t.Fatal("expired terminal operation was retained")
	}
}

func TestPrepareRejectsSkippedInventory(t *testing.T) {
	home, _ := writeFamilyFixture(t, 0)
	denied := filepath.Join(codex.SessionsRoot(home), "unreadable")
	if err := os.MkdirAll(denied, 0o700); err != nil {
		t.Fatal(err)
	}
	manager := New(Options{Root: t.TempDir(), OpenSource: func(context.Context, Selection) (SourceHandle, error) {
		return SourceHandle{Source: skippedDirectorySource{ReadSource: vendors.LocalReadSource, denied: denied}, Home: home}, nil
	}})
	_, err := manager.Prepare(t.Context(), localSelection())
	var preparation *PreparationError
	if !errors.As(err, &preparation) || preparation.Coverage.Problems[0].Code != sessionbackupv1.ProblemUnattributable {
		t.Fatalf("skipped inventory error = %#v", err)
	}
}

func TestSynthesisIsRevisionBoundWithoutChangingPortableRecord(t *testing.T) {
	home, _ := writeFamilyFixture(t, 0)
	prepare := func(store SynthesisStore) (*Prepared, string) {
		spool := t.TempDir()
		manager := New(Options{Root: spool, Synthesis: store, OpenSource: func(context.Context, Selection) (SourceHandle, error) {
			return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
		}})
		prepared, err := manager.Prepare(t.Context(), localSelection())
		if err != nil {
			t.Fatal(err)
		}
		return prepared, spool
	}
	baseline, baselineSpool := prepare(nil)
	baselineRecord := readRootRecord(t, baselineSpool, baseline)
	stale := synthesisStore{records: map[string]synthesis.Record{
		testRootID: {Revision: baselineRecord.Session.LastActivityAtMs - 1, Model: "stale"},
	}}
	stalePrepared, _ := prepare(stale)
	for _, artifact := range stalePrepared.Manifest.Artifacts {
		if artifact.Kind == sessionbackupv1.KindSynthesis {
			t.Fatal("stale synthesis was included")
		}
	}
	exact := synthesisStore{records: map[string]synthesis.Record{
		testRootID: {
			Revision: baselineRecord.Session.LastActivityAtMs, Model: "fixture", GeneratedAt: 1,
			Synthesis: session.SessionSynthesis{Goals: []string{"goal"}, Outcome: "done", KeyDecisions: []string{}, NextStep: "ship"},
		},
	}}
	exactPrepared, exactSpool := prepare(exact)
	exactRecord := readRootRecord(t, exactSpool, exactPrepared)
	if exactRecord.RevisionID != baselineRecord.RevisionID || exactRecord.Session.Synthesis != nil {
		t.Fatalf("portable record changed for synthesis: baseline=%q exact=%q synthesis=%#v", baselineRecord.RevisionID, exactRecord.RevisionID, exactRecord.Session.Synthesis)
	}
	found := false
	for _, artifact := range exactPrepared.Manifest.Artifacts {
		found = found || artifact.Kind == sessionbackupv1.KindSynthesis
	}
	if !found {
		t.Fatal("exact synthesis artifact missing")
	}
}

func TestKnownRawSizesAreBoundedBeforeCopy(t *testing.T) {
	if withinKnownBounds([]vendors.FileFingerprint{{Size: sessionbackupv1.MaxArtifactBytes + 1}}) {
		t.Fatal("oversized artifact accepted")
	}
	aggregate := make([]vendors.FileFingerprint, sessionbackupv1.MaxTotalBytes/sessionbackupv1.MaxArtifactBytes+1)
	for index := range aggregate {
		aggregate[index].Size = sessionbackupv1.MaxArtifactBytes
	}
	if withinKnownBounds(aggregate) {
		t.Fatal("oversized aggregate accepted")
	}
	if !withinKnownBounds([]vendors.FileFingerprint{{Size: sessionbackupv1.MaxArtifactBytes}}) {
		t.Fatal("artifact boundary rejected")
	}
}

func TestBoundedWriterStopsBeforePersistingOverflow(t *testing.T) {
	var output bytes.Buffer
	writer := &boundedWriter{writer: &output, remaining: 3}
	_, err := io.Copy(writer, strings.NewReader("four"))
	if !errors.Is(err, sessionbackupv1.ErrInvalid) || output.String() != "fou" {
		t.Fatalf("bounded copy = %q, %v", output.String(), err)
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
	maxRead   atomic.Int64
	totalRead atomic.Int64
}

func (source *trackingSource) Open(name string) (io.ReadCloser, error) {
	reader, err := source.ReadSource.Open(name)
	if err != nil {
		return nil, err
	}
	return &trackingReader{ReadCloser: reader, maximum: &source.maxRead, total: &source.totalRead}, nil
}

type trackingReader struct {
	io.ReadCloser
	maximum *atomic.Int64
	total   *atomic.Int64
}

func (reader *trackingReader) Read(destination []byte) (int, error) {
	for current := reader.maximum.Load(); int64(len(destination)) > current; current = reader.maximum.Load() {
		if reader.maximum.CompareAndSwap(current, int64(len(destination))) {
			break
		}
	}
	read, err := reader.ReadCloser.Read(destination)
	reader.total.Add(int64(read))
	return read, err
}

type skippedDirectorySource struct {
	vendors.ReadSource
	denied string
}

func (source skippedDirectorySource) ReadDir(path string) ([]fs.DirEntry, error) {
	if path == source.denied {
		return nil, fs.ErrPermission
	}
	return source.ReadSource.ReadDir(path)
}

type synthesisStore struct {
	records map[string]synthesis.Record
}

func (store synthesisStore) LoadRecord(_ string, id string) (synthesis.Record, error) {
	record, ok := store.records[id]
	if !ok {
		return synthesis.Record{}, fs.ErrNotExist
	}
	return record, nil
}

func readRootRecord(t *testing.T, spool string, prepared *Prepared) fullsessionv1.Record {
	t.Helper()
	for _, artifact := range prepared.Manifest.Artifacts {
		if artifact.MemberID != testRootID || artifact.Kind != sessionbackupv1.KindParsedSessionRecord {
			continue
		}
		data, err := os.ReadFile(filepath.Join(spool, prepared.BundleID, filepath.FromSlash(artifact.LogicalName)))
		if err != nil {
			t.Fatal(err)
		}
		record, err := fullsessionv1.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	t.Fatal("root record missing")
	return fullsessionv1.Record{}
}
