package synthesis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestLookupLatestIgnoresPreviewRevisionDrift(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	cache := NewCache()
	want := session.SessionSynthesis{Outcome: "shipped"}
	if err := cache.Store("session", Record{Revision: 42, Synthesis: want}); err != nil {
		t.Fatal(err)
	}
	assertPrivateSynthesisPath(t, filepath.Dir(SummariesDir()), true)
	assertPrivateSynthesisPath(t, SummariesDir(), true)
	assertPrivateSynthesisPath(t, filepath.Join(SummariesDir(), "session.json"), false)
	if got := NewCache().LookupLatest("session"); got == nil || got.Outcome != want.Outcome {
		t.Fatalf("LookupLatest() = %#v", got)
	}
}

func TestCacheRejectsSessionIDPathTraversal(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("COSLASH_HOME", home)
	outside := filepath.Join(root, "outside.json")
	if err := os.WriteFile(outside, []byte("unchanged"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"../outside", `..\outside`, "nested/session", `nested\session`, ".", ""} {
		t.Run(strings.ReplaceAll(id, "/", "_"), func(t *testing.T) {
			if err := NewCache().Store(id, Record{Revision: 42}); err == nil {
				t.Fatalf("Store(%q) accepted traversal", id)
			}
			if _, err := NewCache().Load(id); err == nil {
				t.Fatalf("Load(%q) accepted traversal", id)
			}
		})
	}
	content, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "unchanged" {
		t.Fatalf("outside file changed: %q", content)
	}
}

func TestWriteSchemaFileIsPrivate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	path, err := writeSchemaFile()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != synthesisSchema {
		t.Fatal("schema file content does not match embedded schema")
	}
	assertPrivateSynthesisPath(t, home, true)
	assertPrivateSynthesisPath(t, SynthesisCwd(), true)
	assertPrivateSynthesisPath(t, path, false)
}
