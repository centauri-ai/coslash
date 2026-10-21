package sessionbackupproducer

import (
	"bytes"
	"context"
	"errors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/grok"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestGrokSourceRevisionsCoverFamilyInputs(t *testing.T) {
	_, group, _ := grokFixture(t)
	revisions := func() map[string]string {
		t.Helper()
		got, failures, err := LocalGrokSourceRevisions(t.Context())
		if err != nil || len(failures) != 0 {
			t.Fatalf("source revisions: %v failures: %v", err, failures)
		}
		return got
	}
	first := revisions()
	if len(first) != 3 || first["root"] == "" || first["fork"] == "" || first["worktree"] == "" || first["child"] != "" {
		t.Fatalf("root revisions=%v", first)
	}
	if revisions()["root"] != first["root"] {
		t.Fatal("unchanged source hash changed")
	}
	usage := filepath.Join(group, "grandchild", "usage.json")
	info, err := os.Stat(usage)
	if err != nil {
		t.Fatal(err)
	}
	writeGrokInput(t, usage, []byte(`{"session":{"inputTokens":123}}`))
	// Keep discovery time unchanged: source revision must remain independent.
	if err := os.Chtimes(usage, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	second := revisions()
	if second["root"] == first["root"] || second["fork"] != first["fork"] {
		t.Fatal("grandchild change was omitted or crossed families")
	}
	optional := filepath.Join(group, "grandchild", "plan.md")
	writeGrokInput(t, optional, []byte("synthetic plan"))
	third := revisions()
	if third["root"] == second["root"] {
		t.Fatal("optional presence omitted")
	}
	if err := os.Remove(optional); err != nil {
		t.Fatal(err)
	}
	if revisions()["root"] != second["root"] {
		t.Fatal("removed optional input remained in revision")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := LocalGrokSourceRevisions(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel identity=%v", err)
	}
}

func TestGrokSourceRevisionSeesSameSizeRewrite(t *testing.T) {
	_, group, _ := grokFixture(t)
	first, failures, err := LocalGrokSourceRevisions(t.Context())
	if err != nil || len(failures) != 0 {
		t.Fatal(err)
	}
	path := filepath.Join(group, "grandchild", "usage.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(data, []byte(`"inputTokens":5`), []byte(`"inputTokens":6`), 1)
	if bytes.Equal(data, changed) || len(data) != len(changed) {
		t.Fatal("fixture did not rewrite same size")
	}
	writeGrokInput(t, path, changed)
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	second, failures, err := LocalGrokSourceRevisions(t.Context())
	if err != nil || len(failures) != 0 {
		t.Fatal(err)
	}
	if first["root"] == second["root"] {
		t.Fatal("same-size grandchild usage rewrite with preserved activity was missed")
	}
}

func TestGrokFamilySourceRevisionPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := GrokSourceRevision(ctx, grok.FamilyRef{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("family cancellation=%v", err)
	}
}

func TestGrokSourceRevisionPreservesPreparationClassification(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "oversize"}[oversized], func(t *testing.T) {
			home, group, selection := grokFixture(t)
			refs, err := grok.PlanFamiliesContext(t.Context(), []string{filepath.Join(group, "root"), filepath.Join(group, "child"), filepath.Join(group, "grandchild"), filepath.Join(group, "fork"), filepath.Join(group, "worktree")})
			if err != nil {
				t.Fatal(err)
			}
			var family grok.FamilyRef
			for _, ref := range refs {
				if ref.ID == "root" {
					family = ref
				}
			}
			path := filepath.Join(group, "grandchild", "updates.jsonl")
			if oversized {
				path = filepath.Join(group, "grandchild", "usage.json")
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				truncateErr := file.Truncate((64 << 20) + 1)
				if err := errors.Join(truncateErr, file.Close()); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			_, prepareErr := New(Options{Root: t.TempDir(), LocalHome: func() (string, error) { return home, nil }}).Prepare(t.Context(), selection)
			var expected *PreparationError
			if !errors.As(prepareErr, &expected) {
				t.Fatalf("prepare=%v", prepareErr)
			}
			check := func(err error) {
				t.Helper()
				var got *PreparationError
				if !errors.As(err, &got) || got.TooLarge != oversized || !reflect.DeepEqual(got.Coverage.Problems, expected.Coverage.Problems) {
					t.Fatalf("source classification=%+v want=%+v", err, expected)
				}
				if err.Error() != expected.Error() {
					t.Fatal("non-fixed error message")
				}
			}
			revision, err := GrokSourceRevision(t.Context(), family)
			if revision != "" {
				t.Fatal("failed source admitted")
			}
			check(err)
			revisions, failures, err := LocalGrokSourceRevisions(t.Context())
			if err != nil || len(revisions) != 2 || len(failures) != 1 {
				t.Fatalf("family isolation %v %v %v", revisions, failures, err)
			}
			check(failures["root"])
		})
	}
	ctx, cancel := context.WithDeadline(t.Context(), time.Unix(1, 0))
	defer cancel()
	if _, err := GrokSourceRevision(ctx, grok.FamilyRef{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline=%v", err)
	}
	if _, _, err := LocalGrokSourceRevisions(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("local deadline=%v", err)
	}
}
