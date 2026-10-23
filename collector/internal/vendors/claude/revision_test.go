package claude

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestLocalSourceRevisionIncludesRawOnlyAndSidecarChanges(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(ProjectsRoot(home), "project")
	id := "11111111-2222-3333-4444-555555555555"
	transcript := filepath.Join(root, id+".jsonl")
	child := filepath.Join(root, id, "subagents", "agent-one.jsonl")
	if err := os.MkdirAll(filepath.Dir(child), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{transcript, child} {
		if err := os.WriteFile(file, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	first, err := LocalSourceRevisions(t.Context(), vendors.LocalReadSource, home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte("{}\n{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := LocalSourceRevisions(t.Context(), vendors.LocalReadSource, home)
	if err != nil || first[id] == second[id] {
		t.Fatalf("raw-only revision did not change: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(child), "agent-one.meta.json"), []byte(`{"description":"added"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := LocalSourceRevisions(t.Context(), vendors.LocalReadSource, home)
	if err != nil || second[id] == third[id] {
		t.Fatalf("sidecar revision did not change: %v", err)
	}
	toolResult := filepath.Join(root, id, "tool-results", "big.txt")
	if err := os.MkdirAll(filepath.Dir(toolResult), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(toolResult, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	fourth, err := LocalSourceRevisions(t.Context(), vendors.LocalReadSource, home)
	if err != nil || third[id] == fourth[id] {
		t.Fatalf("tool-results file did not change the revision: %v", err)
	}
	// A dangling link in tool-results only wakes the queue; it must not fail every family.
	if err := os.Symlink(filepath.Join(root, "missing-target"), filepath.Join(filepath.Dir(toolResult), "loop.txt")); err == nil {
		if _, err := LocalSourceRevisions(t.Context(), vendors.LocalReadSource, home); err != nil {
			t.Fatalf("dangling tool-results entry failed the scan: %v", err)
		}
	}
}
