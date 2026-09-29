package opencode

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// FamilyRef names one active root family and its newest activity in epoch
// milliseconds, read from the session rows alone.
type FamilyRef struct {
	ID         string
	ActivityMs int64
}

const familiesQuery = `
SELECT root.id,
	MAX(root.time_updated, COALESCE((
		SELECT MAX(child.time_updated) FROM sessions AS child WHERE child.parent_id = root.id
	), root.time_updated), COALESCE((
		SELECT MAX(child.time_archived) FROM sessions AS child WHERE child.parent_id = root.id
	), root.time_updated))
FROM sessions AS root
WHERE root.parent_id IS NULL AND root.time_archived IS NULL`

// FamiliesContext lists active root families without reading any message.
// A missing database is an empty list.
func FamiliesContext(ctx context.Context) ([]FamilyRef, error) {
	db, err := openContext(ctx)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()
	source, err := sessionSourceContext(ctx, db)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, source+" "+familiesQuery)
	if err != nil {
		return nil, fmt.Errorf("query OpenCode families: %w", err)
	}
	defer rows.Close()
	var families []FamilyRef
	for rows.Next() {
		var ref FamilyRef
		if err := rows.Scan(&ref.ID, &ref.ActivityMs); err != nil {
			return nil, err
		}
		families = append(families, ref)
	}
	return families, rows.Err()
}

// LoadFamiliesContext parses exactly the named families through the parse
// cache and returns the database's live metadata.
func LoadFamiliesContext(ctx context.Context, ids []string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	if len(ids) == 0 {
		return nil, vendors.EmptySessionMetadata(), nil
	}
	db, err := openContext(ctx)
	if errors.Is(err, os.ErrNotExist) {
		return nil, vendors.EmptySessionMetadata(), nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()
	metadata, metadataErr := loadMetadataContext(ctx, db)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if metadataErr != nil {
		log.Printf("%s session metadata failed: %v; continuing without enrichment", vendors.AgentOpenCode, metadataErr)
		metadata = vendors.EmptySessionMetadata()
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	query := activeFamiliesQuery + " AND selected_roots.id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `)
		ORDER BY selected_roots.family_updated DESC, member.parent_id IS NOT NULL, member.time_updated, member.id`
	parsed, skipped, err := loadContext(ctx, db, query, args...)
	if err != nil {
		return nil, nil, err
	}
	for _, family := range skipped {
		log.Printf("OpenCode session family %q: %v; skipping", family.id, family.err)
	}
	return parsed, metadata, nil
}
