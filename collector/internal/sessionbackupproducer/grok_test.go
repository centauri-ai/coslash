package sessionbackupproducer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func writeGrokInput(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func grokFixture(t *testing.T) (string, string, Selection) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GROK_HOME", filepath.Join(home, ".grok"))
	group := filepath.Join(home, ".grok", "sessions", "group")
	for _, item := range []struct {
		id, parent, kind string
		tokens           int
	}{
		{"root", "", "interactive", 12}, {"child", "root", "subagent", 7}, {"grandchild", "child", "subagent", 5},
		{"fork", "root", "fork", 2}, {"worktree", "root", "worktree", 3},
	} {
		summary, err := json.Marshal(map[string]any{
			"info":                map[string]string{"id": item.id, "cwd": filepath.Join(home, "project")},
			"chat_format_version": 1, "session_kind": item.kind, "parent_session_id": item.parent,
			"created_at": "2026-10-01T12:00:00Z", "updated_at": "2026-10-01T12:01:00Z",
		})
		if err != nil {
			t.Fatal(err)
		}
		writeGrokInput(t, filepath.Join(group, item.id, "summary.json"), summary)
		writeGrokInput(t, filepath.Join(group, item.id, "updates.jsonl"), []byte(`{"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"synthetic capture"}}}}`+"\n"+`{"params":{"update":{"sessionUpdate":"turn_completed","usage":{"inputTokens":1000}}}}`+"\n"))
		used, err := json.Marshal(map[string]any{"session": map[string]int{"inputTokens": item.tokens, "outputTokens": 3, "cachedReadTokens": 4}})
		if err != nil {
			t.Fatal(err)
		}
		writeGrokInput(t, filepath.Join(group, item.id, "usage.json"), used)
	}
	for _, link := range []struct{ parent, child string }{{"root", "child"}, {"child", "grandchild"}} {
		meta, err := json.Marshal(map[string]string{"child_session_id": link.child, "status": "completed", "description": "synthetic child"})
		if err != nil {
			t.Fatal(err)
		}
		writeGrokInput(t, filepath.Join(group, link.parent, "subagents", link.child, "meta.json"), meta)
	}
	writeGrokInput(t, filepath.Join(home, ".grok", "auth.json"), []byte("excluded synthetic secret"))
	return home, group, Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentGrok, SessionID: "root"}
}

func grokManager(spool, home string, after func()) *Manager {
	return New(Options{Root: spool, LocalHome: func() (string, error) { return home, nil }, AfterRawCopy: after})
}

func grokRecords(t *testing.T, spool string, prepared *Prepared) map[string]fullsessionv1.Record {
	t.Helper()
	manifest, err := sessionbackupv1.VerifyDirectory(filepath.Join(spool, prepared.BundleID))
	if err != nil {
		t.Fatal(err)
	}
	records := map[string]fullsessionv1.Record{}
	for _, artifact := range manifest.Artifacts {
		if artifact.Kind != sessionbackupv1.KindParsedSessionRecord {
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
		records[record.SessionID] = record
	}
	return records
}

func TestGrokCaptureCompleteFamilyAndRestart(t *testing.T) {
	home, _, selection := grokFixture(t)
	spool := t.TempDir()
	manager := grokManager(spool, home, nil)
	first, err := manager.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, member := range first.Manifest.Members {
		ids = append(ids, member.MemberID)
	}
	if !reflect.DeepEqual(ids, []string{"root", "child", "grandchild"}) {
		t.Fatalf("members = %v", ids)
	}
	records := grokRecords(t, spool, first)
	for id, want := range map[string]int{"root": 19, "child": 14, "grandchild": 12} {
		record := records[id]
		used := record.Session.UnattributedTokens
		if used == nil || used.InputTokens+used.OutputTokens+used.CacheReadInputTokens != want || used.CostMicroUSD != nil || len(record.Session.Usage) != 0 {
			t.Fatalf("%s accounting = %#v", id, record.Session)
		}
	}
	for parent, want := range map[string]int{"root": 14, "child": 12} {
		children := records[parent].Session.Subagents
		if len(children) != 1 || children[0].UnattributedTokens == nil {
			t.Fatalf("%s children=%+v", parent, children)
		}
		used := children[0].UnattributedTokens
		if used.InputTokens+used.OutputTokens+used.CacheReadInputTokens != want || used.CostMicroUSD != nil {
			t.Fatalf("%s child accounting=%+v", parent, used)
		}
	}
	if records["child"].ParentSessionID != "root" || records["grandchild"].ParentSessionID != "child" {
		t.Fatal("lineage lost")
	}
	for _, artifact := range first.Manifest.Artifacts {
		if artifact.Kind == sessionbackupv1.KindRawMetadataRows {
			t.Fatal("invented database artifact")
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(filepath.Join(spool, first.BundleID, filepath.FromSlash(artifact.LogicalName)))
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("artifact permissions: %v %v", info, err)
			}
		}
	}
	for _, id := range []string{"root", "child", "grandchild"} {
		selected := selection
		selected.SessionID = id
		again, err := manager.Prepare(t.Context(), selected)
		if err != nil || again.BundleID != first.BundleID || again.Selection.SessionID != "root" {
			t.Fatalf("selected %s: %#v %v", id, again, err)
		}
	}
	if _, err := grokManager(spool, home, nil).Open(first.BundleID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"fork", "worktree"} {
		selected := selection
		selected.SessionID = id
		separate, err := manager.Prepare(t.Context(), selected)
		if err != nil || len(separate.Manifest.Members) != 1 || separate.Manifest.Family.RootMemberID != id {
			t.Fatalf("independent %s: %#v %v", id, separate, err)
		}
	}
}

