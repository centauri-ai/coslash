package session

type FileEditSet struct {
	Edits []FileEdit
	index map[string]int
}

func NewFileEditSet() *FileEditSet {
	return &FileEditSet{Edits: []FileEdit{}, index: map[string]int{}}
}

func (s *FileEditSet) Add(path string, additions, deletions int, isNew bool) {
	if i, ok := s.index[path]; ok {
		edit := &s.Edits[i]
		edit.Additions += additions
		edit.Deletions += deletions
		edit.Edits++
		if isNew {
			edit.IsNew = true
		}
		return
	}
	s.index[path] = len(s.Edits)
	s.Edits = append(s.Edits, FileEdit{
		Path:      path,
		Additions: additions,
		Deletions: deletions,
		Edits:     1,
		IsNew:     isNew,
	})
}
