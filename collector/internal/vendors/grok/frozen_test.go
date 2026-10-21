package grok

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFrozenFamilyUsesOnlyFrozenInputsAndClassification(t *testing.T) {
	live := t.TempDir()
	t.Setenv("GROK_HOME", live)
	parent := filepath.Join(live, "parent")
	child := filepath.Join(live, "child")
	fork := filepath.Join(live, "fork")
	writeSummary(t, parent, `{"info":{"id":"parent"},"chat_format_version":1}`)
	writeSummary(t, child, `{"info":{"id":"child"},"chat_format_version":1,"session_kind":"subagent"}`)
	writeSummary(t, fork, `{"info":{"id":"fork"},"chat_format_version":1,"session_kind":"fork","parent_session_id":"parent"}`)
	for _, id := range []string{"child", "fork"} {
		dir := filepath.Join(parent, "subagents", id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"child_session_id":"`+id+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	refs, err := PlanFamiliesContext(t.Context(), []string{parent, child, fork})
	if err != nil {
		t.Fatal(err)
	}
	var family FamilyRef
	for _, ref := range refs {
		if ref.ID == "parent" {
			family = ref
		}
	}
	frozen := t.TempDir()
	frozenDirs := []string{filepath.Join(frozen, "parent"), filepath.Join(frozen, "child")}
	for i, dir := range family.Dirs {
		if err := os.CopyFS(frozenDirs[i], os.DirFS(dir)); err != nil {
			t.Fatal(err)
		}
	}
	proof := filepath.Join(frozen, "proof")
	if err := os.CopyFS(proof, os.DirFS(fork)); err != nil {
		t.Fatal(err)
	}
	if got := family.NonChildSummaryPaths(); !reflect.DeepEqual(got, []string{filepath.Join(fork, "summary.json")}) {
		t.Fatalf("proof paths=%v", got)
	}
	if err := os.RemoveAll(live); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseFrozenFamilyContext(t.Context(), family, frozenDirs, []string{filepath.Join(proof, "summary.json")})
	if err != nil || len(parsed) != 2 || parsed[0].Session.ID != "parent" || parsed[1].ParentID != "parent" {
		t.Fatalf("frozen family=%+v %v", parsed, err)
	}
	if _, err := ParseFrozenFamilyContext(t.Context(), family, frozenDirs, nil); err == nil {
		t.Fatal("missing non-child proof accepted")
	}
	writeSummary(t, proof, `{"info":{"id":"fork"},"chat_format_version":1,"session_kind":"subagent"}`)
	if _, err := ParseFrozenFamilyContext(t.Context(), family, frozenDirs, []string{filepath.Join(proof, "summary.json")}); err == nil {
		t.Fatal("child impersonated frozen fork")
	}
	if _, err := ParseFrozenFamilyContext(t.Context(), FamilyRef{ID: family.ID, Dirs: family.Dirs}, frozenDirs, nil); err == nil {
		t.Fatal("fabricated family accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ParseFrozenFamilyContext(ctx, family, frozenDirs, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}

func TestFrozenFamilyRejectsMalformedOptionalInput(t *testing.T) {
	for _, input := range []struct{ name, body string }{
		{"usage.json", `{"session":{"inputTokens":"bad"}}`},
		{"signals.json", `{"turnCount":"bad"}`},
		{"plan.json", `{"todos":[]}`},
		{"plan_mode.json", `{"awaiting_plan_approval":[]}`},
		{"prompt_context.json", `{"is_non_interactive":[]}`},
		{"goal/state.json", `{"objective":[]}`},
		{"compaction_checkpoints/one.json", `{"created_at":[]}`},
		{"subagents/one/meta.json", `{"child_session_id":[]}`},
	} {
		t.Run(input.name, func(t *testing.T) {
			dir := t.TempDir()
			writeSummary(t, dir, `{"info":{"id":"root"},"chat_format_version":1}`)
			refs, err := PlanFamiliesContext(t.Context(), []string{dir})
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, filepath.FromSlash(input.name))
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(input.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ParseFrozenFamilyContext(t.Context(), refs[0], []string{dir}, nil); err == nil {
				t.Fatal("malformed optional input accepted")
			}
		})
	}
}