func TestGrokFailedRefreshRetainsVerifiedBundle(t *testing.T) {
	for _, scenario := range []string{"summary missing", "updates missing", "summary malformed", "updates malformed", "usage malformed", "plan malformed", "usage wrong type", "usage mutation", "optional added", "optional removed", "checkpoint added", "child added", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			home, group, selection := grokFixture(t)
			spool := t.TempDir()
			manager := grokManager(spool, home, nil)
			first, err := manager.Prepare(t.Context(), selection)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			root := filepath.Join(group, "root")
			switch scenario {
			case "summary missing", "updates missing":
				name := "summary.json"
				if scenario == "updates missing" {
					name = "updates.jsonl"
				}
				if err := os.Remove(filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
			case "summary malformed", "updates malformed", "usage malformed", "plan malformed":
				name := map[string]string{"summary malformed": "summary.json", "updates malformed": "updates.jsonl", "usage malformed": "usage.json", "plan malformed": "plan.json"}[scenario]
				writeGrokInput(t, filepath.Join(root, name), []byte("{\n"))
			case "usage wrong type":
				writeGrokInput(t, filepath.Join(root, "usage.json"), []byte(`{"session":{"inputTokens":"bad"}}`))
			case "optional removed":
				writeGrokInput(t, filepath.Join(root, "signals.json"), []byte(`{}`))
				manager.afterRawCopy = func() {
					if err := os.Remove(filepath.Join(root, "signals.json")); err != nil {
						t.Fatal(err)
					}
				}
			case "usage mutation":
				file := filepath.Join(root, "usage.json")
				info, err := os.Stat(file)
				if err != nil {
					t.Fatal(err)
				}
				manager.afterRawCopy = func() {
					data, err := os.ReadFile(file)
					if err != nil {
						t.Fatal(err)
					}
					for i, b := range data {
						if b == '2' {
							data[i] = '3'
							break
						}
					}
					writeGrokInput(t, file, data)
					if err := os.Chtimes(file, info.ModTime(), info.ModTime()); err != nil {
						t.Fatal(err)
					}
				}
			case "optional added":
				manager.afterRawCopy = func() { writeGrokInput(t, filepath.Join(root, "plan.md"), []byte("new plan")) }
			case "checkpoint added":
				manager.afterRawCopy = func() { writeGrokInput(t, filepath.Join(root, "compaction_checkpoints", "new.json"), []byte(`{}`)) }
			case "child added":
				manager.afterRawCopy = func() {
					writeGrokInput(t, filepath.Join(root, "subagents", "new", "meta.json"), []byte(`{"child_session_id":"missing"}`))
				}
			case "cancel":
				manager.afterRawCopy = cancel
			}
			_, err = manager.Prepare(ctx, selection)
			if scenario == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation = %v", err)
				}
			} else if !errors.Is(err, ErrIncomplete) {
				t.Fatalf("refresh = %v", err)
			}
			if _, err := grokManager(spool, home, nil).Open(first.BundleID); err != nil {
				t.Fatalf("lost verified bundle: %v", err)
			}
		})
	}
}

