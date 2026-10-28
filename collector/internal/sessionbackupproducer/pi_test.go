package sessionbackupproducer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/pi"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func piFixture(t *testing.T) (string, string, Selection) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_PI_SESSION_ROOTS", filepath.Join(home, "custom"))
	t.Setenv("COSLASH_HOME", filepath.Join(home, "coslash"))
	t.Chdir(home)
	path := filepath.Join(home, "custom", "unrelated-name.jsonl")
	writePiTranscript(t, path, "header.identity_08", "", piAssistant("a", "", "useful root", "model", 3))
	return home, path, Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentPi, SessionID: "header.identity_08"}
}

func piInputs(t *testing.T, id string) pi.SessionInputs {
	t.Helper()
	plan, err := pi.PlanSourcesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := plan.SessionInputsContext(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return inputs
}

func TestPiForkEvidenceIsPrivateAndReplayUsesOnlyFrozenBytes(t *testing.T) {
	home, path, selection := piFixture(t)
	ancestor := filepath.Join(home, "outside-discovery", "private-ancestor.jsonl")
	inherited := piAssistant("a", "", "inherited", "model", 9)
	writePiTranscript(t, ancestor, "private-parent", "", inherited)
	writePiTranscript(t, path, selection.SessionID, ancestor, inherited, piAssistant("b", "a", "novel", "model", 3))
	inputs := piInputs(t, selection.SessionID)
	if len(inputs.Files) != 2 {
		t.Fatalf("inputs = %+v", inputs)
	}
	spool := t.TempDir()
	prepared, err := grokManager(spool, home, nil).Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	record := grokRecords(t, spool, prepared)[selection.SessionID]
	if len(prepared.Manifest.Members) != 1 || record.ParentSessionID != "" || len(record.Session.Subagents) != 0 || record.Session.DetailsIncomplete || len(record.Session.Usage) != 1 || record.Session.Usage[0].InputTokens != 3 {
		t.Fatalf("fork = %+v", record)
	}
	raw := 0
	for _, artifact := range prepared.Manifest.Artifacts {
		if artifact.MemberID != selection.SessionID {
			t.Fatalf("ancestor became member: %+v", artifact)
		}
		if artifact.Kind == sessionbackupv1.KindRawTranscript {
			data, err := os.ReadFile(filepath.Join(spool, prepared.BundleID, filepath.FromSlash(artifact.LogicalName)))
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(inputs.Files[raw])
			if err != nil || !bytes.Equal(data, original) {
				t.Fatalf("raw bytes differ: %v", err)
			}
			raw++
		}
	}
	if raw != 2 {
		t.Fatalf("raw artifacts = %d", raw)
	}
	t.Setenv("COSLASH_PI_SESSION_ROOTS", strings.Join([]string{filepath.Dir(path), filepath.Dir(ancestor)}, string(os.PathListSeparator)))
	parentSelection := selection
	parentSelection.SessionID = "private-parent"
	parent, err := grokManager(spool, home, nil).Prepare(t.Context(), parentSelection)
	if err != nil || len(parent.Manifest.Members) != 1 || parent.Manifest.Family.RootMemberID != parentSelection.SessionID || parent.BundleID == prepared.BundleID {
		t.Fatalf("independent parent = %+v, %v", parent, err)
	}
	data, err := fullsessionv1.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	assertNoPiPrivateEvidence(t, decoded, path, ancestor, "private-parent", "pi-runtime", "processStartIdentity", "updatedAtMs")
	frozen, err := freezePiInputs(t.Context(), t.TempDir(), inputs)
	if err != nil {
		t.Fatal(err)
	}
	want, revision, err := frozen.project(t.Context(), inputs)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range inputs.Files {
		if err := os.Remove(source); err != nil {
			t.Fatal(err)
		}
	}
	got, replayRevision, err := frozen.project(t.Context(), inputs)
	if err != nil || replayRevision != revision || !reflect.DeepEqual(got, want) {
		t.Fatalf("frozen replay changed: %v", err)
	}
}

func TestPiRevisionIncludesExactPrivateAncestorBytes(t *testing.T) {
	_, path, selection := piFixture(t)
	ancestor := filepath.Join(t.TempDir(), "ancestor.jsonl")
	row := piAssistant("a", "", "inherited", "model", 9)
	writePiTranscript(t, ancestor, "parent", "", row)
	writePiTranscript(t, path, selection.SessionID, ancestor, row, piAssistant("b", "a", "novel", "model", 3))
	inputs := piInputs(t, selection.SessionID)
	before, err := PiSourceRevision(t.Context(), inputs)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(ancestor)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ancestor)
	if err != nil {
		t.Fatal(err)
	}
	writeGrokInput(t, ancestor, bytes.Replace(data, []byte("inherited"), []byte("different"), 1))
	if err := os.Chtimes(ancestor, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := PiSourceRevision(t.Context(), piInputs(t, selection.SessionID))
	if err != nil || before == after {
		t.Fatalf("private rewrite missed: %s, %s, %v", before, after, err)
	}
	frozen, err := freezePiInputs(t.Context(), t.TempDir(), piInputs(t, selection.SessionID))
	if err != nil {
		t.Fatal(err)
	}
	writeGrokInput(t, ancestor, data)
	if err := os.Chtimes(ancestor, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := frozen.verify(t.Context(), inputs); err == nil {
		t.Fatal("unstable private ancestor accepted")
	}
}

func assertNoPiPrivateEvidence(t *testing.T, value any, private ...string) {
	t.Helper()
	switch value := value.(type) {
	case string:
		for _, forbidden := range private {
			if strings.Contains(value, forbidden) {
				t.Fatalf("private evidence in curated value: %q", value)
			}
		}
	case []any:
		for _, item := range value {
			assertNoPiPrivateEvidence(t, item, private...)
		}
	case map[string]any:
		for key, item := range value {
			assertNoPiPrivateEvidence(t, key, private...)
			assertNoPiPrivateEvidence(t, item, private...)
		}
	}
}

func TestPiIncompleteSourceRetainsUsefulTruthfulDetail(t *testing.T) {
	for _, scenario := range []string{"missing ancestor", "corrupt ancestor", "partial tail"} {
		t.Run(scenario, func(t *testing.T) {
			home, path, selection := piFixture(t)
			parent := filepath.Join(home, "outside", "parent.jsonl")
			if scenario == "partial tail" {
				parent = ""
			} else if scenario == "corrupt ancestor" {
				writePiTranscript(t, parent, "parent", "", "{invalid}")
			}
			writePiTranscript(t, path, selection.SessionID, parent, piAssistant("a", "", "useful", "model", 3))
			if scenario == "partial tail" {
				file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := file.WriteString(`{"type":"message"`)
				if closeErr := file.Close(); writeErr != nil || closeErr != nil {
					t.Fatal(writeErr, closeErr)
				}
			}
			spool := t.TempDir()
			prepared, err := grokManager(spool, home, nil).Prepare(t.Context(), selection)
			if err != nil {
				t.Fatal(err)
			}
			record := grokRecords(t, spool, prepared)[selection.SessionID]
			if !record.Session.DetailsIncomplete || record.Session.Summary == nil || *record.Session.Summary != "useful" || record.Session.Usage != nil || record.Session.UnattributedTokens != nil || record.Session.CostMicroUSD != nil {
				t.Fatalf("fallback = %+v", record.Session)
			}
		})
	}
}

func TestPiFailedRefreshKeepsLastGoodAndCleansStaging(t *testing.T) {
	for _, scenario := range []string{"bytes", "identity", "replaced file", "duplicate ID", "ancestor added", "ancestor removed", "malformed root", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			home, path, selection := piFixture(t)
			parent := filepath.Join(home, "outside", "parent.jsonl")
			if strings.HasPrefix(scenario, "ancestor") {
				writePiTranscript(t, path, selection.SessionID, parent, piAssistant("a", "", "useful", "model", 3))
				if scenario == "ancestor removed" {
					writePiTranscript(t, parent, "parent", "", piAssistant("a", "", "useful", "model", 3))
				}
			}
			spool := t.TempDir()
			manager := grokManager(spool, home, nil)
			first, err := manager.Prepare(t.Context(), selection)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			manager.afterRawCopy = func() {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "bytes":
					info, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					writeGrokInput(t, path, bytes.Replace(data, []byte("useful root"), []byte("edited root"), 1))
					if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
						t.Fatal(err)
					}
				case "identity":
					writeGrokInput(t, path, bytes.Replace(data, []byte(selection.SessionID), []byte("different-identity"), 1))
				case "replaced file":
					if err := os.Rename(path, path+".old"); err != nil {
						t.Fatal(err)
					}
					writeGrokInput(t, path, data)
				case "duplicate ID":
					writeGrokInput(t, filepath.Join(filepath.Dir(path), "duplicate.jsonl"), data)
				case "ancestor added":
					writePiTranscript(t, parent, "parent", "", piAssistant("a", "", "useful", "model", 3))
				case "ancestor removed":
					if err := os.Remove(parent); err != nil {
						t.Fatal(err)
					}
				case "malformed root":
					writeGrokInput(t, path, append(data, []byte("{invalid}\n")...))
				case "cancel":
					cancel()
				}
			}
			prepared, err := manager.Prepare(ctx, selection)
			var failure *PreparationError
			if prepared != nil || (scenario == "cancel" && !errors.Is(err, context.Canceled)) || (scenario != "cancel" && (!errors.As(err, &failure) || failure.Coverage.Problems[0].Code != sessionbackupv1.ProblemUnstable || !failure.Coverage.Problems[0].Retryable)) {
				t.Fatalf("unstable capture = %+v, %v", prepared, err)
			}
			if _, err := manager.Open(first.BundleID); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(spool)
			if err != nil || len(entries) != 1 || entries[0].Name() != first.BundleID {
				t.Fatalf("staging leaked = %v, %v", entries, err)
			}
		})
	}
}

