package cursor

import (
	"cmp"
	"errors"
	"io/fs"
	"sort"

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
	parsed := vendors.ParseSourceFiles(vendors.LocalReadSource, files, parseTranscriptSource)
	applyRelationships(parsed, metadata)
	if since > 0 {
		parsed = familiesSince(parsed, since)
	}
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
	return vendors.FindAndParse(files, id, IDFromPath, parseTranscript)
}

func GetSessionFamily(id string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	if id == "" {
		return nil, vendors.EmptySessionMetadata(), nil
	}
	files, err := Files()
	if err != nil {
		return nil, vendors.EmptySessionMetadata(), err
	}
	metadata := vendors.BestEffortMetadata(vendors.AgentCursor, LoadMetadata)
	parsed := vendors.ParseSourceFiles(vendors.LocalReadSource, files, parseTranscriptSource)
	applyRelationships(parsed, metadata)
	return selectFamily(parsed, id), metadata, nil
}

func applyRelationships(parsed []*vendors.ParsedSession, metadata *vendors.SessionMetadata) {
	if metadata == nil {
		metadata = vendors.EmptySessionMetadata()
	}
	byID := make(map[string]*vendors.ParsedSession, len(parsed))
	for _, item := range parsed {
		if item != nil && item.Session != nil {
			byID[item.Session.ID] = item
			applyMetadataTimes(item.Session, metadata.StartedAt[item.Session.ID], metadata.LastActivityAt[item.Session.ID])
			if cwd := metadata.WorkingDirectories[item.Session.ID]; cwd != "" {
				item.Session.WorkingDirectory = cwd
			}
			if name := metadata.Names[item.Session.ID]; name != "" {
				item.Name = name
			}
		}
	}
	type relationship struct {
		childID string
		value   vendors.SessionRelationship
	}
	relationships := make([]relationship, 0, len(metadata.Relationships))
	for childID, value := range metadata.Relationships {
		if _, ok := byID[childID]; ok {
			relationships = append(relationships, relationship{childID: childID, value: value})
		}
	}
	sort.Slice(relationships, func(i, j int) bool {
		left, right := relationships[i], relationships[j]
		if left.value.ParentID != right.value.ParentID {
			return left.value.ParentID < right.value.ParentID
		}
		if left.value.Time != right.value.Time {
			return left.value.Time < right.value.Time
		}
		return left.childID < right.childID
	})

	// Resolve the final graph before rejecting cycles, including path edges.
	// Skipping cyclic metadata alone can leave a path cycle behind.
	for _, relationship := range relationships {
		child := byID[relationship.childID]
		child.ParentID, child.SpawnKey = relationship.value.ParentID, relationship.value.SpawnKey
	}
	cyclic := cyclicRelationshipIDs(byID)
	for id := range cyclic {
		byID[id].ParentID, byID[id].SpawnKey = "", ""
	}
	claimed := map[string]map[int]bool{}
	for _, relationship := range relationships {
		if cyclic[relationship.childID] {
			continue
		}
		value := relationship.value

		parent, ok := byID[value.ParentID]
		if !ok || parent.Session == nil {
			continue
		}
		if parent.Spawns == nil {
			parent.Spawns = map[string]vendors.SpawnState{}
		}
		spawn := parent.Spawns[value.SpawnKey]
		spawn.Completed = value.Completed
		spawn.Active = value.Active
		parent.Spawns[value.SpawnKey] = spawn

		for index, entry := range parent.Session.Digest {
			if entry.Category != session.DigestSubagent || cmp.Or(parent.Spawns[entry.SpawnKey].Task, entry.Description) != value.Task || claimed[parent.Session.ID][index] {
				continue
			}
			if claimed[parent.Session.ID] == nil {
				claimed[parent.Session.ID] = map[int]bool{}
			}
			claimed[parent.Session.ID][index] = true
			spawn := parent.Spawns[entry.SpawnKey]
			spawn.Completed = value.Completed
			spawn.Active = value.Active
			delete(parent.Spawns, entry.SpawnKey)
			parent.Session.Digest[index].SpawnKey = value.SpawnKey
			parent.Spawns[value.SpawnKey] = spawn
			break
		}
	}
}

func applyMetadataTimes(value *session.Session, startedAt, lastActivityAt int64) {
	if startedAt > 0 && (lastActivityAt >= startedAt || lastActivityAt == 0 && value.LastActivityTime >= startedAt) {
		value.StartedAt = startedAt
	}
	if lastActivityAt > 0 && lastActivityAt >= value.StartedAt {
		value.LastActivityTime = lastActivityAt
	}
	value.DurationMs = nil
	if value.StartedAt > 0 && value.LastActivityTime >= value.StartedAt {
		duration := int(value.LastActivityTime - value.StartedAt)
		value.DurationMs = &duration
	}
}

func cyclicRelationshipIDs(byID map[string]*vendors.ParsedSession) map[string]bool {
	parents := map[string]string{}
	for id, item := range byID {
		if item.ParentID != "" {
			if _, ok := byID[item.ParentID]; ok {
				parents[id] = item.ParentID
			}
		}
	}
	ids := make([]string, 0, len(parents))
	for id := range parents {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	cyclic := map[string]bool{}
	state := map[string]int{}
	stack := []string{}
	position := map[string]int{}
	var visit func(string)
	visit = func(id string) {
		state[id] = 1
		position[id] = len(stack)
		stack = append(stack, id)
		if parentID, ok := parents[id]; ok {
			switch state[parentID] {
			case 0:
				visit(parentID)
			case 1:
				for _, member := range stack[position[parentID]:] {
					cyclic[member] = true
				}
			}
		}
		stack = stack[:len(stack)-1]
		delete(position, id)
		state[id] = 2
	}
	for _, id := range ids {
		if state[id] == 0 {
			visit(id)
		}
	}
	return cyclic
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
	seenAncestors := map[string]bool{}
	for root.ParentID != "" {
		if seenAncestors[root.Session.ID] {
			return nil
		}
		seenAncestors[root.Session.ID] = true
		parent, ok := byID[root.ParentID]
		if !ok {
			return nil
		}
		root = parent
	}

	children := map[string][]*vendors.ParsedSession{}
	for _, item := range parsed {
		if item == nil || item.Session == nil || item.ParentID == "" {
			continue
		}
		if _, ok := byID[item.ParentID]; ok {
			children[item.ParentID] = append(children[item.ParentID], item)
		}
	}
	for _, entries := range children {
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Session.ID < entries[j].Session.ID
		})
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

func familiesSince(parsed []*vendors.ParsedSession, since int64) []*vendors.ParsedSession {
	// ponytail: full-history parsing and one scan per changed family; index families if large local histories make polling costly.
	selected := map[string]bool{}
	for _, item := range parsed {
		if item.LogModifiedAtMs < since || selected[item.Session.ID] {
			continue
		}
		// Let shared composition report and omit a child whose parent is missing.
		selected[item.Session.ID] = true
		for _, member := range selectFamily(parsed, item.Session.ID) {
			selected[member.Session.ID] = true
		}
	}
	result := make([]*vendors.ParsedSession, 0, len(selected))
	for _, item := range parsed {
		if selected[item.Session.ID] {
			result = append(result, item)
		}
	}
	return result
}