func TestGrokUsageAndChildOnlyRefresh(t *testing.T) {
	home, group, selection := grokFixture(t)
	spool := t.TempDir()
	manager := grokManager(spool, home, nil)
	first, err := manager.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"root", "grandchild"} {
		writeGrokInput(t, filepath.Join(group, id, "usage.json"), []byte(`{"session":{"inputTokens":21,"costIsPartial":true,"costUsdTicks":999}}`))
		next, err := manager.Prepare(t.Context(), selection)
		if err != nil {
			t.Fatal(err)
		}
		if next.BundleID == first.BundleID {
			t.Fatalf("%s update did not change bundle", id)
		}
		used := grokRecords(t, spool, next)[id].Session.UnattributedTokens
		if used == nil || used.InputTokens != 21 || used.CostMicroUSD != nil {
			t.Fatalf("usage = %#v", used)
		}
		first = next
	}
	if err := os.Remove(filepath.Join(group, "root", "usage.json")); err != nil {
		t.Fatal(err)
	}
	missing, err := manager.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if grokRecords(t, spool, missing)["root"].Session.UnattributedTokens != nil {
		t.Fatal("missing usage became known")
	}
}

func TestGrokRejectsAmbiguousOrUnsafeFamily(t *testing.T) {
	for _, scenario := range []string{"duplicate", "conflicting parent", "missing child", "unsafe id", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			home, group, selection := grokFixture(t)
			switch scenario {
			case "duplicate":
				data, err := os.ReadFile(filepath.Join(group, "root", "summary.json"))
				if err != nil {
					t.Fatal(err)
				}
				writeGrokInput(t, filepath.Join(filepath.Dir(group), "other", "duplicate", "summary.json"), data)
			case "conflicting parent":
				writeGrokInput(t, filepath.Join(group, "fork", "subagents", "child", "meta.json"), []byte(`{"child_session_id":"child"}`))
			case "missing child":
				for _, id := range []string{"child", "grandchild"} {
					if err := os.RemoveAll(filepath.Join(group, id)); err != nil {
						t.Fatal(err)
					}
				}
			case "unsafe id":
				writeGrokInput(t, filepath.Join(group, "root", "summary.json"), []byte(`{"info":{"id":"../escape"},"chat_format_version":1}`))
				selection.SessionID = "../escape"
			case "symlink":
				if runtime.GOOS == "windows" {
					t.Skip("symlink creation requires host privileges")
				}
				target := filepath.Join(home, "outside.json")
				writeGrokInput(t, target, []byte(`{}`))
				if err := os.Symlink(target, filepath.Join(group, "root", "signals.json")); err != nil {
					t.Fatal(err)
				}
			}
			spool := t.TempDir()
			if _, err := grokManager(spool, home, nil).Prepare(t.Context(), selection); !errors.Is(err, ErrIncomplete) {
				t.Fatalf("%s = %v", scenario, err)
			}
			entries, err := os.ReadDir(spool)
			if err != nil || len(entries) != 0 {
				t.Fatalf("partial spool: %v %v", entries, err)
			}
		})
	}
}

func TestGrokSSHAndPiRemainUnsupported(t *testing.T) {
	for _, selection := range []Selection{
		{SourceKind: sessionbackupv1.SourceSSH, SourceID: "remote", Agent: vendors.AgentGrok, SessionID: "root"},
		{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: "pi", SessionID: "root"},
	} {
		manager := New(Options{Root: t.TempDir(), OpenSource: func(context.Context, Selection) (SourceHandle, error) {
			t.Fatal("unsupported source opened")
			return SourceHandle{}, nil
		}})
		_, err := manager.Prepare(t.Context(), selection)
		var failure *PreparationError
		if !errors.As(err, &failure) || failure.Coverage.Problems[0].Code != sessionbackupv1.ProblemUnsupported {
			t.Fatalf("unsupported = %v", err)
		}
	}
}

