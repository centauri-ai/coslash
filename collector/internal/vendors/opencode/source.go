package opencode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const activeFamiliesQuery = `
WITH selected_roots AS (
	SELECT root.id,
		MAX(root.time_updated,
			COALESCE(MAX(child.time_updated), root.time_updated),
			COALESCE(MAX(child.time_archived), root.time_updated)) AS family_updated
	FROM session AS root
	LEFT JOIN session AS child ON child.parent_id = root.id
	WHERE root.parent_id IS NULL AND root.time_archived IS NULL
	GROUP BY root.id
)
SELECT member.id, member.parent_id, member.directory, member.title, member.summary_files,
	member.summary_diffs, member.agent, member.model, member.cost,
	CASE WHEN member.id = selected_roots.id THEN selected_roots.family_updated ELSE member.time_updated END
FROM selected_roots
JOIN session AS member ON member.id = selected_roots.id OR member.parent_id = selected_roots.id
WHERE member.time_archived IS NULL`

const activeRootQuery = `
SELECT root.id, root.parent_id, root.directory, root.title, root.summary_files,
	root.summary_diffs, root.agent, root.model, root.cost,
	MAX(root.time_updated, COALESCE((
		SELECT MAX(child.time_updated)
		FROM session AS child
		WHERE child.parent_id = root.id
	), root.time_updated), COALESCE((
		SELECT MAX(child.time_archived)
		FROM session AS child
		WHERE child.parent_id = root.id
	), root.time_updated))
FROM session AS root
WHERE root.parent_id IS NULL AND root.time_archived IS NULL`

type skippedFamily struct {
	id  string
	err error
}

func Collect(since int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	db, err := open()
	if errors.Is(err, os.ErrNotExist) {
		return []*vendors.ParsedSession{}, vendors.EmptySessionMetadata(), nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()
	metadata := vendors.BestEffortMetadata(vendors.AgentOpenCode, func() (*vendors.SessionMetadata, error) {
		return loadMetadata(db)
	})

	query := activeFamiliesQuery
	var args []any
	if since > 0 {
		query += " AND (selected_roots.family_updated >= ?"
		args = append(args, since)
		if len(metadata.Live) > 0 {
			query += " OR selected_roots.id IN (" +
				strings.TrimSuffix(strings.Repeat("?,", len(metadata.Live)), ",") + ")"
			for id := range metadata.Live {
				args = append(args, id)
			}
		}
		query += ")"
	}
	query += ` ORDER BY selected_roots.family_updated DESC,
		member.parent_id IS NOT NULL, member.time_updated, member.id`
	parsed, skipped, err := load(db, query, args...)
	for _, family := range skipped {
		log.Printf("OpenCode session family %q: %v; skipping", family.id, family.err)
	}
	return parsed, metadata, err
}

func GetSessionFacts(id string) (*vendors.ParsedSession, error) {
	if id == "" {
		return nil, nil
	}
	db, err := open()
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()

	parsed, skipped, err := load(db, activeRootQuery+" AND root.id = ?", id)
	if err != nil || len(parsed) == 0 {
		if len(skipped) > 0 {
			return nil, fmt.Errorf(
				"parse OpenCode session family %q: %w",
				skipped[0].id,
				skipped[0].err,
			)
		}
		return nil, err
	}
	return parsed[0], nil
}

func Health() vendors.SourceHealth {
	root, rootErr := Root()
	if rootErr != nil {
		return vendors.SourceHealth{Agent: vendors.AgentOpenCode, Err: rootErr}
	}
	db, err := open()
	if errors.Is(err, os.ErrNotExist) {
		return vendors.SourceHealth{Agent: vendors.AgentOpenCode, Root: root, Missing: true}
	}
	if err != nil {
		return vendors.SourceHealth{Agent: vendors.AgentOpenCode, Root: root, Err: err}
	}
	defer db.Close()

	health := vendors.SourceHealth{Agent: vendors.AgentOpenCode, Root: root}
	err = db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(parent_id IS NULL), 0)
		FROM session
		WHERE time_archived IS NULL
	`).Scan(&health.Entries, &health.Sessions)
	health.Err = err
	return health
}

func load(
	db *sql.DB,
	query string,
	args ...any,
) ([]*vendors.ParsedSession, []skippedFamily, error) {
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("query OpenCode sessions: %w", err)
	}
	stored := []storedSession{}
	for rows.Next() {
		var row storedSession
		if err := rows.Scan(
			&row.id, &row.parentID, &row.directory, &row.title, &row.summaryFiles, &row.summaryDiffs,
			&row.agent, &row.model, &row.cost, &row.updatedAt,
		); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("read OpenCode session: %w", err)
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, fmt.Errorf("read OpenCode sessions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}

	families := map[string][]storedSession{}
	familyIDs := []string{}
	for _, row := range stored {
		familyID := row.id
		if row.parentID.Valid {
			familyID = row.parentID.String
		}
		if _, exists := families[familyID]; !exists {
			familyIDs = append(familyIDs, familyID)
		}
		families[familyID] = append(families[familyID], row)
	}
	parsed := make([]parsedSession, 0, len(stored))
	skipped := []skippedFamily{}
	for _, familyID := range familyIDs {
		family := make([]parsedSession, 0, len(families[familyID]))
		for _, row := range families[familyID] {
			item, err := parse(tx, row)
			if err != nil {
				if !errors.Is(err, errMalformedSession) {
					return nil, nil, fmt.Errorf("parse OpenCode session %q: %w", row.id, err)
				}
				skipped = append(skipped, skippedFamily{id: familyID, err: err})
				family = nil
				break
			}
			family = append(family, item)
		}
		parsed = append(parsed, family...)
	}
	byID := make(map[string]*vendors.ParsedSession, len(parsed))
	for _, item := range parsed {
		byID[item.transcript.Session.ID] = item.transcript
	}
	for _, parent := range parsed {
		for childID, task := range parent.tasks {
			child, ok := byID[childID]
			if !ok || child.ParentID != parent.transcript.Session.ID {
				continue
			}
			child.SpawnKey = childID
			child.Stopped = task.status == "error"
			if task.name != "" {
				child.Name = task.name
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	transcripts := make([]*vendors.ParsedSession, 0, len(parsed))
	for _, item := range parsed {
		transcripts = append(transcripts, item.transcript)
	}
	return transcripts, skipped, nil
}
