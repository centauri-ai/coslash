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
	"slices"
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
	testRootID     = "11111111-2222-3333-4444-555555555555"
	testChildID    = "66666666-7777-8888-9999-aaaaaaaaaaaa"
	testGuardianID = "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
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

func TestUnsupportedAgentsNeverOpenSourceOrPublishBundle(t *testing.T) {
	for _, unsupported := range []struct{ agent, sourceKind string }{
		{vendors.AgentClaude, sessionbackupv1.SourceSSH},
		{vendors.AgentOpenCode, sessionbackupv1.SourceSSH},
		{vendors.AgentCursor, sessionbackupv1.SourceSSH},
	} {
		agent, sourceKind := unsupported.agent, unsupported.sourceKind
		t.Run(agent+"/"+sourceKind, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "spool")
			opened := false
			manager := New(Options{Root: root, OpenSource: func(context.Context, Selection) (SourceHandle, error) {
				opened = true
				return SourceHandle{}, nil
			}})
			prepared, err := manager.Prepare(t.Context(), Selection{
				SourceKind: sourceKind, SourceID: "fixture-source", Agent: agent, SessionID: "fixture-session",
			})
			var failure *PreparationError
			if prepared != nil || !errors.As(err, &failure) || len(failure.Coverage.Problems) != 1 ||
				failure.Coverage.Problems[0].Code != sessionbackupv1.ProblemUnsupported || opened {
				t.Fatalf("prepared=%#v failure=%#v opened=%t", prepared, failure, opened)
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unsupported source created a spool: %v", err)
			}
		})
	}
}

func TestPreparePreservesRepeatedRootIndexRowsInSSHFamilyBundle(t *testing.T) {
	home, _ := writeFamilyFixture(t, 0)
	rootRow := []byte(fmt.Sprintf("{\"id\":%q,\"thread_name\":\"Fixture root\"}\r\n", testRootID))
	childRow := []byte(fmt.Sprintf("{\"id\":%q,\"thread_name\":\"Fixture child\"}\n", testChildID))
	indexContent := append(append(append([]byte(nil), rootRow...), rootRow...), childRow...)
	if err := os.WriteFile(codex.SessionIndexPath(home), indexContent, 0o600); err != nil {
		t.Fatal(err)
	}

	spool := t.TempDir()
	manager := New(Options{
		Root: spool,
		OpenSource: func(context.Context, Selection) (SourceHandle, error) {
			return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
		},
	})
	prepared, err := manager.Prepare(t.Context(), Selection{
		SourceKind: sessionbackupv1.SourceSSH, SourceID: "remote-fixture",
		Agent: vendors.AgentCodex, SessionID: testRootID,
	})
	if err != nil {
		t.Fatalf("prepare repeated attributed rows: %v", err)
	}
	if len(prepared.Manifest.Members) != 2 {
		t.Fatalf("family members = %d, want root and child", len(prepared.Manifest.Members))
	}

	var rootSidecar string
	var sidecarCount int
	for _, artifact := range prepared.Manifest.Artifacts {
		if artifact.Kind != sessionbackupv1.KindRawSidecar {
			continue
		}
		sidecarCount++
		if artifact.MemberID == testRootID {
			rootSidecar = artifact.LogicalName
		}
	}
	if sidecarCount != 2 || rootSidecar == "" {
		t.Fatalf("raw sidecars = %d, root sidecar = %q", sidecarCount, rootSidecar)
	}
	wantRootSidecar := append(append([]byte(nil), rootRow...), rootRow...)
	gotRootSidecar := make([]byte, len(wantRootSidecar)+1)
	n, err := manager.Read(prepared.BundleID, rootSidecar, 0, gotRootSidecar)
	if err != nil || !bytes.Equal(gotRootSidecar[:n], wantRootSidecar) {
		t.Fatalf("root sidecar bytes = %q, %v; want both original rows", gotRootSidecar[:n], err)
	}
	if _, err := sessionbackupv1.VerifyDirectory(filepath.Join(spool, prepared.BundleID)); err != nil {
		t.Fatalf("repeated-row family bundle failed verification: %v", err)
	}
}

