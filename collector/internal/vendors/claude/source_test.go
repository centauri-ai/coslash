package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseFilesDeduplicatesRootsBySessionID(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	duplicateID := "11111111-1111-1111-1111-111111111111"
	otherID := "22222222-2222-2222-2222-222222222222"
	older := filepath.Join(root, "C--Users-old", duplicateID+".jsonl")
	newer := filepath.Join(root, "C--Users-new", duplicateID+".jsonl")
	other := filepath.Join(root, "C--Users-other", otherID+".jsonl")
	olderChild := filepath.Join(root, "C--Users-old", duplicateID, "subagents", "agent-child.jsonl")
	newerChild := filepath.Join(root, "C--Users-new", duplicateID, "subagents", "agent-child.jsonl")

	writeTranscript(t, older, duplicateID, "C:\\Users\\old")
	writeTranscript(t, newer, duplicateID, "C:\\Users\\new")
	writeTranscript(t, other, otherID, "C:\\Users\\other")
	writeTranscript(t, olderChild, "agent-child", "C:\\Users\\old")
	writeTranscript(t, newerChild, "agent-child", "C:\\Users\\new")
	setModifiedTime(t, older, 1_000)
	setModifiedTime(t, newer, 2_000)

	got := parseFiles([]string{older, newer, other, olderChild, newerChild})
	if len(got) != 4 {
		t.Fatalf("parsed sessions = %d, want 4", len(got))
	}

	byID := map[string][]string{}
	for _, item := range got {
		byID[item.Session.ID] = append(byID[item.Session.ID], item.LogPath)
		if item.Session.ID == duplicateID {
			if item.LogPath != newer || item.LogModifiedAtMs != 2_000 {
				t.Fatalf("duplicate winner = %q at %d, want %q at 2000",
					item.LogPath, item.LogModifiedAtMs, newer)
			}
		}
		if item.Session.ID == "agent-child" && item.ParentID != duplicateID {
			t.Fatalf("child parent = %q, want %q", item.ParentID, duplicateID)
		}
	}
	if len(byID[duplicateID]) != 1 || len(byID[otherID]) != 1 || len(byID["agent-child"]) != 2 {
		t.Fatalf("session paths = %#v, want one duplicate winner, other root, and both children", byID)
	}
	if byID[otherID][0] != other || byID["agent-child"][0] != olderChild ||
		byID["agent-child"][1] != newerChild {
		t.Fatalf("unchanged paths = %#v, want %q, %q, and %q",
			byID, other, olderChild, newerChild)
	}
}

func writeTranscript(t *testing.T, path, sessionID, cwd string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	row := fmt.Sprintf(
		`{"sessionId":%q,"uuid":"row-%s","cwd":%q,"timestamp":"2026-01-01T00:00:00Z","type":"user","message":{"content":"hello"}}`+"\n",
		sessionID, sessionID, cwd,
	)
	if err := os.WriteFile(path, []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setModifiedTime(t *testing.T, path string, milliseconds int64) {
	t.Helper()
	stamp := time.UnixMilli(milliseconds)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}
