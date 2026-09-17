package cursor

import (
	"errors"
	"io/fs"
	"sort"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func Collect(since int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	files, err := Files()
	if errors.Is(err, fs.ErrNotExist) {
		return []*vendors.ParsedSession{}, vendors.EmptySessionMetadata(), nil
	}
	if err != nil {
		return nil, nil, err
	}
	files = selectCursorFilesSource(vendors.LocalReadSource, files, since)
	return parseTranscriptFilesSource(vendors.LocalReadSource, files), vendors.EmptySessionMetadata(), nil
}

func GetSessionFacts(id string) (*vendors.ParsedSession, error) {
	if id == "" {
		return nil, nil
	}
	files, err := Files()
	if err != nil {
		return nil, err
	}
	fragments := make([]string, 0, 1)
	for _, path := range files {
		if IDFromPath(path) == id {
			fragments = append(fragments, path)
		}
	}
	if len(fragments) == 0 {
		return nil, nil
	}
	return parseTranscriptFragmentsSource(vendors.LocalReadSource, fragments)
}

func GetSessionFamily(id string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	if id == "" {
		return nil, vendors.EmptySessionMetadata(), nil
	}
	files, err := Files()
	if err != nil {
		return nil, vendors.EmptySessionMetadata(), err
	}
	parsed := parseTranscriptFilesSource(vendors.LocalReadSource, cursorFamilyFiles(files, id))
	return selectFamily(parsed, id), vendors.EmptySessionMetadata(), nil
}

func cursorFamilyFiles(files []string, id string) []string {
	parents := make(map[string]string, len(files))
	for _, path := range files {
		parents[IDFromPath(path)] = ParentIDFromPath(path)
	}
	root := func(value string) string {
		seen := map[string]bool{}
		for parents[value] != "" && !seen[value] {
			seen[value] = true
			value = parents[value]
		}
		return value
	}
	want := root(id)
	selected := make([]string, 0, len(files))
	for _, path := range files {
		if root(IDFromPath(path)) == want {
			selected = append(selected, path)
		}
	}
	return selected
}

func parseTranscriptFilesSource(source vendors.ReadSource, files []string) []*vendors.ParsedSession {
	groups := make(map[string][]string, len(files))
	ids := make([]string, 0, len(files))
	for _, path := range files {
		id := IDFromPath(path)
		if _, exists := groups[id]; !exists {
			ids = append(ids, id)
		}
		groups[id] = append(groups[id], path)
	}
	return vendors.ParseFiles(ids, func(id string) (*vendors.ParsedSession, error) {
		return parseTranscriptFragmentsSource(source, groups[id])
	})
}

func selectCursorFilesSource(source vendors.ReadSource, files []string, since int64) []string {
	if len(files) == 0 {
		return nil
	}
	union := newCursorFamilyUnion()
	for _, path := range files {
		id := IDFromPath(path)
		union.add(id)
		if parentID := ParentIDFromPath(path); parentID != "" {
			union.union(id, parentID)
		}
	}
	eligibleFamilies := map[string]bool{}
	for _, path := range files {
		familyID := union.find(IDFromPath(path))
		if since <= 0 {
			eligibleFamilies[familyID] = true
			continue
		}
		info, err := source.Stat(path)
		if err != nil || info.ModTime().UnixMilli() >= since {
			eligibleFamilies[familyID] = true
		}
	}
	eligible := make([]string, 0, len(files))
	for _, path := range files {
		if eligibleFamilies[union.find(IDFromPath(path))] {
			eligible = append(eligible, path)
		}
	}
	if since <= 0 {
		return eligible
	}
	selected, _ := vendors.LimitNewestSourceFileFamilies(source, eligible, vendors.MaxCandidateFilesPerAgent,
		func(path string) string { return union.find(IDFromPath(path)) })
	return selected
}

type cursorFamilyUnion struct{ parents map[string]string }

func newCursorFamilyUnion() *cursorFamilyUnion {
	return &cursorFamilyUnion{parents: map[string]string{}}
}
func (u *cursorFamilyUnion) add(id string) {
	if _, ok := u.parents[id]; !ok {
		u.parents[id] = id
	}
}
func (u *cursorFamilyUnion) find(id string) string {
	u.add(id)
	parent := u.parents[id]
	if parent == id {
		return id
	}
	u.parents[id] = u.find(parent)
	return u.parents[id]
}
func (u *cursorFamilyUnion) union(left, right string) {
	leftRoot, rightRoot := u.find(left), u.find(right)
	if leftRoot == rightRoot {
		return
	}
	if leftRoot < rightRoot {
		u.parents[rightRoot] = leftRoot
	} else {
		u.parents[leftRoot] = rightRoot
	}
}

func selectFamily(parsed []*vendors.ParsedSession, id string) []*vendors.ParsedSession {
	byID := make(map[string]*vendors.ParsedSession, len(parsed))
	for _, item := range parsed {
		if item != nil && item.Session != nil {
			byID[item.Session.ID] = item
		}
	}
	root, ok := byID[id]
	if !ok {
		return nil
	}
	for root.ParentID != "" {
		parent, ok := byID[root.ParentID]
		if !ok {
			return nil
		}
		root = parent
	}
	children := map[string][]*vendors.ParsedSession{}
	for _, item := range parsed {
		if item != nil && item.Session != nil && item.ParentID != "" {
			if _, ok := byID[item.ParentID]; ok {
				children[item.ParentID] = append(children[item.ParentID], item)
			}
		}
	}
	for _, entries := range children {
		sort.Slice(entries, func(i, j int) bool { return entries[i].Session.ID < entries[j].Session.ID })
	}
	family := []*vendors.ParsedSession{}
	seen := map[string]bool{}
	var visit func(*vendors.ParsedSession)
	visit = func(item *vendors.ParsedSession) {
		if seen[item.Session.ID] {
			return
		}
		seen[item.Session.ID] = true
		family = append(family, item)
		for _, child := range children[item.Session.ID] {
			visit(child)
		}
	}
	visit(root)
	return family
}

func Health() vendors.SourceHealth {
	root, err := Root()
	if err != nil {
		return vendors.SourceHealth{Agent: vendors.AgentCursor, Err: err}
	}
	scan, err := Scan()
	if err != nil {
		return vendors.SourceHealth{Agent: vendors.AgentCursor, Root: root, Err: err}
	}
	return cursorSourceHealth(root, scan)
}

func cursorSourceHealth(root string, scan *vendors.SourceScan) vendors.SourceHealth {
	return vendors.FileSourceHealth(vendors.AgentCursor, root, scan,
		func(path string) (bool, error) { return ParentIDFromPath(path) == "", nil })
}
