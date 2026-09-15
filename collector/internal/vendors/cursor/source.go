package cursor

import (
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
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
	metadata := vendors.BestEffortMetadata(vendors.AgentCursor, LoadMetadata)
	files = selectCursorFilesSource(vendors.LocalReadSource, files, since)
	parsed := parseTranscriptFilesSource(vendors.LocalReadSource, files)
	applyCursorEnrichment(parsed, metadata)
	return parsed, metadata, nil
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
	parsed, err := parseTranscriptFragmentsSource(vendors.LocalReadSource, fragments)
	if err != nil || parsed == nil {
		return parsed, err
	}
	applyCursorEnrichment([]*vendors.ParsedSession{parsed}, vendors.BestEffortMetadata(vendors.AgentCursor, LoadMetadata))
	return parsed, nil
}

func GetSessionFamily(id string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	if id == "" {
		return nil, vendors.EmptySessionMetadata(), nil
	}
	files, err := Files()
	if err != nil {
		return nil, vendors.EmptySessionMetadata(), err
	}
	parsed := parseTranscriptFilesSource(vendors.LocalReadSource, files)
	metadata := vendors.BestEffortMetadata(vendors.AgentCursor, LoadMetadata)
	applyCursorEnrichment(parsed, metadata)
	return selectFamily(parsed, id), metadata, nil
}

func applyCursorEnrichment(parsed []*vendors.ParsedSession, metadata *vendors.SessionMetadata) {
	if metadata == nil {
		return
	}
	for _, item := range parsed {
		if item == nil || item.Session == nil {
			continue
		}
		enrichment := metadata.Session(item.Session.ID)
		applyMetadataTimes(item.Session, enrichment.StartedAt, enrichment.LastActivityAt)
		if enrichment.WorkingDirectory != "" {
			item.Session.WorkingDirectory = enrichment.WorkingDirectory
		}
		mergeIDEFileEdits(item.Session, enrichment.FileEdits)
		if len(enrichment.CommitObservations) > 0 {
			item.Session.CommitLog = append(item.Session.CommitLog, enrichment.CommitObservations...)
		}
	}
}

func applyMetadataTimes(value *session.Session, startedAt, lastActivityAt int64) {
	if startedAt > 0 {
		value.StartedAt = startedAt
	}
	if lastActivityAt > 0 {
		value.LastActivityTime = lastActivityAt
	}
}

func mergeIDEFileEdits(value *session.Session, sideStore []session.FileEdit) {
	if len(sideStore) == 0 {
		return
	}
	byPath := make(map[string]int, len(value.FileEdits))
	for index, edit := range value.FileEdits {
		byPath[normalizedEditPath(value.WorkingDirectory, edit.Path)] = index
	}
	for _, edit := range sideStore {
		path := normalizedEditPath(value.WorkingDirectory, edit.Path)
		if index, ok := byPath[path]; ok {
			value.FileEdits[index].Additions = edit.Additions
			value.FileEdits[index].Deletions = edit.Deletions
			value.FileEdits[index].IsNew = edit.IsNew
			continue
		}
		edit.Path = path
		value.FileEdits = append(value.FileEdits, edit)
		byPath[path] = len(value.FileEdits) - 1
	}
	value.EditedFileCount = len(value.FileEdits)
}

func normalizedEditPath(cwd, path string) string {
	path = filepath.Clean(path)
	if cwd == "" || !filepath.IsAbs(path) {
		return path
	}
	relative, err := filepath.Rel(cwd, path)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return relative
	}
	return path
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
	return vendors.FileSourceHealth(vendors.AgentCursor, root, scan, func(string) (bool, error) { return true, nil })
}
