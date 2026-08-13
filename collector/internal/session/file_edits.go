package session

import "strings"

type FileChange struct {
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	Operation string `json:"operation"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

type FileEditSet struct {
	Edits []FileEdit
	index map[string]int
}

func NewFileEditSet() *FileEditSet {
	return &FileEditSet{Edits: []FileEdit{}, index: map[string]int{}}
}

func (s *FileEditSet) Add(path string, additions, deletions int, isNew bool) {
	operation := "Edit"
	if isNew {
		operation = "Add"
	}
	change := FileChange{Operation: operation, Additions: additions, Deletions: deletions}
	if i, ok := s.index[path]; ok {
		edit := &s.Edits[i]
		edit.Additions += additions
		edit.Deletions += deletions
		edit.Edits++
		if isNew {
			edit.IsNew = true
		}
		edit.changes = append(edit.changes, change)
		return
	}
	s.index[path] = len(s.Edits)
	s.Edits = append(s.Edits, FileEdit{
		Path:      path,
		Additions: additions,
		Deletions: deletions,
		Edits:     1,
		IsNew:     isNew,
		changes:   []FileChange{change},
	})
}

func (s *FileEditSet) Change(path, before, after string) {
	change := s.pendingChange(path)
	if before == after && change.Operation != "Add" {
		return
	}
	change.Kind = "diff"
	change.Text = renderChange(path, before, after)
}

func (s *FileEditSet) Write(path, content string) {
	change := s.pendingChange(path)
	change.Kind = "content"
	change.Text = content
	change.Operation = "Write"
}

func (s *FileEditSet) Patch(path, patch string) {
	change := s.pendingChange(path)
	change.Kind = "diff"
	change.Text = patch
	if change.Operation != "Add" {
		change.Operation = "Patch"
	}
}

func (e FileEdit) Changes() []FileChange {
	changes := make([]FileChange, 0, len(e.changes))
	for _, change := range e.changes {
		if change.Kind != "" {
			changes = append(changes, change)
		}
	}
	return changes
}

func (s *FileEditSet) pendingChange(path string) *FileChange {
	edit := &s.Edits[s.index[path]]
	if len(edit.changes) == 0 || edit.changes[len(edit.changes)-1].Kind != "" {
		edit.changes = append(edit.changes, FileChange{Operation: "Edit"})
	}
	return &edit.changes[len(edit.changes)-1]
}

func renderChange(path, before, after string) string {
	var output strings.Builder
	output.WriteString("--- " + path + "\n+++ " + path + "\n@@\n")
	writeSnippetDiff(&output, before, after)
	return output.String()
}

func writeSnippetDiff(output *strings.Builder, before, after string) {
	for _, line := range splitLines(before) {
		output.WriteString("-" + line + "\n")
	}
	for _, line := range splitLines(after) {
		output.WriteString("+" + line + "\n")
	}
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}
