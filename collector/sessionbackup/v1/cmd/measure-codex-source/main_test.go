package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestUnreadableResultFails(t *testing.T) {
	if successful(report{UnreadableCount: 1}) {
		t.Fatal("measurement succeeded with unreadable input")
	}
}

func TestCodexRootsIncludeActiveAndArchivedTrees(t *testing.T) {
	active := filepath.Join("approved-home", ".codex", "sessions")
	want := []string{active, filepath.Join("approved-home", ".codex", "archived_sessions")}
	if got := codexRoots(active, ""); !reflect.DeepEqual(got, want) {
		t.Fatalf("roots = %v; want %v", got, want)
	}
}
