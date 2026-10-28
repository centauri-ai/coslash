package pi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func captureTranscript(t *testing.T, id, parent string, rows ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "unrelated filename.jsonl")
	h, err := json.Marshal(header{Type: "session", Version: 3, ID: id, CWD: filepath.Dir(path), Timestamp: "2026-01-01T00:00:00Z", ParentSession: parent})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(string(h)+"\n"+strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return canonicalPath(path)
}

func captureEnvironment(t *testing.T, roots ...string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_PI_SESSION_ROOTS", strings.Join(roots, string(os.PathListSeparator)))
	t.Setenv("COSLASH_HOME", filepath.Join(home, "coslash"))
	t.Chdir(home)
}

func freezeInputs(t *testing.T, inputs SessionInputs) map[string]string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{}
	for i, path := range inputs.Files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		frozen := filepath.Join(dir, fmt.Sprintf("%d.jsonl", i))
		if err := os.WriteFile(frozen, data, 0o600); err != nil {
			t.Fatal(err)
		}
		files[path] = frozen
	}
	return files
}

func TestSourcePlanUsesHeaderIdentityAndRejectsDuplicates(t *testing.T) {
	path := captureTranscript(t, "custom.session_01", "")
	captureEnvironment(t, filepath.Dir(path), filepath.Dir(path))
	plan, err := PlanSourcesContext(t.Context())
	if err != nil || !reflect.DeepEqual(plan.Sources, []SourceRef{{ID: "custom.session_01", Path: path}}) || len(plan.Scan.Files) != 1 {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "duplicate.jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err = PlanSourcesContext(t.Context())
	if err != nil || len(plan.Sources) != 0 || plan.Scan.SkippedTotal != 2 {
		t.Fatalf("ambiguous plan = %+v, %v", plan, err)
	}
	if _, err := plan.SessionInputsContext(t.Context(), "custom.session_01"); err == nil {
		t.Fatal("ambiguous identity selected")
	}
}

func TestFrozenSessionKeepsExternalAncestorsWithoutLiveReads(t *testing.T) {
	r := userRow("r", "", "shared", "2026-01-01T00:00:01Z")
	a, b := assistantRow("a", "r", "grandparent", 6), assistantRow("b", "a", "parent", 2)
	grandparent := captureTranscript(t, "grandparent", "", r, a)
	parent := captureTranscript(t, "parent", grandparent, r, a, b)
	fork := captureTranscript(t, "fork", parent, r, a, b, assistantRow("c", "b", "novel", 3))
	captureEnvironment(t, filepath.Dir(fork))
	plan, err := PlanSourcesContext(t.Context())
	if err != nil || len(plan.Sources) != 1 {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	inputs, err := plan.SessionInputsContext(t.Context(), "fork")
	if err != nil || !reflect.DeepEqual(inputs.Files, []string{fork, parent, grandparent}) {
		t.Fatalf("inputs = %+v, %v", inputs, err)
	}
	files := freezeInputs(t, inputs)
	for _, path := range inputs.Files {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	parsed, selection, err := ParseFrozenSessionContext(WithRuntimeSnapshot(t.Context(), nil), inputs, files)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ParentID != "" || len(parsed.Session.Subagents) != 0 || parsed.Session.TokensUnavailable || parsed.Session.DetailsIncomplete || parsed.RecordedCost == nil || *parsed.RecordedCost != 3 {
		t.Fatalf("frozen fork = %+v", parsed)
	}
	if selection != (ContextSelection{LeafID: "c"}) || parsed.Session.Digest[0].Active != nil || parsed.Session.Digest[0].Inherited == nil || !*parsed.Session.Digest[0].Inherited {
		t.Fatalf("frozen selection = %+v, digest = %+v", selection, parsed.Session.Digest)
	}
}

func TestFrozenSessionPreservesUnavailableProvenanceAndTail(t *testing.T) {
	for _, tail := range []bool{false, true} {
		t.Run(fmt.Sprint(tail), func(t *testing.T) {
			parent := ""
			if !tail {
				parent = filepath.Join(t.TempDir(), "missing.jsonl")
			}
			path := captureTranscript(t, "root", parent, assistantRow("a", "", "useful", 3))
			if tail {
				f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := f.WriteString(`{"type":"message"`)
				closeErr := f.Close()
				if writeErr != nil || closeErr != nil {
					t.Fatal(writeErr, closeErr)
				}
			}
			captureEnvironment(t, filepath.Dir(path))
			plan, err := PlanSourcesContext(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			inputs, err := plan.SessionInputsContext(t.Context(), "root")
			if err != nil {
				t.Fatal(err)
			}
			parsed, _, err := ParseFrozenSessionContext(t.Context(), inputs, freezeInputs(t, inputs))
			if err != nil || parsed == nil || !parsed.Session.TokensUnavailable || !parsed.Session.CostUnavailable || !parsed.Session.DetailsIncomplete || parsed.RecordedCost != nil || parsed.ParentID != "" {
				t.Fatalf("incomplete root = %+v, %v", parsed, err)
			}
		})
	}
}

func TestFrozenSessionRejectsMissingCopiesAndChangedHeaders(t *testing.T) {
	parent := captureTranscript(t, "parent", "", assistantRow("a", "", "copied", 2))
	path := captureTranscript(t, "fork", parent, assistantRow("a", "", "copied", 2))
	captureEnvironment(t, filepath.Dir(path))
	plan, err := PlanSourcesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := plan.SessionInputsContext(t.Context(), "fork")
	if err != nil {
		t.Fatal(err)
	}
	files := freezeInputs(t, inputs)
	delete(files, parent)
	if _, _, err := ParseFrozenSessionContext(t.Context(), inputs, files); err == nil {
		t.Fatal("missing frozen parent fell back to live evidence")
	}
	files = freezeInputs(t, inputs)
	data, err := os.ReadFile(files[path])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files[path], []byte(strings.Replace(string(data), `"id":"fork"`, `"id":"other"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ParseFrozenSessionContext(t.Context(), inputs, files); err == nil {
		t.Fatal("changed frozen identity accepted")
	}
}

func TestFrozenSessionUsesSnapshotLeafAndNilActiveFallback(t *testing.T) {
	path := captureTranscript(t, "branches", "", userRow("r", "", "shared", "2026-01-01T00:00:01Z"), assistantRow("a", "r", "earlier", 2), assistantRow("b", "r", "latest", 3))
	captureEnvironment(t, filepath.Dir(path))
	for _, leaf := range []string{"a", "not-persisted", ""} {
		t.Run(leaf, func(t *testing.T) {
			owner := runtimeEvidence{Record: RuntimeRecord{RuntimeID: "synthetic", SessionID: "branches", TranscriptPath: path, LeafID: &leaf}}
			snapshot := &RuntimeSnapshot{owners: map[string][]runtimeEvidence{"branches\x00" + path: {owner}}, states: map[string]string{runtimeOwnerKey(owner): "live"}}
			plan, err := PlanSourcesContext(WithRuntimeSnapshot(t.Context(), snapshot))
			if err != nil {
				t.Fatal(err)
			}
			inputs, err := plan.SessionInputsContext(t.Context(), "branches")
			if err != nil {
				t.Fatal(err)
			}
			want := ContextSelection{LeafID: "b"}
			if leaf == "a" {
				want = ContextSelection{LeafID: "a", Verified: true}
			}
			leaf = "b"
			parsed, selection, err := ParseFrozenSessionContext(WithRuntimeSnapshot(t.Context(), nil), inputs, freezeInputs(t, inputs))
			if err != nil || selection != want {
				t.Fatalf("selection = %+v, %v; want %+v", selection, err, want)
			}
			for _, row := range parsed.Session.Digest {
				if (row.Active != nil) != want.Verified {
					t.Fatalf("active evidence = %+v", row)
				}
			}
		})
	}
}

func TestCaptureAPIsPreserveCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := PlanSourcesContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("plan cancellation = %v", err)
	}
	if _, err := (&SourcePlan{}).SessionInputsContext(ctx, "root"); !errors.Is(err, context.Canceled) {
		t.Fatalf("input cancellation = %v", err)
	}
	if _, _, err := ParseFrozenSessionContext(ctx, SessionInputs{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("parse cancellation = %v", err)
	}
}

func TestSourceInputsRejectConflictingAncestorIdentity(t *testing.T) {
	parent := captureTranscript(t, "parent", "", assistantRow("a", "", "copied", 2))
	fork := captureTranscript(t, "fork", parent, assistantRow("a", "", "copied", 2))
	duplicate := captureTranscript(t, "parent", "", assistantRow("b", "", "other", 9))
	captureEnvironment(t, filepath.Dir(parent), filepath.Dir(fork), filepath.Dir(duplicate))
	plan, err := PlanSourcesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := plan.SessionInputsContext(t.Context(), "fork")
	if err != nil || !reflect.DeepEqual(inputs.Files, []string{fork}) {
		t.Fatalf("ambiguous ancestor inputs = %+v, %v", inputs, err)
	}
	parsed, _, err := ParseFrozenSessionContext(t.Context(), inputs, freezeInputs(t, inputs))
	if err != nil || !parsed.Session.TokensUnavailable || parsed.RecordedCost != nil || !parsed.Session.DetailsIncomplete {
		t.Fatalf("ambiguous ancestry = %+v, %v", parsed, err)
	}
}

func TestSourceInputsBoundAncestorDepth(t *testing.T) {
	parent := ""
	for i := 257; i >= 0; i-- {
		parent = captureTranscript(t, fmt.Sprintf("session-%d", i), parent)
	}
	captureEnvironment(t, filepath.Dir(parent))
	plan, err := PlanSourcesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := plan.SessionInputsContext(t.Context(), "session-0")
	if err != nil || len(inputs.Files) != 257 {
		t.Fatalf("depth bound: %d inputs, %v", len(inputs.Files), err)
	}
	parsed, _, err := ParseFrozenSessionContext(t.Context(), inputs, freezeInputs(t, inputs))
	if err != nil || !parsed.Session.TokensUnavailable || !parsed.Session.DetailsIncomplete {
		t.Fatalf("unverifiable depth = %+v, %v", parsed, err)
	}
}

func TestFrozenSessionKeepsUsefulRootWithMalformedAncestorDetail(t *testing.T) {
	parent := captureTranscript(t, "parent", "", `{"malformed":`)
	path := captureTranscript(t, "fork", parent, assistantRow("a", "", "useful", 3))
	captureEnvironment(t, filepath.Dir(path))
	plan, err := PlanSourcesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := plan.SessionInputsContext(t.Context(), "fork")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := ParseFrozenSessionContext(t.Context(), inputs, freezeInputs(t, inputs))
	if err != nil || parsed == nil || !parsed.Session.TokensUnavailable || parsed.RecordedCost != nil || !parsed.Session.DetailsIncomplete || parsed.Session.Summary == nil || *parsed.Session.Summary != "useful" {
		t.Fatalf("unverifiable ancestor hid useful root = %+v, %v", parsed, err)
	}
	inputs, err = plan.SessionInputsContext(t.Context(), "fork")
	if err != nil {
		t.Fatal(err)
	}
	files := freezeInputs(t, inputs)
	data, err := os.ReadFile(files[path])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files[path], append(data, []byte("{invalid}\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ParseFrozenSessionContext(t.Context(), inputs, files); err == nil {
		t.Fatal("malformed selected root accepted")
	}
}