func TestPiAccountingAndObservedOutputs(t *testing.T) {
	for _, scenario := range []string{"known zero", "unknown", "mixed", "command output"} {
		t.Run(scenario, func(t *testing.T) {
			home, path, selection := piFixture(t)
			rows := []string{}
			switch scenario {
			case "known zero":
				rows = append(rows, `{"type":"message","id":"u","timestamp":"2026-10-01T12:00:01Z","message":{"role":"user","content":"hello"}}`)
			case "unknown":
				rows = append(rows, `{"type":"message","id":"a","timestamp":"2026-10-01T12:00:01Z","message":{"role":"assistant","content":"unknown"}}`)
			case "mixed":
				rows = append(rows, piAssistant("a", "", "known", "model", 3), piAssistant("b", "a", "unattributed", "", 7))
			case "command output":
				rows = append(rows, `{"type":"message","id":"a","timestamp":"2026-10-01T12:00:01Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"pr","name":"bash","arguments":{"command":"gh pr create --fill"}}]}}`, `{"type":"message","id":"r","parentId":"a","timestamp":"2026-10-01T12:00:02Z","message":{"role":"toolResult","toolCallId":"pr","content":"https://github.com/example/repo/pull/8","isError":false}}`)
			}
			writePiTranscript(t, path, selection.SessionID, "", rows...)
			spool := t.TempDir()
			prepared, err := grokManager(spool, home, nil).Prepare(t.Context(), selection)
			if err != nil {
				t.Fatal(err)
			}
			s := grokRecords(t, spool, prepared)[selection.SessionID].Session
			switch scenario {
			case "known zero":
				if s.Usage == nil || len(s.Usage) != 0 || s.UnattributedTokens != nil || s.CostMicroUSD == nil || *s.CostMicroUSD != 0 {
					t.Fatalf("zero = %+v", s)
				}
			case "unknown":
				if s.Usage != nil || s.UnattributedTokens != nil || s.CostMicroUSD != nil {
					t.Fatalf("unknown = %+v", s)
				}
			case "mixed":
				if len(s.Usage) != 1 || s.Usage[0].InputTokens != 3 || s.Usage[0].CostMicroUSD != nil || s.UnattributedTokens == nil || s.UnattributedTokens.InputTokens != 7 || s.UnattributedTokens.CostMicroUSD != nil || s.CostMicroUSD == nil || *s.CostMicroUSD != 10_000_000 {
					t.Fatalf("mixed = %+v", s)
				}
			case "command output":
				if len(s.CommandOutputs) != 1 || s.CommandOutputs[0] == nil || *s.CommandOutputs[0] != "https://github.com/example/repo/pull/8" || len(s.PullRequestLinks) != 1 || s.PullRequestLinks[0].Number != 8 {
					t.Fatalf("outputs = %+v", s)
				}
			}
		})
	}
}