func TestGrokExistingVendorFixtures(t *testing.T) {
	for _, fixture := range []string{"finished", "open", "commands", "plan", "compacted"} {
		t.Run(fixture, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("GROK_HOME", home)
			dir := filepath.Join(home, "sessions", "fixture", "member")
			source := filepath.Join("..", "vendors", "grok", "testdata", fixture)
			if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				relative, err := filepath.Rel(source, path)
				if err != nil {
					return err
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				writeGrokInput(t, filepath.Join(dir, relative), data)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if fixture == "plan" || fixture == "compacted" {
				data, err := os.ReadFile(filepath.Join("..", "vendors", "grok", "testdata", "commands", "updates.jsonl"))
				if err != nil {
					t.Fatal(err)
				}
				writeGrokInput(t, filepath.Join(dir, "updates.jsonl"), data)
			}
			data, err := os.ReadFile(filepath.Join(dir, "summary.json"))
			if err != nil {
				t.Fatal(err)
			}
			if fixture == "commands" || fixture == "plan" || fixture == "compacted" {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(data, &fields); err != nil {
					t.Fatal(err)
				}
				fields["created_at"] = json.RawMessage(`"2026-10-01T12:00:00Z"`)
				fields["updated_at"] = json.RawMessage(`"2026-10-01T12:01:00Z"`)
				data, err = json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				writeGrokInput(t, filepath.Join(dir, "summary.json"), data)
			}
			var summary struct {
				Info struct {
					ID string `json:"id"`
				} `json:"info"`
			}
			if err := json.Unmarshal(data, &summary); err != nil {
				t.Fatal(err)
			}
			spool := t.TempDir()
			prepared, err := grokManager(spool, home, nil).Prepare(t.Context(), Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentGrok, SessionID: summary.Info.ID})
			if err != nil {
				t.Fatal(err)
			}
			records := grokRecords(t, spool, prepared)
			record := records[summary.Info.ID]
			if len(records) != 1 {
				t.Fatalf("records = %d", len(records))
			}
			if fixture == "open" && (record.Session.UnattributedTokens != nil || len(record.Session.Usage) != 0 || record.Session.CostMicroUSD != nil) {
				t.Fatal("open usage became known")
			}
			for _, artifact := range prepared.Manifest.Artifacts {
				if artifact.SourceKey == "chat_history.jsonl" {
					t.Fatal("redundant conversation copied")
				}
			}
		})
	}
}

func TestGrokSynthesisIsRevisionBound(t *testing.T) {
	home, _, selection := grokFixture(t)
	spool := t.TempDir()
	manager := grokManager(spool, home, nil)
	baseline, err := manager.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	original := grokRecords(t, spool, baseline)["root"]
	for _, delta := range []int64{-1, 0} {
		manager.synthesis = synthesisStore{records: map[string]synthesis.Record{"root": {
			Revision: original.Session.LastActivityAtMs + delta, Model: "fixture", GeneratedAt: 1,
			Synthesis: session.SessionSynthesis{Goals: []string{"capture"}, Outcome: "done", KeyDecisions: []string{}, NextStep: "verify"},
		}}}
		prepared, err := manager.Prepare(t.Context(), selection)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, artifact := range prepared.Manifest.Artifacts {
			if artifact.Kind == sessionbackupv1.KindSynthesis {
				found = true
			}
		}
		if found != (delta == 0) {
			t.Fatalf("synthesis delta %d present=%t", delta, found)
		}
		if got := grokRecords(t, spool, prepared)["root"]; got.RevisionID != original.RevisionID {
			t.Fatal("synthesis changed parsed revision")
		}
	}
}

