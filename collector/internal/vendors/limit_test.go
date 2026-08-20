package vendors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLimitRootTranscriptsKeepsNewestRootsAndChildren(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, mtime int64) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		stamp := time.UnixMilli(mtime)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		return path
	}
	oldRoot := write("old.jsonl", 1000)
	newRoot := write("new.jsonl", 3000)
	midRoot := write("mid.jsonl", 2000)
	childOfNew := write("child-new.jsonl", 2500)
	childOfOld := write("child-old.jsonl", 1500)

	parentID := func(path string) string {
		switch filepath.Base(path) {
		case "child-new.jsonl":
			return "new"
		case "child-old.jsonl":
			return "old"
		default:
			return ""
		}
	}
	rootID := func(path string) string {
		return strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}

	limited, truncated := LimitRootTranscripts(
		[]string{oldRoot, newRoot, midRoot, childOfNew, childOfOld},
		2,
		parentID,
		rootID,
	)
	if !truncated {
		t.Fatal("expected truncation")
	}
	got := map[string]bool{}
	for _, path := range limited {
		got[filepath.Base(path)] = true
	}
	if !got["new.jsonl"] || !got["mid.jsonl"] || !got["child-new.jsonl"] {
		t.Fatalf("limited = %#v", got)
	}
	if got["old.jsonl"] || got["child-old.jsonl"] {
		t.Fatalf("old root/child leaked: %#v", got)
	}
}