func writePiOwner(t *testing.T, path, id, leaf, modality string, sequence int64) {
	t.Helper()
	identity, err := pi.ProcessStartIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(pi.RuntimeRecord{
		Version: 1, RuntimeID: "private-runtime", PID: os.Getpid(), ProcessStartIdentity: identity,
		StartedAtMs: 1, SessionID: id, TranscriptPath: path, LeafID: &leaf,
		Entrypoint: modality, WorkState: "idle", Sequence: sequence, UpdatedAtMs: sequence,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeGrokInput(t, filepath.Join(os.Getenv("COSLASH_HOME"), "pi-runtime", "private-runtime.json"), data)
}

func TestPiSourceRevisionTracksEffectiveLeafWithoutRuntimeIdentity(t *testing.T) {
	home, path, selection := piFixture(t)
	writePiTranscript(t, path, selection.SessionID, "", piAssistant("a", "", "earlier", "model", 2), piAssistant("b", "", "latest", "model", 3))
	revision := func() string {
		t.Helper()
		value, err := PiSourceRevision(t.Context(), piInputs(t, selection.SessionID))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	fallback := revision()
	writePiOwner(t, path, selection.SessionID, "a", "pi-tui", 1)
	inputs := piInputs(t, selection.SessionID)
	earlier := revision()
	if earlier == fallback {
		t.Fatal("verified earlier leaf did not change source revision")
	}
	spool := t.TempDir()
	prepared, err := grokManager(spool, home, nil).Prepare(t.Context(), selection)
	if err != nil || prepared.Manifest.Source.SourceRevision != earlier {
		t.Fatalf("capture revision differs: %+v, %v", prepared, err)
	}
	record := grokRecords(t, spool, prepared)[selection.SessionID]
	if record.Session.Summary == nil || *record.Session.Summary != "earlier" || record.Session.Entrypoint != nil {
		t.Fatalf("selected context = %+v", record.Session)
	}
	writePiOwner(t, path, selection.SessionID, "a", "pi-rpc", 2)
	if revision() != earlier {
		t.Fatal("runtime modality/sequence/heartbeat changed revision")
	}
	writePiOwner(t, path, selection.SessionID, "b", "pi-rpc", 3)
	if revision() == earlier {
		t.Fatal("leaf-only change did not change source revision")
	}
	if sameSnapshot, err := PiSourceRevision(t.Context(), inputs); err != nil || sameSnapshot != earlier {
		t.Fatalf("caller snapshot mutated = %s, %v", sameSnapshot, err)
	}
	for _, leaf := range []string{"missing", "also-missing"} {
		writePiOwner(t, path, selection.SessionID, leaf, "pi-rpc", 4)
		if revision() != fallback {
			t.Fatal("invalid leaves were not normalized to fallback")
		}
	}
	writePiOwner(t, path, selection.SessionID, "a", "pi-tui", 5)
	snapshot, err := pi.LoadRuntimeSnapshotContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	manager := grokManager(spool, home, func() { writePiOwner(t, path, selection.SessionID, "b", "pi-tui", 6) })
	if _, err := manager.Prepare(pi.WithRuntimeSnapshot(t.Context(), snapshot), selection); err == nil {
		t.Fatal("context changed during capture but publication succeeded")
	}
	if _, err := manager.Open(prepared.BundleID); err != nil {
		t.Fatal(err)
	}
}

func TestPiInvalidRootDuplicateIdentityAndBounds(t *testing.T) {
	for _, scenario := range []string{"malformed", "duplicate", "oversize", "unsafe ID"} {
		t.Run(scenario, func(t *testing.T) {
			home, path, selection := piFixture(t)
			inputs := piInputs(t, selection.SessionID)
			switch scenario {
			case "malformed":
				writePiTranscript(t, path, selection.SessionID, "", "{invalid}")
			case "duplicate":
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				writeGrokInput(t, filepath.Join(filepath.Dir(path), "duplicate.jsonl"), data)
			case "oversize":
				if err := os.Truncate(path, sessionbackupv1.MaxArtifactBytes+1); err != nil {
					t.Fatal(err)
				}
				_, err := PiSourceRevision(t.Context(), inputs)
				var failure *PreparationError
				if !errors.As(err, &failure) || !failure.TooLarge {
					t.Fatalf("revision lost size classification: %v", err)
				}
			case "unsafe ID":
				selection.SessionID = "../../escape"
				writePiTranscript(t, path, selection.SessionID, "", piAssistant("a", "", "useful", "model", 1))
			}
			spool := t.TempDir()
			_, err := grokManager(spool, home, nil).Prepare(t.Context(), selection)
			var failure *PreparationError
			if !errors.As(err, &failure) || failure.Coverage.Problems[0].Retryable {
				t.Fatalf("invalid capture = %v (%+v)", err, failure)
			}
			entries, err := os.ReadDir(spool)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed capture leaked = %v, %v", entries, err)
			}
		})
	}
	_, _, selection := piFixture(t)
	inputs := piInputs(t, selection.SessionID)
	inputs.Files = make([]string, 258)
	if _, err := PiSourceRevision(t.Context(), inputs); err == nil {
		t.Fatal("unbounded ancestry accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := PiSourceRevision(ctx, inputs); !errors.Is(err, context.Canceled) {
		t.Fatalf("revision cancellation = %v", err)
	}
}

func TestPiSpoolResumeSynthesisAndPublicationCancellation(t *testing.T) {
	home, _, selection := piFixture(t)
	spool := t.TempDir()
	manager := grokManager(spool, home, nil)
	baseline, err := manager.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	record := grokRecords(t, spool, baseline)[selection.SessionID]
	for _, delta := range []int64{-1, 0} {
		manager.synthesis = synthesisStore{records: map[string]synthesis.Record{selection.SessionID: {
			Revision: record.Session.LastActivityAtMs + delta, Model: "fixture", GeneratedAt: 1,
			Synthesis: session.SessionSynthesis{Goals: []string{"goal"}, Outcome: "done", NextStep: "review"},
		}}}
		prepared, err := manager.Prepare(t.Context(), selection)
		if err != nil {
			t.Fatal(err)
		}
		if prepared.Manifest.Source.SourceRevision != baseline.Manifest.Source.SourceRevision || grokRecords(t, spool, prepared)[selection.SessionID].RevisionID != record.RevisionID {
			t.Fatal("synthesis changed portable/source revision")
		}
		found := false
		for _, artifact := range prepared.Manifest.Artifacts {
			found = found || artifact.Kind == sessionbackupv1.KindSynthesis
		}
		if found != (delta == 0) {
			t.Fatal("synthesis was not revision matched")
		}
	}
	restarted := grokManager(spool, home, nil)
	reader, err := restarted.Reader(baseline.BundleID)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range baseline.Manifest.Artifacts {
		data, err := os.ReadFile(filepath.Join(spool, baseline.BundleID, filepath.FromSlash(artifact.LogicalName)))
		if err != nil {
			t.Fatal(err)
		}
		offset := len(data) / 2
		chunk := make([]byte, len(data)-offset)
		n, err := reader.Read(artifact.LogicalName, int64(offset), chunk)
		if err != nil && !errors.Is(err, io.EOF) || n != len(chunk) || !bytes.Equal(chunk, data[offset:]) {
			t.Fatalf("resumed read differs: %d, %v", n, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	freshSpool := t.TempDir()
	manager = grokManager(freshSpool, home, nil)
	manager.afterRename = cancel
	if _, err := manager.Prepare(ctx, selection); !errors.Is(err, context.Canceled) {
		t.Fatalf("publication cancellation = %v", err)
	}
	entries, err := os.ReadDir(freshSpool)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cancelled publication leaked: %v, %v", entries, err)
	}
}

func writePiTranscript(t *testing.T, path, id, parent string, rows ...string) {
	t.Helper()
	header, err := json.Marshal(map[string]any{"type": "session", "version": 3, "id": id, "cwd": filepath.Join(filepath.Dir(path), "project"), "timestamp": "2026-10-01T12:00:00Z", "parentSession": parent})
	if err != nil {
		t.Fatal(err)
	}
	writeGrokInput(t, path, []byte(string(header)+"\n"+strings.Join(rows, "\n")+"\n"))
}

func piAssistant(id, parent, text, model string, tokens int) string {
	row, err := json.Marshal(map[string]any{"type": "message", "id": id, "parentId": parent, "timestamp": "2026-10-01T12:00:01Z", "message": map[string]any{
		"role": "assistant", "model": model, "content": []map[string]string{{"type": "text", "text": text}},
		"usage": map[string]any{"input": tokens, "output": 0, "cacheRead": 0, "cacheWrite": 0, "cost": map[string]int{"total": tokens}},
	}})
	if err != nil {
		panic(err)
	}
	return string(row)
}

func TestPiPrepareCustomRootHeaderIdentityAndReopen(t *testing.T) {
	home, path, selection := piFixture(t)
	spool := t.TempDir()
	manager := grokManager(spool, home, nil)
	plan, err := pi.PlanSourcesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := plan.SessionInputsContext(t.Context(), selection.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := PiSourceRevision(t.Context(), inputs)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := manager.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Manifest.Members) != 1 || prepared.Manifest.Family.RootMemberID != selection.SessionID {
		t.Fatalf("family = %+v", prepared.Manifest.Family)
	}
	if prepared.Manifest.Source.SourceRevision != revision || prepared.Manifest.Members[0].SourceRevision != revision {
		t.Fatal("queue and producer source revisions differ")
	}
	record := grokRecords(t, spool, prepared)[selection.SessionID]
	if record.Agent != "pi" || record.ParentSessionID != "" || len(record.Session.Subagents) != 0 || record.Session.Summary == nil || *record.Session.Summary != "useful root" || len(record.Session.Usage) != 1 || record.Session.Usage[0].InputTokens != 3 {
		t.Fatalf("record = %+v", record)
	}
	again, err := manager.Prepare(t.Context(), selection)
	if err != nil || again.BundleID != prepared.BundleID {
		t.Fatalf("retry = %+v, %v", again, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	reopened, err := grokManager(spool, home, nil).Open(prepared.BundleID)
	if err != nil || reopened.BundleID != prepared.BundleID {
		t.Fatalf("reopen = %+v, %v", reopened, err)
	}
}
