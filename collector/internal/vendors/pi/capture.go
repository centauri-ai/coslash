package pi

import (
	"context"
	"fmt"
	"reflect"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

type SourceRef struct {
	ID   string
	Path string
}

// SourcePlan resolves roots and header identities once, before stat-only inventory.
type SourcePlan struct {
	Scan       *vendors.SourceScan
	Sources    []SourceRef
	Metadata   *vendors.SessionMetadata
	headers    map[string]header
	identities map[string][]string
	leaves     map[string]*string
}

// SessionInputs separates one visible session from its private fork evidence.
type SessionInputs struct {
	ID       string
	Path     string
	Files    []string // selected transcript first, followed by its ancestors
	headers  []header
	leaf     *string
	complete bool
}

// ContextSelection contains only normalized selection evidence, never owner identity.
type ContextSelection struct {
	LeafID   string
	Verified bool
}

func PlanSourcesContext(ctx context.Context) (*SourcePlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot, _ := snapshotContext(ctx)
	ctx = WithRuntimeSnapshot(ctx, snapshot)
	scan, _, headers, err := discoverHeadersContext(ctx)
	if err != nil {
		return nil, err
	}
	plan := &SourcePlan{Scan: scan, headers: headers, identities: map[string][]string{}, leaves: map[string]*string{}}
	plan.Metadata = vendors.BestEffortMetadata(vendors.AgentPi, func() (*vendors.SessionMetadata, error) { return LoadMetadataContext(ctx) })
	for path, h := range headers {
		plan.identities[h.ID] = append(plan.identities[h.ID], path)
	}
	for _, path := range scan.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h, ok := headers[path]
		if !ok {
			continue
		}
		if len(plan.identities[h.ID]) != 1 {
			scan.RecordSkipped(path, fmt.Errorf("conflicting Pi session identity"))
			continue
		}
		plan.Sources = append(plan.Sources, SourceRef{ID: h.ID, Path: path})
		if snapshot != nil {
			if leaf, verified := snapshot.Leaf(h.ID, path); verified {
				plan.leaves[path] = &leaf
			}
		}
	}
	return plan, nil
}

// SessionInputsContext reads ancestor headers only for the selected session.
func (plan *SourcePlan) SessionInputsContext(ctx context.Context, id string) (SessionInputs, error) {
	if err := ctx.Err(); err != nil {
		return SessionInputs{}, err
	}
	paths := plan.identities[id]
	if len(paths) != 1 {
		return SessionInputs{}, fmt.Errorf("%w: Pi source identity unavailable", vendors.ErrInvalidData)
	}
	path := paths[0]
	h := plan.headers[path]
	inputs := SessionInputs{ID: id, Path: path, Files: []string{path}, headers: []header{h}, complete: true}
	if leaf := plan.leaves[path]; leaf != nil {
		value := *leaf
		inputs.leaf = &value
	}
	seenPaths, seenIDs := map[string]bool{path: true}, map[string]bool{id: true}
	for depth := 0; h.ParentSession != ""; depth++ {
		if err := ctx.Err(); err != nil {
			return SessionInputs{}, err
		}
		parent := canonicalPath(h.ParentSession)
		if depth >= 256 || seenPaths[parent] {
			inputs.complete = false
			break
		}
		var err error
		h, err = readTranscriptHeader(ctx, parent)
		if ctx.Err() != nil {
			return SessionInputs{}, ctx.Err()
		}
		identities := plan.identities[h.ID]
		if err != nil || seenIDs[h.ID] || len(identities) > 1 || len(identities) == 1 && identities[0] != parent {
			inputs.complete = false
			break
		}
		seenPaths[parent], seenIDs[h.ID] = true, true
		inputs.Files = append(inputs.Files, parent)
		inputs.headers = append(inputs.headers, h)
	}
	return inputs, nil
}

// ParseFrozenSessionContext opens only the supplied copies, including ancestor evidence.
func ParseFrozenSessionContext(ctx context.Context, inputs SessionInputs, files map[string]string) (*vendors.ParsedSession, ContextSelection, error) {
	if err := ctx.Err(); err != nil {
		return nil, ContextSelection{}, err
	}
	invalid := func() (*vendors.ParsedSession, ContextSelection, error) {
		return nil, ContextSelection{}, fmt.Errorf("%w: invalid Pi frozen inputs", vendors.ErrInvalidData)
	}
	if len(inputs.Files) == 0 || len(inputs.Files) != len(inputs.headers) || inputs.Files[0] != inputs.Path || inputs.headers[0].ID != inputs.ID {
		return invalid()
	}
	for _, path := range inputs.Files {
		if files[path] == "" {
			return invalid()
		}
	}
	var root, parent, previous *transcript
	provenance := inputs.complete
	for i, path := range inputs.Files {
		frozen := files[path]
		t, err := parseTranscriptContext(ctx, frozen)
		if ctx.Err() != nil {
			return nil, ContextSelection{}, ctx.Err()
		}
		if err != nil {
			if i == 0 {
				return invalid()
			}
			h, headerErr := readTranscriptHeader(ctx, frozen)
			if ctx.Err() != nil {
				return nil, ContextSelection{}, ctx.Err()
			}
			if headerErr != nil || !reflect.DeepEqual(h, inputs.headers[i]) {
				return invalid()
			}
			provenance = false
			break
		}
		h := t.Header
		h.Raw = nil
		if !reflect.DeepEqual(h, inputs.headers[i]) {
			return invalid()
		}
		if previous != nil {
			// Rebind only the private parsed header; retained JSONL bytes stay exact.
			previous.Header.ParentSession = t.Path
			if _, verified := inheritedEntriesContext(ctx, previous, t); !verified {
				provenance = false
			}
		} else {
			root = t
		}
		if i == 1 {
			parent = t
		}
		previous = t
	}
	if !provenance {
		parent = nil
	}
	selection := ContextSelection{}
	if len(root.Entries) > 0 {
		selection.LeafID = root.Entries[len(root.Entries)-1].ID
	}
	if inputs.leaf != nil {
		if _, ok := root.ByID[*inputs.leaf]; ok {
			selection = ContextSelection{LeafID: *inputs.leaf, Verified: true}
		}
	}
	parsed, err := projectLeafContext(ctx, root, parent, selection.LeafID, selection.Verified)
	if err != nil {
		return nil, ContextSelection{}, err
	}
	parsed.Session.TranscriptPath, parsed.LogPath = inputs.Path, inputs.Path
	parsed.Session.DetailsIncomplete = parsed.Session.DetailsIncomplete || root.Incomplete || len(root.Diagnostics) > 0 || !provenance
	return parsed, selection, nil
}
