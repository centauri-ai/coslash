package cursor

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestResumeDirectoryFindsTheDirectoryTheChatWasFiledUnder(t *testing.T) {
	const id = "a7c28665-5abb-453d-9cf9-72e64c7a7b06"
	home := t.TempDir()
	t.Setenv("HOME", home)
	frontend := filepath.Join(home, "repo", "frontend")
	value := &session.Session{ID: id, WorkingDirectory: filepath.Join(frontend, "src")}

	if got := ResumeDirectory(value); got != value.WorkingDirectory {
		t.Fatalf("without a chat store, ResumeDirectory = %q, want %q", got, value.WorkingDirectory)
	}

	sum := md5.Sum([]byte(frontend))
	store := filepath.Join(home, ".cursor", "chats", hex.EncodeToString(sum[:]), id, "store.db")
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResumeDirectory(value); got != frontend {
		t.Fatalf("ResumeDirectory = %q, want the directory the CLI ran in %q", got, frontend)
	}

	value.WorkingDirectory = filepath.Join(home, "repo")
	value.FileEdits = []session.FileEdit{{Path: filepath.Join(frontend, "src", "app.ts")}}
	if got := ResumeDirectory(value); got != frontend {
		t.Fatalf("with a parent working directory, ResumeDirectory = %q, want %q from the edited files", got, frontend)
	}

	value.WorkingDirectory, value.FileEdits = filepath.Join(home, "elsewhere"), nil
	if got := ResumeDirectory(value); got != value.WorkingDirectory {
		t.Fatalf("with no matching ancestor, ResumeDirectory = %q, want %q", got, value.WorkingDirectory)
	}
}
