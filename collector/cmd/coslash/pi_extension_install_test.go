package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPiExtensionIsSkippedWhenPiIsAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", "")
	if err := ensurePiExtensionIfDetected(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi")); !os.IsNotExist(err) {
		t.Fatalf("Pi directory was created for an absent CLI: stat error=%v", err)
	}
}