func TestMetadataFromRepeatedIndexRowsUsesLastThreadName(t *testing.T) {
	rows := map[string][][]byte{
		"member": {
			[]byte(`{"id":"member","thread_name":"Earlier name"}`),
			[]byte(`{"id":"member","thread_name":"Current name"}`),
		},
	}
	metadata, err := metadataFromRows(t.Context(), rows)
	if err != nil || metadata.Session("member").Name != "Current name" {
		t.Fatalf("metadata = %#v, error = %v", metadata, err)
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

// Codex's guardian auto-review runs as a parented subagent rollout that the
// product hides, so it has no parsed record. The complete backup still keeps
// its exact bytes, with the member it reviewed, without changing that
// member's portable record.
func TestPrepareRetainsHiddenGuardianRolloutWithReviewedMember(t *testing.T) {
	home, _ := writeFamilyFixture(t, 0)
	prepare := func() (*Prepared, string) {
		spool := t.TempDir()
		manager := New(Options{Root: spool, OpenSource: func(context.Context, Selection) (SourceHandle, error) {
			return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
		}})
		prepared, err := manager.Prepare(t.Context(), localSelection())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sessionbackupv1.VerifyDirectory(filepath.Join(spool, prepared.BundleID)); err != nil {
			t.Fatalf("independent verification: %v", err)
		}
		return prepared, spool
	}
	rootRecordRevision := func(manifest sessionbackupv1.Manifest) string {
		for _, artifact := range manifest.Artifacts {
			if artifact.MemberID == testRootID && artifact.Kind == sessionbackupv1.KindParsedSessionRecord {
				return artifact.SourceKey
			}
		}
		return ""
	}
	withoutGuardian, _ := prepare()

	guardianFile := familyFile(home, false, testGuardianID)
	guardianFixture, err := os.ReadFile("testdata/guardian.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	guardianBytes := string(guardianFixture)
	writeRollout(t, guardianFile, guardianBytes)
	withoutIndex, _ := prepare()
	indexRows := []byte(fmt.Sprintf("{\"id\":%q,\"thread_name\":\"Fixture review\"}\r\n", testGuardianID))
	indexRows = append(indexRows, indexRows...)
	index, err := os.OpenFile(codex.SessionIndexPath(home), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = index.Write(indexRows)
	if closeErr := index.Close(); err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	withGuardian, spool := prepare()

	if len(withGuardian.Manifest.Members) != 2 {
		t.Fatalf("members = %#v, want only the represented root and child", withGuardian.Manifest.Members)
	}
	var rootRollouts []string
	var guardianSidecars int
	for _, artifact := range withGuardian.Manifest.Artifacts {
		if artifact.MemberID == testGuardianID {
			t.Fatalf("hidden guardian became artifact member: %#v", artifact)
		}
		if artifact.Kind == sessionbackupv1.KindRawSidecar && artifact.SourceKey == "session_index-"+testGuardianID {
			data, err := os.ReadFile(filepath.Join(spool, withGuardian.BundleID, filepath.FromSlash(artifact.LogicalName)))
			if err != nil || artifact.MemberID != testRootID || !bytes.Equal(data, indexRows) {
				t.Fatalf("guardian sidecar = %#v, %q, %v", artifact, data, err)
			}
			guardianSidecars++
		}
		if artifact.MemberID == testRootID && artifact.Kind == sessionbackupv1.KindRawTranscript {
			rootRollouts = append(rootRollouts, artifact.LogicalName)
		}
	}
	retained := false
	for _, name := range rootRollouts {
		data, err := os.ReadFile(filepath.Join(spool, withGuardian.BundleID, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		retained = retained || string(data) == guardianBytes
	}
	if len(rootRollouts) != 2 || !retained || guardianSidecars != 1 {
		t.Fatalf("root raw transcripts = %q, want its own rollout plus the exact guardian rollout", rootRollouts)
	}
	if got, want := rootRecordRevision(withGuardian.Manifest), rootRecordRevision(withoutGuardian.Manifest); got == "" || got != want {
		t.Fatalf("root record revision = %q, want unchanged %q", got, want)
	}
	if withGuardian.Manifest.Members[0].SourceRevision == withoutGuardian.Manifest.Members[0].SourceRevision {
		t.Fatal("root source revision ignores the retained guardian bytes")
	}
	if withGuardian.Manifest.Members[0].SourceRevision == withoutIndex.Manifest.Members[0].SourceRevision {
		t.Fatal("root source revision ignores the retained guardian index rows")
	}
}

func TestPrepareBlocksGuardianHeaderParserMismatch(t *testing.T) {
	home, _ := writeFamilyFixture(t, 0)
	rollout := guardianRollout(testGuardianID, testRootID, "/fixture/workspace") + fmt.Sprintf(
		"{\"timestamp\":\"2026-08-18T10:00:10.000Z\",\"type\":\"session_meta\",\"payload\":{\"id\":%q,\"session_id\":%q,\"parent_thread_id\":%q}}\n",
		testGuardianID, testGuardianID, testRootID)
	writeRollout(t, familyFile(home, false, testGuardianID), rollout)
	manager := New(Options{Root: t.TempDir(), OpenSource: func(context.Context, Selection) (SourceHandle, error) {
		return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}})
	prepared, err := manager.Prepare(t.Context(), localSelection())
	var preparation *PreparationError
	if prepared != nil || !errors.As(err, &preparation) || len(preparation.Coverage.Problems) != 1 ||
		preparation.Coverage.Problems[0].Code != sessionbackupv1.ProblemInvalid ||
		preparation.Coverage.Problems[0].Kind != sessionbackupv1.KindParsedSessionRecord {
		t.Fatalf("prepared=%#v error=%#v, want invalid parsed record", prepared, err)
	}
}

func TestPrepareAttributesGuardianSidecarToReviewedChild(t *testing.T) {
	home, _ := writeFamilyFixture(t, 0)
	fixture, err := os.ReadFile("testdata/guardian.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	writeRollout(t, familyFile(home, false, testGuardianID), strings.ReplaceAll(string(fixture), testRootID, testChildID))
	index, err := os.OpenFile(codex.SessionIndexPath(home), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(index, "{\"id\":%q,\"thread_name\":\"Fixture review\"}\n", testGuardianID)
	if closeErr := index.Close(); err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	spool := t.TempDir()
	manager := New(Options{Root: spool, OpenSource: func(context.Context, Selection) (SourceHandle, error) {
		return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}})
	prepared, err := manager.Prepare(t.Context(), localSelection())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessionbackupv1.VerifyDirectory(filepath.Join(spool, prepared.BundleID)); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range prepared.Manifest.Artifacts {
		if artifact.Kind == sessionbackupv1.KindRawSidecar && artifact.SourceKey == "session_index-"+testGuardianID {
			if artifact.MemberID != testChildID {
				t.Fatalf("guardian sidecar owner = %q, want reviewed child", artifact.MemberID)
			}
			return
		}
	}
	t.Fatal("guardian sidecar missing")
}

func TestPrepareRejectsGuardianIDWithConflictingOwners(t *testing.T) {
	home, _ := writeFamilyFixture(t, 0)
	writeRollout(t, familyFile(home, false, testGuardianID), guardianRollout(testGuardianID, testRootID, "/fixture/workspace"))
	writeRollout(t, familyFile(home, true, testGuardianID), guardianRollout(testGuardianID, testChildID, "/fixture/workspace"))
	index, err := os.OpenFile(codex.SessionIndexPath(home), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(index, "{\"id\":%q,\"thread_name\":\"Fixture review\"}\n", testGuardianID)
	if closeErr := index.Close(); err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	manager := New(Options{Root: t.TempDir(), OpenSource: func(context.Context, Selection) (SourceHandle, error) {
		return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}})
	prepared, err := manager.Prepare(t.Context(), localSelection())
	var preparation *PreparationError
	if prepared != nil || !errors.As(err, &preparation) || len(preparation.Coverage.Problems) != 1 ||
		preparation.Coverage.Problems[0].Code != sessionbackupv1.ProblemUnattributable ||
		preparation.Coverage.Problems[0].Kind != sessionbackupv1.KindRawTranscript {
		t.Fatalf("prepared=%#v error=%#v, want unattributable raw transcript", prepared, err)
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
			Synthesis: session.SessionSynthesis{Goals: []string{"first", "second"}, Outcome: "done", KeyDecisions: []string{"keep order", "verify"}, NextStep: "ship"},
		},
	}}
	exactPrepared, exactSpool := prepare(exact)
	exactRecord := readRootRecord(t, exactSpool, exactPrepared)
	if exactRecord.RevisionID != baselineRecord.RevisionID || exactRecord.Session.Synthesis != nil {
		t.Fatalf("portable record changed for synthesis: baseline=%q exact=%q synthesis=%#v", baselineRecord.RevisionID, exactRecord.RevisionID, exactRecord.Session.Synthesis)
	}
	found := false
	for _, artifact := range exactPrepared.Manifest.Artifacts {
		if artifact.Kind != sessionbackupv1.KindSynthesis || artifact.MemberID != testRootID {
			continue
		}
		found = true
		body, err := os.ReadFile(filepath.Join(exactSpool, exactPrepared.BundleID, filepath.FromSlash(artifact.LogicalName)))
		if err != nil {
			t.Fatal(err)
		}
		record, err := sessionbackupv1.DecodeSynthesisRecord(body)
		if err != nil {
			t.Fatal(err)
		}
		if record.Revision != baselineRecord.Session.LastActivityAtMs || record.Model != "fixture" || record.GeneratedAt != 1 ||
			!slices.Equal(record.Synthesis.Goals, []string{"first", "second"}) || record.Synthesis.Outcome != "done" ||
			!slices.Equal(record.Synthesis.KeyDecisions, []string{"keep order", "verify"}) || record.Synthesis.NextStep != "ship" ||
			exactPrepared.Manifest.Members[0].SynthesisRevisionMs != record.Revision {
			t.Fatalf("synthesis artifact or revision mismatch: %+v", record)
		}
	}
	if !found {
		t.Fatal("exact synthesis artifact missing")
	}
	if _, err := sessionbackupv1.VerifyDirectory(filepath.Join(exactSpool, exactPrepared.BundleID)); err != nil {
		t.Fatalf("complete backup verification: %v", err)
	}
}

type fixtureSynthesisRunner struct{ result session.SessionSynthesis }

func (r fixtureSynthesisRunner) Run(context.Context, string) (session.SessionSynthesis, error) {
	return r.result, nil
}

func (fixtureSynthesisRunner) ModelName() string { return "synthetic-model" }

func TestSyntheticGenerationThenCompleteBackupCapture(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	home, _ := writeFamilyFixture(t, 0)
	spool := t.TempDir()
	openSource := func(context.Context, Selection) (SourceHandle, error) {
		return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}
	baseline, err := New(Options{Root: spool, OpenSource: openSource}).Prepare(t.Context(), localSelection())
	if err != nil {
		t.Fatal(err)
	}
	revision := readRootRecord(t, spool, baseline).Session.LastActivityAtMs
	want := session.SessionSynthesis{Goals: []string{"first", "second"}, Outcome: "done",
		KeyDecisions: []string{"keep order"}, NextStep: "ship"}
	mgr := synthesis.NewManager(fixtureSynthesisRunner{result: want})
	if !mgr.Ensure(&session.Session{ID: testRootID, Agent: vendors.AgentCodex, LastActivityTime: revision,
		SessionDetails: session.SessionDetails{Turns: 6}}, revision) {
		t.Fatal("synthetic generation did not start")
	}
	deadline := time.Now().Add(3 * time.Second)
	for mgr.Lookup(vendors.AgentCodex, testRootID, revision) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if mgr.Lookup(vendors.AgentCodex, testRootID, revision) == nil {
		t.Fatal("synthetic generation did not persist")
	}
	captureSpool := t.TempDir()
	prepared, err := New(Options{Root: captureSpool, Synthesis: mgr, OpenSource: openSource}).Prepare(t.Context(), localSelection())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, artifact := range prepared.Manifest.Artifacts {
		if artifact.MemberID != testRootID || artifact.Kind != sessionbackupv1.KindSynthesis {
			continue
		}
		found = true
		if prepared.Manifest.Members[0].SynthesisRevisionMs != revision {
			t.Fatalf("captured synthesis revision = %d, want %d", prepared.Manifest.Members[0].SynthesisRevisionMs, revision)
		}
		body, err := os.ReadFile(filepath.Join(captureSpool, prepared.BundleID, filepath.FromSlash(artifact.LogicalName)))
		if err != nil {
			t.Fatal(err)
		}
		record, err := sessionbackupv1.DecodeSynthesisRecord(body)
		if err != nil || record.Revision != revision || record.Model != "synthetic-model" ||
			!slices.Equal(record.Synthesis.Goals, want.Goals) || record.Synthesis.Outcome != want.Outcome ||
			!slices.Equal(record.Synthesis.KeyDecisions, want.KeyDecisions) || record.Synthesis.NextStep != want.NextStep {
			t.Fatalf("generated debrief changed during capture: %+v, err=%v", record, err)
		}
	}
	if !found {
		t.Fatal("generated synthesis was not included in the complete backup")
	}
}

func TestKnownRawSizesAreBoundedBeforeCopy(t *testing.T) {
	if withinKnownBounds([]vendors.FileFingerprint{{Size: sessionbackupv1.MaxArtifactBytes + 1}}) {
		t.Fatal("oversized artifact accepted")
	}
	if !exceedsKnownBounds([]vendors.FileFingerprint{{Size: sessionbackupv1.MaxArtifactBytes + 1}}) {
		t.Fatal("oversized artifact lost its size cause")
	}
	aggregate := make([]vendors.FileFingerprint, sessionbackupv1.MaxTotalBytes/sessionbackupv1.MaxArtifactBytes+1)
	for index := range aggregate {
		aggregate[index].Size = sessionbackupv1.MaxArtifactBytes
	}
	if withinKnownBounds(aggregate) {
		t.Fatal("oversized aggregate accepted")
	}
	if !exceedsKnownBounds(aggregate) {
		t.Fatal("oversized aggregate lost its size cause")
	}
	if !withinKnownBounds([]vendors.FileFingerprint{{Size: sessionbackupv1.MaxArtifactBytes}}) {
		t.Fatal("artifact boundary rejected")
	}
	if exceedsKnownBounds([]vendors.FileFingerprint{{Size: sessionbackupv1.MaxArtifactBytes}}) {
		t.Fatal("artifact at the boundary marked too large")
	}
}

func TestOversizedRawArtifactPreservesTooLargeCause(t *testing.T) {
	home, _ := writeFamilyFixture(t, 0)
	if err := os.Truncate(familyFile(home, false, testRootID), sessionbackupv1.MaxArtifactBytes+1); err != nil {
		t.Fatal(err)
	}
	manager := New(Options{Root: t.TempDir(), OpenSource: func(context.Context, Selection) (SourceHandle, error) {
		return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}})
	_, err := manager.Prepare(t.Context(), Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentCodex, SessionID: testRootID})
	var failure *PreparationError
	if !errors.As(err, &failure) || !failure.TooLarge || len(failure.Coverage.Problems) != 1 || failure.Coverage.Problems[0].Code != sessionbackupv1.ProblemInvalid {
		t.Fatalf("oversized artifact failure = %v, want invalid coverage and too-large cause", err)
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

func TestInterruptedRefreshRetainsVerifiedLastGoodBundle(t *testing.T) {
	for _, sourceKind := range []string{sessionbackupv1.SourceLocal, sessionbackupv1.SourceSSH} {
		t.Run(sourceKind, func(t *testing.T) {
			home, _ := writeFamilyFixture(t, 0)
			rootFile := familyFile(home, false, testRootID)
			spool := t.TempDir()
			selection := localSelection()
			selection.SourceKind = sourceKind
			if sourceKind == sessionbackupv1.SourceSSH {
				selection.SourceID = "remote-fixture"
			}
			options := Options{Root: spool, OpenSource: func(context.Context, Selection) (SourceHandle, error) {
				return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
			}}
			good, err := New(options).Prepare(t.Context(), selection)
			if err != nil {
				t.Fatal(err)
			}
			options.AfterRawCopy = func() {
				file, err := os.OpenFile(rootFile, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = file.WriteString(" \n")
				_ = file.Close()
			}
			if _, err := New(options).Prepare(t.Context(), selection); !errors.Is(err, ErrIncomplete) {
				t.Fatalf("interrupted refresh error = %v", err)
			}
			reopened, err := New(Options{Root: spool}).Open(good.BundleID)
			if err != nil || reopened.BundleID != good.BundleID {
				t.Fatalf("last good bundle = %#v, %v", reopened, err)
			}
			if _, err := sessionbackupv1.VerifyDirectory(filepath.Join(spool, good.BundleID)); err != nil {
				t.Fatalf("last good verification: %v", err)
			}
		})
	}
}

func TestPrepareReadsLiveFamilyOnceWithinSourceBudget(t *testing.T) {
	home, _ := writeFamilyFixture(t, 64<<10)
	var budget int64 = 2 * 1024
	for _, name := range []string{familyFile(home, false, testRootID), familyFile(home, true, testChildID), codex.SessionIndexPath(home)} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		budget += info.Size()
	}
	source := &byteBudgetSource{ReadSource: vendors.LocalReadSource, remaining: budget}
	manager := New(Options{
		Root: t.TempDir(),
		OpenSource: func(context.Context, Selection) (SourceHandle, error) {
			return SourceHandle{Source: source, Home: home}, nil
		},
	})
	selection := localSelection()
	selection.SourceKind, selection.SourceID = sessionbackupv1.SourceSSH, "remote-budget"
	if _, err := manager.Prepare(t.Context(), selection); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareScopesPersistedSynthesisToLocalSource(t *testing.T) {
	for _, sourceKind := range []string{sessionbackupv1.SourceLocal, sessionbackupv1.SourceSSH} {
		t.Run(sourceKind, func(t *testing.T) {
			home, _ := writeFamilyFixture(t, 0)
			store := &missingSynthesisStore{}
			manager := New(Options{
				Root: t.TempDir(), Synthesis: store,
				OpenSource: func(context.Context, Selection) (SourceHandle, error) {
					return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
				},
			})
			selection := localSelection()
			selection.SourceKind = sourceKind
			if sourceKind == sessionbackupv1.SourceSSH {
				selection.SourceID = "remote-fixture"
			}
			if _, err := manager.Prepare(t.Context(), selection); err != nil {
				t.Fatal(err)
			}
			wantLoads := 2
			if sourceKind == sessionbackupv1.SourceSSH {
				wantLoads = 0
			}
			if store.loads != wantLoads {
				t.Fatalf("synthesis loads = %d; want %d", store.loads, wantLoads)
			}
		})
	}
}

func TestStartCancellationAfterRenameDropsPublishedBundle(t *testing.T) {
	home, _ := writeFamilyFixture(t, 0)
	spool := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var publishedBundle string
	manager := New(Options{
		Root: spool, Lifecycle: ctx,
		OpenSource: func(context.Context, Selection) (SourceHandle, error) {
			return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
		},
		AfterRename: func() {
			entries, _ := os.ReadDir(spool)
			for _, entry := range entries {
				if !strings.HasPrefix(entry.Name(), ".preparing-") {
					publishedBundle = entry.Name()
				}
			}
			cancel()
		},
	})
	id, err := manager.Start(t.Context(), localSelection())
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.Wait(t.Context(), id)
	if err != nil || state.State != StateCancelled || state.Prepared != nil || publishedBundle == "" {
		t.Fatalf("cancelled state = %#v, published=%q, err=%v", state, publishedBundle, err)
	}
	if _, err := manager.Open(publishedBundle); !errors.Is(err, ErrNotPrepared) {
		t.Fatalf("open cancelled bundle = %v", err)
	}
}

func TestStartCancellationAfterPrepareDropsOwnedBundle(t *testing.T) {
	home, _ := writeFamilyFixture(t, 0)
	spool := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var publishedBundle string
	manager := New(Options{
		Root: spool, Lifecycle: ctx,
		OpenSource: func(context.Context, Selection) (SourceHandle, error) {
			return SourceHandle{Source: vendors.LocalReadSource, Home: home, Close: func() error {
				entries, _ := os.ReadDir(spool)
				for _, entry := range entries {
					if !strings.HasPrefix(entry.Name(), ".preparing-") {
						publishedBundle = entry.Name()
					}
				}
				cancel()
				return nil
			}}, nil
		},
	})
	id, err := manager.Start(t.Context(), localSelection())
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.Wait(t.Context(), id)
	if err != nil || state.State != StateCancelled || state.Prepared != nil || publishedBundle == "" {
		t.Fatalf("cancelled state = %#v, published=%q, err=%v", state, publishedBundle, err)
	}
	if _, err := manager.Open(publishedBundle); !errors.Is(err, ErrNotPrepared) {
		t.Fatalf("open cancelled bundle = %v", err)
	}
}

func TestStartCancellationAfterReusingBundleKeepsPublishedBundle(t *testing.T) {
	home, _ := writeFamilyFixture(t, 0)
	spool := t.TempDir()
	openSource := func(context.Context, Selection) (SourceHandle, error) {
		return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}
	seed := New(Options{Root: spool, OpenSource: openSource})
	prepared, err := seed.Prepare(t.Context(), localSelection())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	manager := New(Options{
		Root: spool, Lifecycle: ctx,
		OpenSource: func(context.Context, Selection) (SourceHandle, error) {
			return SourceHandle{Source: vendors.LocalReadSource, Home: home, Close: func() error {
				cancel()
				return nil
			}}, nil
		},
	})
	id, err := manager.Start(t.Context(), localSelection())
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.Wait(t.Context(), id)
	if err != nil || state.State != StateCancelled {
		t.Fatalf("cancelled state = %#v, err=%v", state, err)
	}
	if _, err := manager.Open(prepared.BundleID); err != nil {
		t.Fatalf("open reused bundle = %v", err)
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

func guardianRollout(id, parentID, workspace string) string {
	return fmt.Sprintf(`{"timestamp":"2026-08-18T10:00:07.000Z","type":"session_meta","payload":{"id":%q,"session_id":%q,"cwd":%q,"parent_thread_id":%q,"source":{"subagent":{"other":"guardian"}}}}
{"timestamp":"2026-08-18T10:00:08.000Z","type":"event_msg","payload":{"type":"user_message","message":"synthetic review request"}}
{"timestamp":"2026-08-18T10:00:09.000Z","type":"event_msg","payload":{"type":"agent_message","phase":"final_answer","message":"synthetic verdict"}}
`, id, id, workspace, parentID)
}

func localSelection() Selection {
	return Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentCodex, SessionID: testRootID}
}

type trackingSource struct {
	vendors.ReadSource
	maxRead   atomic.Int64
	totalRead atomic.Int64
}

type byteBudgetSource struct {
	vendors.ReadSource
	remaining int64
}

type missingSynthesisStore struct {
	loads int
}

func (store *missingSynthesisStore) LoadRecord(string, string) (synthesis.Record, error) {
	store.loads++
	return synthesis.Record{}, os.ErrNotExist
}

func (source *byteBudgetSource) Open(name string) (io.ReadCloser, error) {
	reader, err := source.ReadSource.Open(name)
	if err != nil {
		return nil, err
	}
	return &byteBudgetReader{ReadCloser: reader, source: source}, nil
}

type byteBudgetReader struct {
	io.ReadCloser
	source *byteBudgetSource
}

func (reader *byteBudgetReader) Read(destination []byte) (int, error) {
	if reader.source.remaining <= 0 {
		return 0, errors.New("source byte budget exhausted")
	}
	if int64(len(destination)) > reader.source.remaining {
		destination = destination[:reader.source.remaining]
	}
	count, err := reader.ReadCloser.Read(destination)
	reader.source.remaining -= int64(count)
	return count, err
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