func TestGrokCaptureFreezesExternalNonChildProof(t *testing.T) {
	home, group, selection := grokFixture(t)
	writeGrokInput(t, filepath.Join(group, "root", "subagents", "fork", "meta.json"), []byte(`{"child_session_id":"fork"}`))
	spool := t.TempDir()
	manager := grokManager(spool, home, nil)
	first, err := manager.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Manifest.Members) != 3 {
		t.Fatal("fork imported into family")
	}
	source, err := os.ReadFile(filepath.Join(group, "fork", "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, artifact := range first.Manifest.Artifacts {
		if artifact.MemberID == "fork" {
			t.Fatal("fork became a family member")
		}
		if artifact.SourceKey != "non-children/fork/summary.json" {
			continue
		}
		frozen, err := os.ReadFile(filepath.Join(spool, first.BundleID, filepath.FromSlash(artifact.LogicalName)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(source, frozen) || artifact.Kind != sessionbackupv1.KindRawSidecar {
			t.Fatal("external summary proof lost")
		}
		found = true
	}
	if !found {
		t.Fatal("external classification proof omitted")
	}
	writeGrokInput(t, filepath.Join(group, "fork", "usage.json"), []byte(`{"session":{"inputTokens":999}}`))
	same, err := manager.Prepare(t.Context(), selection)
	if err != nil || same.BundleID != first.BundleID {
		t.Fatalf("unconsumed fork usage changed family: %v", err)
	}
	manager.afterRawCopy = func() {
		writeGrokInput(t, filepath.Join(group, "fork", "summary.json"), bytes.Replace(source, []byte(`"session_kind":"fork"`), []byte(`"session_kind":"subagent"`), 1))
	}
	if _, err := manager.Prepare(t.Context(), selection); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("changing classification accepted: %v", err)
	}
	if _, err := grokManager(spool, home, nil).Open(first.BundleID); err != nil {
		t.Fatal(err)
	}
}

func TestGrokAttributedCostPreservesUnknownAndKnownZero(t *testing.T) {
	for _, tc := range []struct {
		name, model, usage string
		input              int
		unknown, positive  bool
	}{
		{"partial", "grok-4.7", `{"session":{"inputTokens":12,"costUsdTicks":10000000000,"costIsPartial":true,"modelUsage":{"grok-4.7":{"inputTokens":12,"costUsdTicks":10000000000,"costIsPartial":true}}}}`, 12, true, false},
		{"incomplete", "grok-4.7", `{"session":{"inputTokens":12,"usageIsIncomplete":true,"modelUsage":{"grok-4.7":{"inputTokens":12,"usageIsIncomplete":true}}}}`, 12, true, false},
		{"unpriced", "unpriced-synthetic-model", `{"session":{"inputTokens":12,"modelUsage":{"unpriced-synthetic-model":{"inputTokens":12}}}}`, 12, true, false},
		{"source zero", "unpriced-synthetic-model", `{"session":{"inputTokens":12,"costUsdTicks":0,"modelUsage":{"unpriced-synthetic-model":{"inputTokens":12,"costUsdTicks":0}}}}`, 12, false, false},
		{"priced", "gpt-5", `{"session":{"inputTokens":12,"modelUsage":{"gpt-5":{"inputTokens":12}}}}`, 12, false, true},
		{"priced zero", "gpt-5", `{"session":{"modelUsage":{"gpt-5":{"inputTokens":0}}}}`, 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, group, selection := grokFixture(t)
			for _, id := range []string{"root", "child"} {
				writeGrokInput(t, filepath.Join(group, id, "usage.json"), []byte(tc.usage))
			}
			spool := t.TempDir()
			prepared, err := grokManager(spool, home, nil).Prepare(t.Context(), selection)
			if err != nil {
				t.Fatal(err)
			}
			records := grokRecords(t, spool, prepared)
			for _, id := range []string{"root", "child"} {
				record := records[id]
				if len(record.Session.Usage) != 1 || record.Session.Usage[0].Model != tc.model || record.Session.Usage[0].InputTokens != tc.input || record.Session.UnattributedTokens != nil {
					t.Fatalf("%s attributed usage lost: %+v", id, record.Session)
				}
				cost := record.Session.CostMicroUSD
				if tc.unknown {
					if cost != nil || !reflect.DeepEqual(record.Session.UnpricedModels, []string{tc.model}) {
						t.Fatalf("%s cost=%v unpriced=%v", id, cost, record.Session.UnpricedModels)
					}
				} else if cost == nil || (*cost > 0) != tc.positive || len(record.Session.UnpricedModels) != 0 {
					t.Fatalf("%s known cost=%v unpriced=%v", id, cost, record.Session.UnpricedModels)
				}
			}
			children := records["root"].Session.Subagents
			if len(children) != 1 || children[0].ID != "child" {
				t.Fatalf("children=%+v", children)
			}
			cost := children[0].CostMicroUSD
			if tc.unknown {
				if cost != nil {
					t.Fatalf("child projection cost=%v", cost)
				}
			} else if cost == nil || (*cost > 0) != tc.positive {
				t.Fatalf("child projection known cost=%v", cost)
			}
		})
	}
}

