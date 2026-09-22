package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCursorExecutableOnlyFallsBackToAgentOwnedByCursor(t *testing.T) {
	root := t.TempDir()
	cursorBinary := filepath.Join(root, "cursor-agent", "versions", "1", "cursor-agent")
	otherBinary := filepath.Join(root, "grok", "agent")
	for _, path := range []string{cursorBinary, otherBinary} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name  string
		links map[string]string
		want  string
	}{
		{"prefers cursor-agent", map[string]string{"cursor-agent": cursorBinary, "agent": cursorBinary}, "cursor-agent"},
		{"accepts Cursor's agent", map[string]string{"agent": cursorBinary}, "agent"},
		{"rejects another agent", map[string]string{"agent": otherBinary}, "cursor-agent"},
		{"reports cursor-agent when missing", nil, "cursor-agent"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bin := t.TempDir()
			for name, target := range test.links {
				if err := os.Symlink(target, filepath.Join(bin, name)); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			if got := CursorExecutable(); got != test.want {
				t.Fatalf("CursorExecutable() = %q, want %q", got, test.want)
			}
		})
	}
}
