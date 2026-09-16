package cursor

import (
	"cmp"
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
	selectionMetadata := vendors.BestEffortMetadata(vendors.AgentCursor, LoadSelectionMetadata)
	files = selectCursorFilesSourceWithMetadata(vendors.LocalReadSource, files, since, selectionMetadata)
	ids := make([]string, 0, len(files))
	for _, path := range files {
		ids = append(ids, IDFromPath(path))
	}
	metadata := vendors.BestEffortMetadata(vendors.AgentCursor, func() (*vendors.SessionMetadata, error) {
		return LoadMetadataForSessions(canonicalCursorIDs(ids), files)
	})
	parsed := parseTranscriptFilesSource(vendors.LocalReadSource, files)
	for _, item := range parsed {
		if item != nil && item.Session != nil && item.Stopped {
			// A terminal transcript error is authoritative over a stale open DB.
			metadata.Session(item.Session.ID).Live = ""
		}
	}
	applyCursorEnrichment(parsed, metadata)
	applyRelationships(parsed, metadata)
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
	metadata := vendors.BestEffortMetadata(vendors.AgentCursor, func() (*vendors.SessionMetadata, error) {
		return LoadMetadataForSessions([]string{parsed.Session.ID}, fragments)
	})
	applyCursorEnrichment([]*vendors.ParsedSession{parsed}, metadata)
	applyRelationships([]*vendors.ParsedSession{parsed}, metadata)
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
	requestedFiles := make([]string, 0, 1)
	for _, path := range files {
		if IDFromPath(path) == strings.ToLower(id) {
			requestedFiles = append(requestedFiles, path)
		}
	}
	metadata := vendors.BestEffortMetadata(vendors.AgentCursor, func() (*vendors.SessionMetadata, error) {
		return LoadMetadataForSessions([]string{id}, requestedFiles)
	})
	familyFiles := cursorFamilyFilesWithMetadata(files, id, metadata)
	parsed := parseTranscriptFilesSource(vendors.LocalReadSource, familyFiles)
	ids := make([]string, 0, len(parsed))
	for _, item := range parsed {
		if item != nil && item.Session != nil {
			ids = append(ids, item.Session.ID)
		}
	}
	metadata = vendors.BestEffortMetadata(vendors.AgentCursor, func() (*vendors.SessionMetadata, error) {
		return LoadMetadataForSessions(ids, familyFiles)
	})
	applyCursorEnrichment(parsed, metadata)
	applyRelationships(parsed, metadata)
	return selectFamily(parsed, id), metadata, nil
}

func applyRelationships(parsed []*vendors.ParsedSession, metadata *vendors.SessionMetadata) {
	if metadata == nil {
		return
	}
	byID := map[string]*vendors.ParsedSession{}
	for _, item := range parsed {
		if item != nil && item.Session != nil {
			byID[item.Session.ID] = item
		}
	}
	for childID, entry := range metadata.Sessions {
		if child := byID[childID]; child != nil && entry.Relationship.ParentID != "" {
			child.ParentID, child.SpawnKey = entry.Relationship.ParentID, entry.Relationship.SpawnKey
		}
	}
	for _, item := range byID {
		seen := map[string]bool{}
		for item.ParentID != "" {
			if seen[item.Session.ID] {
				item.ParentID, item.SpawnKey = "", ""
				break
			}
			seen[item.Session.ID] = true
			parent := byID[item.ParentID]
			if parent == nil {
				break
			}
			item = parent
		}
	}
	childIDs := make([]string, 0, len(metadata.Sessions))
	for childID := range metadata.Sessions {
		childIDs = append(childIDs, childID)
	}
	sort.Strings(childIDs)
	claimed := map[string]map[int]bool{}
	for _, childID := range childIDs {
		entry := metadata.Sessions[childID]
		value := entry.Relationship
		child, parent := byID[childID], byID[value.ParentID]
		if child == nil || parent == nil || child.ParentID == "" {
			continue
		}
		if parent.Spawns == nil {
			parent.Spawns = map[string]vendors.SpawnState{}
		}
		spawn := parent.Spawns[value.SpawnKey]
		spawn.Completed, spawn.Active = value.Completed, value.Active
		parent.Spawns[value.SpawnKey] = spawn
		for i, digest := range parent.Session.Digest {
			if claimed[value.ParentID][i] {
				continue
			}
			if digest.Category != session.DigestSubagent || (digest.SpawnKey != value.SpawnKey && cmp.Or(parent.Spawns[digest.SpawnKey].Task, digest.Description) != value.Task) {
				continue
			}
			if claimed[value.ParentID] == nil {
				claimed[value.ParentID] = map[int]bool{}
			}
			claimed[value.ParentID][i] = true
			if value.Task == "" {
				value.Task = digest.Description
				metadata.Session(childID).Relationship.Task = value.Task
			}
			spawn := parent.Spawns[digest.SpawnKey]
			spawn.Completed, spawn.Active = value.Completed, value.Active
			delete(parent.Spawns, digest.SpawnKey)
			parent.Session.Digest[i].SpawnKey = value.SpawnKey
			parent.Spawns[value.SpawnKey] = spawn
			break
		}
	}
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
		enrichment.FileEdits = append([]session.FileEdit(nil), item.Session.FileEdits...)
		if len(enrichment.CommitObservations) > 0 {
			item.Session.CommitLog = append(item.Session.CommitLog, enrichment.CommitObservations...)
		}
		vendors.ApplySessionEnrichment(item, enrichment)
		if item.Session.ContextWindow == nil && item.Session.Model != nil {
			item.Session.ContextWindow = session.ContextWindowFor(*item.Session.Model)
		}
		session.AttachCost(item.Session, item.RecordedCost)
	}
}