func TestGrokCaptureKeepsVerifiedFamilyDuringUnrelatedSummaryFailure(t *testing.T) {
	for _, state := range []string{"truncated", "missing"} {
		t.Run(state, func(t *testing.T) {
			home, group, selection := grokFixture(t)
			spool := t.TempDir()
			manager := grokManager(spool, home, nil)
			first, err := manager.Prepare(t.Context(), selection)
			if err != nil {
				t.Fatal(err)
			}
			unrelated := filepath.Join(group, "unrelated")
			if err := os.MkdirAll(unrelated, 0o700); err != nil {
				t.Fatal(err)
			}
			if state == "truncated" {
				writeGrokInput(t, filepath.Join(unrelated, "summary.json"), []byte(`{`))
			}
			selected := selection
			selected.SessionID = "grandchild"
			unchanged, err := manager.Prepare(t.Context(), selected)
			if err != nil || unchanged.BundleID != first.BundleID {
				t.Fatalf("verified family blocked: %v", err)
			}
			writeGrokInput(t, filepath.Join(group, "child", "usage.json"), []byte(`{"session":{"inputTokens":31}}`))
			updated, err := manager.Prepare(t.Context(), selected)
			if err != nil {
				t.Fatal(err)
			}
			used := grokRecords(t, spool, updated)["child"].Session.UnattributedTokens
			if updated.BundleID == first.BundleID || len(updated.Manifest.Members) != 3 || used == nil || used.InputTokens != 31 {
				t.Fatal("verified family update lost")
			}
			for _, id := range []string{"root", "child"} {
				t.Run(id, func(t *testing.T) {
					file := filepath.Join(group, id, "summary.json")
					original, err := os.ReadFile(file)
					if err != nil {
						t.Fatal(err)
					}
					writeGrokInput(t, file, []byte(`{`))
					if _, err := manager.Prepare(t.Context(), selection); !errors.Is(err, ErrIncomplete) {
						t.Fatalf("malformed selected member accepted: %v", err)
					}
					writeGrokInput(t, file, original)
				})
			}
			manager.afterRawCopy = func() { writeGrokInput(t, filepath.Join(group, "child", "summary.json"), []byte(`{`)) }
			if _, err := manager.Prepare(t.Context(), selection); !errors.Is(err, ErrIncomplete) {
				t.Fatalf("changing selected member accepted: %v", err)
			}
			if _, err := manager.Open(updated.BundleID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func grokMissingMetaFixture(t *testing.T) (string, string, Selection) {
	t.Helper()
	home, group, selection := grokFixture(t)
	if err := os.Remove(filepath.Join(group, "root", "subagents", "child", "meta.json")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(group, "child", "summary.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var summary map[string]json.RawMessage
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	delete(summary, "parent_session_id")
	data, err = json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	writeGrokInput(t, path, data)
	path = filepath.Join(group, "root", "updates.jsonl")
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte(`{"params":{"sessionId":"root","update":{"sessionUpdate":"subagent_spawned","subagent_id":"child","parent_session_id":"root","child_session_id":"child","attempt_id":"one","prompt":"recover me"}}}`+"\n")...)
	writeGrokInput(t, path, data)
	return home, group, selection
}

// Detect planner entry without line-number or wall-clock assumptions.
type grokPlanningReadContext struct {
	context.Context
	cancel  context.CancelFunc
	entered bool
}

func (ctx *grokPlanningReadContext) Err() error {
	pc, _, _, _ := runtime.Caller(1)
	if strings.HasSuffix(runtime.FuncForPC(pc).Name(), ".readSubagentMetas") {
		ctx.entered = true
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestGrokRejectsOversizeBeforeSpawnPlanning(t *testing.T) {
	for _, id := range []string{"root", "fork"} {
		t.Run(id, func(t *testing.T) {
			home, group, selection := grokMissingMetaFixture(t)
			file, err := os.OpenFile(filepath.Join(group, id, "updates.jsonl"), os.O_RDWR, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			err = file.Truncate(sessionbackupv1.MaxArtifactBytes + 1)
			closeErr := file.Close()
			if err != nil {
				t.Fatal(err)
			}
			if closeErr != nil {
				t.Fatal(closeErr)
			}
			base, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx := &grokPlanningReadContext{Context: base, cancel: cancel}
			_, err = grokManager(t.TempDir(), home, nil).Prepare(ctx, selection)
			var failure *PreparationError
			if ctx.entered || !errors.As(err, &failure) || !failure.TooLarge || failure.Coverage.Problems[0].Kind != sessionbackupv1.KindRawTranscript {
				t.Fatalf("planner entered=%t failure=%+v err=%v", ctx.entered, failure, err)
			}
		})
	}
}

func TestGrokMissingMetadataStillRecoversSpawn(t *testing.T) {
	home, _, selection := grokMissingMetaFixture(t)
	spool := t.TempDir()
	prepared, err := grokManager(spool, home, nil).Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	records := grokRecords(t, spool, prepared)
	if len(records) != 3 || records["child"].ParentSessionID != "root" || records["grandchild"].ParentSessionID != "child" {
		t.Fatal("recovered family lost")
	}
	children := records["root"].Session.Subagents
	if len(children) != 1 || children[0].ID != "child" || children[0].Task != "recover me" {
		t.Fatalf("recovered spawn=%+v", children)
	}
}

func TestGrokMixedModelUsageDoesNotPublishPartialEstimate(t *testing.T) {
	for _, tc := range []struct {
		name, flags string
		knownZero   bool
	}{
		{"withheld", "", false},
		{"partial", `"costUsdTicks":10000000000,"costIsPartial":true,`, false},
		{"incomplete", `"costUsdTicks":10000000000,"usageIsIncomplete":true,`, false},
		{"source zero", `"costUsdTicks":0,`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, group, selection := grokFixture(t)
			usage := []byte(`{"session":{"inputTokens":24,` + tc.flags + `"modelUsage":{"gpt-5":{"inputTokens":12},"unpriced-synthetic-model":{"inputTokens":12}}}}`)
			for _, id := range []string{"root", "child"} {
				writeGrokInput(t, filepath.Join(group, id, "usage.json"), usage)
			}
			spool := t.TempDir()
			prepared, err := grokManager(spool, home, nil).Prepare(t.Context(), selection)
			if err != nil {
				t.Fatal(err)
			}
			records := grokRecords(t, spool, prepared)
			for _, id := range []string{"root", "child"} {
				record := records[id]
				models := map[string]int{}
				for _, used := range record.Session.Usage {
					models[used.Model] = used.InputTokens
				}
				if !reflect.DeepEqual(models, map[string]int{"gpt-5": 12, "unpriced-synthetic-model": 12}) || record.Session.UnattributedTokens != nil {
					t.Fatalf("%s usage=%+v", id, record.Session.Usage)
				}
				cost := record.Session.CostMicroUSD
				if tc.knownZero {
					if cost == nil || *cost != 0 || len(record.Session.UnpricedModels) != 0 {
						t.Fatalf("%s known zero=%v unpriced=%v", id, cost, record.Session.UnpricedModels)
					}
				} else {
					if cost != nil || !reflect.DeepEqual(record.Session.UnpricedModels, []string{"unpriced-synthetic-model"}) {
						t.Fatalf("%s partial aggregate=%v unpriced=%v", id, cost, record.Session.UnpricedModels)
					}
					if record.Session.Usage[0].Model != "gpt-5" || record.Session.Usage[0].CostMicroUSD <= 0 {
						t.Fatal("priced model estimate lost")
					}
				}
			}
			child := records["root"].Session.Subagents[0]
			if tc.knownZero {
				if child.CostMicroUSD == nil || *child.CostMicroUSD != 0 {
					t.Fatal("known-zero child projection lost")
				}
			} else if child.CostMicroUSD != nil {
				t.Fatal("partial child cost became known")
			}
		})
	}
}
