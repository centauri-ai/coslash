package cursor

import (
	"context"
	"os"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// Plan is the stat-free grouping of local Cursor transcripts into families,
// with the live and relationship metadata that grouping needed.
type Plan struct {
	Families map[string][]string
	Live     map[string]string
	Metadata *vendors.SessionMetadata
}

// PlanFamiliesContext groups transcripts by family using paths and the
// relationship metadata Cursor keeps beside them. Streamed discovery uses it
// to order and batch families before parsing.
func PlanFamiliesContext(ctx context.Context, files []string) (*Plan, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	live := loadLiveSessionsContext(ctx)
	metadata, metadataErr := loadSelectionMetadataWithLiveContext(ctx, home, live)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if metadataErr != nil {
		metadata = vendors.BestEffortMetadata(vendors.AgentCursor, func() (*vendors.SessionMetadata, error) {
			return nil, metadataErr
		})
	}
	union := newCursorFamilyUnion()
	for _, path := range files {
		id := IDFromPath(path)
		union.add(id)
		if parentID := ParentIDFromPath(path); parentID != "" {
			union.union(id, parentID)
		}
	}
	for childID, entry := range metadata.Sessions {
		if entry.Relationship.ParentID != "" {
			union.add(childID)
			union.add(entry.Relationship.ParentID)
			union.union(childID, entry.Relationship.ParentID)
		}
	}
	families := map[string][]string{}
	for _, path := range files {
		root := union.find(IDFromPath(path))
		families[root] = append(families[root], path)
	}
	return &Plan{Families: families, Live: live, Metadata: metadata}, nil
}

// ParseFilesContext parses whole families of local transcripts and applies
// Cursor's metadata enrichment and relationships to them. The returned
// metadata covers only these sessions.
func ParseFilesContext(ctx context.Context, files []string, live map[string]string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, 0, len(files))
	for _, path := range files {
		ids = append(ids, IDFromPath(path))
	}
	metadata, metadataErr := loadMetadataForSessionsWithLiveContext(ctx, home, canonicalCursorIDs(ids), live)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if metadataErr != nil {
		metadata = vendors.BestEffortMetadata(vendors.AgentCursor, func() (*vendors.SessionMetadata, error) {
			return nil, metadataErr
		})
	}
	parsed, err := parseTranscriptFilesSourceContext(ctx, vendors.LocalReadSource, files)
	if err != nil {
		return nil, nil, err
	}
	if err := applyCursorEnrichmentContext(ctx, parsed, metadata); err != nil {
		return nil, nil, err
	}
	if err := applyRelationshipsContext(ctx, parsed, metadata); err != nil {
		return nil, nil, err
	}
	return parsed, metadata, nil
}