func applyMetadataTimes(value *session.Session, startedAt, lastActivityAt int64) {
	changed := false
	if startedAt > 0 {
		value.StartedAt = startedAt
		changed = true
	}
	if lastActivityAt > 0 {
		value.LastActivityTime = lastActivityAt
		changed = true
	}
	if changed {
		value.DurationMs = nil
		if value.StartedAt > 0 && value.LastActivityTime >= value.StartedAt {
			duration := int(value.LastActivityTime - value.StartedAt)
			value.DurationMs = &duration
		}
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

func cursorFamilyFiles(files []string, id string) []string {
	return cursorFamilyFilesWithMetadata(files, id, nil)
}

func cursorFamilyFilesWithMetadata(files []string, id string, metadata *vendors.SessionMetadata) []string {
	union := newCursorFamilyUnion()
	for _, path := range files {
		childID := IDFromPath(path)
		union.add(childID)
		if parentID := ParentIDFromPath(path); parentID != "" {
			union.union(childID, parentID)
		}
	}
	if metadata != nil {
		for childID, entry := range metadata.Sessions {
			if entry.Relationship.ParentID != "" {
				union.union(childID, entry.Relationship.ParentID)
			}
		}
	}
	want := union.find(strings.ToLower(id))
	selected := make([]string, 0, len(files))
	for _, path := range files {
		if union.find(IDFromPath(path)) == want {
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
	return selectCursorFilesSourceWithMetadata(source, files, since, nil)
}

func selectCursorFilesSourceWithMetadata(source vendors.ReadSource, files []string, since int64, metadata *vendors.SessionMetadata) []string {
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
	if metadata != nil {
		for childID, entry := range metadata.Sessions {
			if entry.Relationship.ParentID != "" {
				union.add(childID)
				union.add(entry.Relationship.ParentID)
				union.union(childID, entry.Relationship.ParentID)
			}
		}
	}
	eligibleFamilies := map[string]bool{}
	for _, path := range files {
		familyID := union.find(IDFromPath(path))
		if since <= 0 {
			eligibleFamilies[familyID] = true
			continue
		}
		if metadata != nil && metadata.Session(IDFromPath(path)).Live != "" {
			eligibleFamilies[familyID] = true
			continue
		}
		info, err := source.Stat(path)
		enrichment := metadata.Lookup(IDFromPath(path))
		if err != nil || info.ModTime().UnixMilli() >= since ||
			enrichment != nil && max(enrichment.StartedAt, enrichment.LastActivityAt) >= since {
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
	selected, _ := vendors.LimitNewestFileFamilies(eligible, vendors.MaxCandidateFilesPerAgent,
		func(path string) string { return union.find(IDFromPath(path)) },
		func(path string) int64 {
			modified := vendors.SourceModificationTime(source, path)
			if enrichment := metadata.Lookup(IDFromPath(path)); enrichment != nil {
				modified = max(modified, enrichment.StartedAt, enrichment.LastActivityAt)
			}
			return modified
		})
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
	id = strings.ToLower(id)
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
	ancestors := map[string]bool{}
	for root.ParentID != "" {
		if ancestors[root.Session.ID] || root.ParentID == root.Session.ID {
			return nil
		}
		ancestors[root.Session.ID] = true
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
	ids := make([]string, 0, len(scan.Files))
	for _, path := range scan.Files {
		ids = append(ids, IDFromPath(path))
	}
	metadata := vendors.BestEffortMetadata(vendors.AgentCursor, func() (*vendors.SessionMetadata, error) {
		return LoadMetadataForSessions(canonicalCursorIDs(ids), scan.Files)
	})
	return cursorSourceHealth(root, scan, metadata)
}

func cursorSourceHealth(root string, scan *vendors.SourceScan, metadata *vendors.SessionMetadata) vendors.SourceHealth {
	seen := map[string]bool{}
	return vendors.FileSourceHealth(vendors.AgentCursor, root, scan,
		func(path string) (bool, error) {
			if ParentIDFromPath(path) != "" {
				return false, nil
			}
			id := IDFromPath(path)
			if entry := metadata.Lookup(id); entry != nil && entry.Relationship.ParentID != "" {
				return false, nil
			}
			isRoot := !seen[id]
			seen[id] = true
			return isRoot, nil
		})
}
