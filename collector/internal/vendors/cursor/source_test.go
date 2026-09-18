package cursor

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestCursorFamilyFilesSelectsOnlyRequestedFamily(t *testing.T) {
	rootID := "00000000-0000-4000-8000-000000000001"
	childID := "00000000-0000-4000-8000-000000000002"
	otherID := "00000000-0000-4000-8000-000000000003"
	root := filepath.Join("projects", "repo", "agent-transcripts", rootID, rootID+".jsonl")
	child := filepath.Join("projects", "repo", "agent-transcripts", rootID, "subagents", childID+".jsonl")
	other := filepath.Join("projects", "repo", "agent-transcripts", otherID, otherID+".jsonl")
	files := []string{root, child, other}

	for _, id := range []string{rootID, childID} {
		got := cursorFamilyFiles(files, id)
		if len(got) != 2 || got[0] != root || got[1] != child {
			t.Fatalf("cursorFamilyFiles(%q) = %v, want [%s %s]", id, got, root, child)
		}
	}
}

func TestSelectCursorFilesDoesNotCapAllHistory(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(tempFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(tempFile)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]string, vendors.MaxCandidateFilesPerAgent+1)
	for i := range files {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012x", i)
		files[i] = filepath.Join("agent-transcripts", id, id+".jsonl")
	}

	got := selectCursorFilesSource(statReadSource{info: info}, files, 0)
	if len(got) != len(files) {
		t.Fatalf("selected %d files, want all %d", len(got), len(files))
	}
}

func TestCursorSourceHealthCountsOnlyRootTranscripts(t *testing.T) {
	rootID := "00000000-0000-4000-8000-000000000001"
	childID := "00000000-0000-4000-8000-000000000002"
	scan := &vendors.SourceScan{Files: []string{
		filepath.Join("agent-transcripts", rootID, rootID+".jsonl"),
		filepath.Join("agent-transcripts", rootID, "subagents", childID+".jsonl"),
	}}

	health := cursorSourceHealth("/cursor", scan)
	if health.Sessions != 1 {
		t.Fatalf("sessions = %d, want 1", health.Sessions)
	}
}

func TestCursorSourceHealthDeduplicatesRootFragments(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	scan := &vendors.SourceScan{Files: []string{
		filepath.Join("projects", "one", "agent-transcripts", id, id+".jsonl"),
		filepath.Join("projects", "two", "agent-transcripts", id, id+".jsonl"),
	}}

	health := cursorSourceHealth("/cursor", scan)
	if health.Sessions != 1 {
		t.Fatalf("sessions = %d, want 1", health.Sessions)
	}
}

func TestSelectFamilyRejectsParentCycle(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	parsed := []*vendors.ParsedSession{{
		Session:  &session.Session{ID: id},
		ParentID: id,
	}}
	done := make(chan []*vendors.ParsedSession, 1)
	go func() { done <- selectFamily(parsed, id) }()

	select {
	case family := <-done:
		if family != nil {
			t.Fatalf("family = %v, want cycle rejected", family)
		}
	case <-time.After(time.Second):
		t.Fatal("selectFamily did not terminate for a self-parent cycle")
	}
}

func TestCursorPathIDsAreCanonical(t *testing.T) {
	upper := "00000000-0000-4000-8000-00000000ABCD"
	lower := "00000000-0000-4000-8000-00000000abcd"
	root := filepath.Join("agent-transcripts", upper, lower+".jsonl")
	child := filepath.Join("agent-transcripts", upper, "subagents", upper+".jsonl")

	if !IsTranscript(root) {
		t.Fatalf("mixed-case root path %q was rejected", root)
	}
	if got := IDFromPath(root); got != lower {
		t.Fatalf("IDFromPath() = %q, want %q", got, lower)
	}
	if got := ParentIDFromPath(child); got != lower {
		t.Fatalf("ParentIDFromPath() = %q, want %q", got, lower)
	}
}

type statReadSource struct{ info fs.FileInfo }

func (s statReadSource) Open(string) (io.ReadCloser, error) {
	return nil, errors.New("unexpected Open")
}

func (s statReadSource) ReadDir(string) ([]fs.DirEntry, error) {
	return nil, errors.New("unexpected ReadDir")
}

func (s statReadSource) Stat(string) (fs.FileInfo, error) { return s.info, nil }
