package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestLocalSSHMirrorIDsReturnsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := localSSHMirrorIDs(ctx, t.TempDir(), []string{"session.jsonl"})
	if err != context.Canceled {
		t.Fatalf("mirror ID scan error = %v, want %v", err, context.Canceled)
	}
}

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

func TestGetSessionFactsUsesNewestDuplicate(t *testing.T) {
	home := t.TempDir()
	setClaudeTestHome(t, home)

	id := "33333333-3333-3333-3333-333333333333"
	older := filepath.Join(ProjectsRoot(home), "a-old", id+".jsonl")
	newer := filepath.Join(ProjectsRoot(home), "z-new", id+".jsonl")
	writeTranscript(t, older, id, "C:\\Users\\old")
	writeTranscript(t, newer, id, "C:\\Users\\new")
	setModifiedTime(t, older, 1_000)
	setModifiedTime(t, newer, 2_000)

	got, err := GetSessionFacts(id)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.LogPath != newer || got.LogModifiedAtMs != 2_000 {
		t.Fatalf("session facts = %#v, want %q at 2000", got, newer)
	}
}

func TestSessionFactsLoaderUsesNewestDuplicate(t *testing.T) {
	home := t.TempDir()
	setClaudeTestHome(t, home)

	id := "55555555-5555-5555-5555-555555555555"
	older := filepath.Join(ProjectsRoot(home), "a-old", id+".jsonl")
	newer := filepath.Join(ProjectsRoot(home), "z-new", id+".jsonl")
	writeTranscript(t, older, id, "C:\\Users\\old")
	writeTranscript(t, newer, id, "C:\\Users\\new")
	setModifiedTime(t, older, 1_000)
	setModifiedTime(t, newer, 2_000)

	load, err := NewSessionFactsLoader()
	if err != nil {
		t.Fatal(err)
	}
	got, err := load(id)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.LogPath != newer || got.LogModifiedAtMs != 2_000 {
		t.Fatalf("session facts = %#v, want %q at 2000", got, newer)
	}
}

func TestParseFilesDuplicateTieUsesLexicalPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := "44444444-4444-4444-4444-444444444444"
	first := filepath.Join(root, "a-project", id+".jsonl")
	second := filepath.Join(root, "z-project", id+".jsonl")
	writeTranscript(t, first, id, "C:\\Users\\first")
	writeTranscript(t, second, id, "C:\\Users\\second")
	setModifiedTime(t, first, 1_000)
	setModifiedTime(t, second, 1_000)

	for _, files := range [][]string{{first, second}, {second, first}} {
		got := parseFiles(files)
		if len(got) != 1 || got[0].LogPath != first {
			t.Fatalf("equal-mtime winner for %q = %#v, want %q", files, got, first)
		}
	}
}

func TestCollectMarksOnlyDesktopSSHMirrorRoots(t *testing.T) {
	home := t.TempDir()
	setClaudeTestHome(t, home)
	root := ProjectsRoot(home)
	validID := "66666666-6666-4666-8666-666666666666"
	ordinaryID := "77777777-7777-4777-8777-777777777777"
	looseID := "88888888-8888-4888-8888-888888888888"
	mismatchID := "99999999-9999-4999-8999-999999999999"

	valid := filepath.Join(root, "ssh-"+validID, validID+".jsonl")
	ordinary := filepath.Join(root, "project", ordinaryID+".jsonl")
	loose := filepath.Join(root, "ssh-not-a-uuid", looseID+".jsonl")
	mismatch := filepath.Join(root, "ssh-"+validID, mismatchID+".jsonl")
	child := filepath.Join(root, "ssh-"+validID, validID, "subagents", "agent-child.jsonl")
	for _, test := range []struct {
		path      string
		sessionID string
	}{
		{path: valid, sessionID: validID},
		{path: ordinary, sessionID: ordinaryID},
		{path: loose, sessionID: looseID},
		{path: mismatch, sessionID: mismatchID},
		{path: child, sessionID: "agent-child"},
	} {
		writeTranscript(t, test.path, test.sessionID, "/workspace")
	}

	parsed, _, err := Collect(0)
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]*vendors.ParsedSession, len(parsed))
	for _, item := range parsed {
		byPath[item.LogPath] = item
	}
	for _, test := range []struct {
		path string
		want bool
	}{
		{path: valid, want: true},
		{path: ordinary},
		{path: loose},
		{path: mismatch},
		{path: child},
	} {
		item := byPath[test.path]
		if item == nil || item.Session.LocalSSHMirror != test.want {
			t.Fatalf("%s mirror marker = %v, want %v; parsed=%#v", test.path, item != nil && item.Session.LocalSSHMirror, test.want, item)
		}
	}
}

func TestCollectPreservesDesktopSSHMirrorIdentityAcrossDuplicateRoots(t *testing.T) {
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	for _, test := range []struct {
		name             string
		mirrorModified   int64
		ordinaryModified int64
	}{
		{name: "newer ordinary root", mirrorModified: 1_000, ordinaryModified: 2_000},
		{name: "ordinary root wins tie", mirrorModified: 1_000, ordinaryModified: 1_000},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			setClaudeTestHome(t, home)
			root := ProjectsRoot(home)
			mirror := filepath.Join(root, "ssh-"+id, id+".jsonl")
			ordinary := filepath.Join(root, "project", id+".jsonl")
			writeTranscript(t, mirror, id, "/workspace")
			writeTranscript(t, ordinary, id, "/workspace")
			setModifiedTime(t, mirror, test.mirrorModified)
			setModifiedTime(t, ordinary, test.ordinaryModified)

			parsed, _, err := Collect(0)
			if err != nil {
				t.Fatal(err)
			}
			if len(parsed) != 1 || parsed[0].LogPath != ordinary {
				t.Fatalf("parsed roots = %#v, want ordinary root %q", parsed, ordinary)
			}
			if !parsed[0].Session.LocalSSHMirror {
				t.Fatal("deduplicated ordinary root lost its SSH mirror identity")
			}
		})
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
