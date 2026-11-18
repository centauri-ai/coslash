package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteReplacesFileAndLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	var protected string
	protect := func(tempPath string, file *os.File) error {
		protected = tempPath
		return nil
	}
	if err := Write(path, []byte("new"), 0o640, protect); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "new" {
		t.Fatalf("content = %q, %v", data, err)
	}
	if filepath.Dir(protected) != dir {
		t.Fatalf("protect saw %q, want a temporary file in %q", protected, dir)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
			t.Fatalf("mode = %v, %v", info.Mode().Perm(), err)
		}
	}
	assertOnlyEntry(t, dir, "state.json")
}

func TestWriteKeepsOldFileWhenProtectFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("protect failed")
	err := Write(path, []byte("new"), 0o600, func(string, *os.File) error { return failure })
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want %v", err, failure)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "old" {
		t.Fatalf("content = %q, %v", data, err)
	}
	assertOnlyEntry(t, dir, "state.json")
}

func assertOnlyEntry(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		t.Fatalf("directory entries = %v, want only %s", entries, name)
	}
}
