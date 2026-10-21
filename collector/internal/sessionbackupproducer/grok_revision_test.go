package sessionbackupproducer

import (
	"bytes"
	"context"
	"errors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/grok"
	"os"
	"path/filepath"
	"testing"
)

func TestGrokSourceRevisionsCoverFamilyInputs(t *testing.T) {
	_, group, _ := grokFixture(t)
	revisions := func() map[string]string {
		t.Helper()
		got, err := LocalGrokSourceRevisions(t.Context())
		if err != nil {
			t.Fatal(err)
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
	if _, err := LocalGrokSourceRevisions(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel identity=%v", err)
	}
}

func TestGrokSourceRevisionSeesSameSizeRewrite(t *testing.T) {
	_, group, _ := grokFixture(t)
	first, err := LocalGrokSourceRevisions(t.Context())
	if err != nil {
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
	second, err := LocalGrokSourceRevisions(t.Context())
	if err != nil {
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
