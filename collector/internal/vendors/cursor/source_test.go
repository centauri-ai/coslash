package cursor

import (
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestSelectCursorFilesKeepsRecentPathFamily(t *testing.T) {
	root := "11111111-1111-4111-8111-111111111111"
	child := "22222222-2222-4222-8222-222222222222"
	rootPath := filepath.Join("project", "agent-transcripts", root, root+".jsonl")
	childPath := filepath.Join("project", "agent-transcripts", root, "subagents", child+".jsonl")
	unrelatedPath := filepath.Join("project", "agent-transcripts", "33333333-3333-4333-8333-333333333333", "33333333-3333-4333-8333-333333333333.jsonl")

	source := newCursorSelectionSource(map[string]int64{
		rootPath:      1000,
		childPath:     3000,
		unrelatedPath: 1000,
	})
	got := selectCursorFilesSource(source, []string{rootPath, childPath, unrelatedPath}, 2000, vendors.EmptySessionMetadata())

	if !sameStrings(got, []string{rootPath, childPath}) {
		t.Fatalf("selected files = %#v, want complete recent path family", got)
	}
}

func TestSelectCursorFilesKeepsLiveSideStoreFamilyAtCap(t *testing.T) {
	root := "root"
	child := "child"
	rootPath := filepath.Join("project", "agent-transcripts", root, root+".jsonl")
	childPath := filepath.Join("other-project", "agent-transcripts", child, child+".jsonl")

	files := make([]string, 0, vendors.MaxCandidateFilesPerAgent)
	modifications := map[string]int64{rootPath: 1000, childPath: 1000}
	for i := 0; i < vendors.MaxCandidateFilesPerAgent-2; i++ {
		path := fmt.Sprintf("filler-%04d.jsonl", i)
		files = append(files, path)
		modifications[path] = 4000
	}
	files = append(files, rootPath, childPath)
	modifications[childPath] = 5000

	metadata := vendors.EmptySessionMetadata()
	metadata.Live[child] = "interactive"
	metadata.Relationships[child] = vendors.SessionRelationship{ParentID: root}
	got := selectCursorFilesSource(newCursorSelectionSource(modifications), files, 2000, metadata)

	if len(got) != vendors.MaxCandidateFilesPerAgent {
		t.Fatalf("selected %d files, want %d", len(got), vendors.MaxCandidateFilesPerAgent)
	}
	selected := map[string]bool{}
	for _, path := range got {
		selected[path] = true
	}
	if !selected[rootPath] || !selected[childPath] {
		t.Fatalf("side-store family was split at cap: root=%v child=%v", selected[rootPath], selected[childPath])
	}
}

func TestSelectCursorFilesSkipsOldUnrelatedTranscripts(t *testing.T) {
	oldPath := filepath.Join("project", "agent-transcripts", "old", "old.jsonl")
	recentPath := filepath.Join("project", "agent-transcripts", "recent", "recent.jsonl")
	source := newCursorSelectionSource(map[string]int64{oldPath: 1000, recentPath: 3000})

	got := selectCursorFilesSource(source, []string{oldPath, recentPath}, 2000, vendors.EmptySessionMetadata())

	if !sameStrings(got, []string{recentPath}) {
		t.Fatalf("selected files = %#v, want only recent transcript", got)
	}
}

type cursorSelectionSource struct {
	modifications map[string]int64
}

func newCursorSelectionSource(modifications map[string]int64) cursorSelectionSource {
	return cursorSelectionSource{modifications: modifications}
}

func (s cursorSelectionSource) Open(string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (s cursorSelectionSource) ReadDir(string) ([]fs.DirEntry, error) {
	return nil, fs.ErrNotExist
}

func (s cursorSelectionSource) Stat(path string) (fs.FileInfo, error) {
	modified, ok := s.modifications[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return cursorSelectionFileInfo{path: path, modified: modified}, nil
}

type cursorSelectionFileInfo struct {
	path     string
	modified int64
}

func (i cursorSelectionFileInfo) Name() string       { return filepath.Base(i.path) }
func (i cursorSelectionFileInfo) Size() int64        { return 0 }
func (i cursorSelectionFileInfo) Mode() fs.FileMode  { return 0 }
func (i cursorSelectionFileInfo) ModTime() time.Time { return time.UnixMilli(i.modified) }
func (i cursorSelectionFileInfo) IsDir() bool        { return false }
func (i cursorSelectionFileInfo) Sys() any           { return nil }

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
