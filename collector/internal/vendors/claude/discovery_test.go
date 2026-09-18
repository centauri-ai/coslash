package claude

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestFilesSinceIncludesBackgroundRehomePredecessorFamily(t *testing.T) {
	project := t.TempDir()
	oldRoot := filepath.Join(project, "old.jsonl")
	oldChild := filepath.Join(project, "old", "subagents", "agent-child.jsonl")
	newRoot := filepath.Join(project, "new.jsonl")
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	shared := `{"sessionId":"old","uuid":"shared","timestamp":"2026-09-16T00:00:00Z","message":{"content":"hello"}}` + "\n"
	write(oldRoot, shared)
	write(oldChild, `{"sessionId":"child","uuid":"child-row","message":{"content":"work"}}`+"\n")
	write(newRoot, shared+`{"sessionId":"new","sessionKind":"bg","uuid":"new-row","timestamp":"2026-09-18T00:00:00Z","message":{"content":"continued"}}`+"\n")
	oldTime := time.Unix(1, 0)
	if err := os.Chtimes(oldRoot, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oldChild, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	files := []string{oldRoot, oldChild, newRoot}
	got := FilesSince(files, nil, time.Now().Add(-time.Hour).UnixMilli())
	want := familyFiles(vendors.LocalReadSource, files, "new")
	if !slices.Equal(got, want) {
		t.Fatalf("window family = %v, exact family = %v", got, want)
	}
}
