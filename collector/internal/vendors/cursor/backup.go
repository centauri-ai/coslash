package cursor

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

// BackupPlan binds each transcript and projected SQLite row to one Cursor
// session. The database files themselves are never included in a bundle.
type BackupPlan struct {
	Files      map[string][]string
	Rows       map[string][]BackupRows
	Sidecars   map[string][]byte
	Metadata   *vendors.SessionMetadata
	Entrypoint string
	dbPaths    []string
	dbState    map[string]fileState
}

type BackupRows struct {
	SourceKey string
	Bytes     []byte
}

var ErrBackupUnstable = errors.New("Cursor backup source changed during capture")

// ErrBackupRowsUnrepresentable reports attributed rows that
// session-backup-db-rows/v1 cannot carry, such as a value over its per-value
// size limit. The rows do belong to the session, so this is not an
// attribution failure, and retrying the same rows cannot succeed.
var ErrBackupRowsUnrepresentable = errors.New("Cursor backup rows cannot be carried by the database rows contract")

type fileState struct {
	size     int64
	modified int64
}

func (plan *BackupPlan) Stable() bool {
	return reflect.DeepEqual(plan.dbState, databaseState(plan.dbPaths))
}

func databaseState(paths []string) map[string]fileState {
	state := make(map[string]fileState, len(paths)*2)
	for _, path := range paths {
		for _, candidate := range []string{path, path + "-wal"} {
			if info, err := os.Stat(candidate); err == nil {
				state[candidate] = fileState{size: info.Size(), modified: info.ModTime().UnixNano()}
			}
		}
	}
	return state
}

func PlanBackupContext(ctx context.Context, home, rootID string) (*BackupPlan, error) {
	rootID = canonicalCursorID(rootID)
	if !transcriptIDPattern.MatchString(rootID) {
		return nil, vendors.ErrInvalidData
	}
	scan, err := vendors.ScanSourceContext(ctx, vendors.LocalReadSource, ProjectsRoot(home))
	if err != nil || scan.SkippedTotal != 0 {
		return nil, fmt.Errorf("%w: Cursor transcript discovery incomplete", vendors.ErrInvalidData)
	}
	// An unrecognized .jsonl blocks only the family it could belong to; see
	// strayMayBelongToFamily once the family is known.
	files := filterTranscripts(scan.Files)
	ids := make([]string, 0, len(files))
	for _, path := range files {
		ids = append(ids, IDFromPath(path))
	}
	global := cursorGlobalStorage(home)
	statePath := filepath.Join(global, "state.vscdb")
	searchPath := filepath.Join(global, "conversation-search.db")
	trackingPath := filepath.Join(home, ".cursor", "ai-tracking", "ai-code-tracking.db")
	observedPaths := []string{statePath, searchPath, trackingPath}
	stores, err := cursorChatStoresContext(ctx, home, ids)
	if err != nil {
		return nil, err
	}
	for _, store := range stores {
		observedPaths = append(observedPaths, store, filepath.Join(filepath.Dir(store), "meta.json"))
	}
	baseline := databaseState(observedPaths)
	metadata, err := loadMetadataForSessionsWithLiveContext(ctx, home, ids, nil)
	if err != nil {
		return nil, err
	}
	family := cursorFamilyFilesWithMetadata(files, rootID, metadata)
	groups := map[string][]string{}
	for _, path := range family {
		id := IDFromPath(path)
		groups[id] = append(groups[id], path)
	}
	projects := ProjectsRoot(home)
	for _, path := range scan.Files {
		if !IsTranscript(path) && strayMayBelongToFamily(projects, path, rootID, groups) {
			return nil, vendors.ErrInvalidData
		}
	}
	if len(groups[rootID]) == 0 {
		return nil, fs.ErrNotExist
	}
	for id, entry := range metadata.Sessions {
		if len(groups[id]) != 0 || entry.Relationship.ParentID == "" {
			continue
		}
		seen := map[string]bool{id: true}
		for parent := entry.Relationship.ParentID; parent != "" && !seen[parent]; {
			if len(groups[parent]) != 0 {
				return nil, vendors.ErrInvalidData
			}
			seen[parent] = true
			ancestor := metadata.Lookup(parent)
			if ancestor == nil {
				break
			}
			parent = ancestor.Relationship.ParentID
		}
	}
	lane := metadata.Session(rootID).Entrypoint
	if lane != entrypointIDE && lane != entrypointCLI {
		return nil, vendors.ErrInvalidData
	}
	plan := &BackupPlan{Files: groups, Rows: map[string][]BackupRows{}, Sidecars: map[string][]byte{}, Metadata: metadata, Entrypoint: lane}
	for id := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		memberLane := metadata.Session(id).Entrypoint
		if memberLane != lane {
			return nil, vendors.ErrInvalidData
		}
		if lane == entrypointIDE {
			for _, input := range []struct {
				path, label string
				tables      []tableFilter
			}{
				{statePath, "ide-state", []tableFilter{{"composerHeaders", "LOWER(composerId) = ?", id}, {"cursorDiskKV", cursorKeyIDSegment + " = ?", id}}},
				{searchPath, "ide-search", []tableFilter{{"conversations", "LOWER(id) = ?", id}}},
				{trackingPath, "ide-tracking", []tableFilter{{"conversation_summaries", "LOWER(conversationId) = ?", id}}},
			} {
				rows, present, err := projectDatabase(ctx, input.path, input.label, id, input.tables)
				if err != nil {
					return nil, err
				}
				if present {
					plan.dbPaths = append(plan.dbPaths, input.path)
				}
				if len(rows) > 0 {
					plan.Rows[id] = append(plan.Rows[id], BackupRows{SourceKey: input.label, Bytes: rows})
				}
			}
		} else {
			stores, err := cursorChatStoresContext(ctx, home, []string{id})
			if err != nil {
				return nil, vendors.ErrInvalidData
			}
			if stores = withoutResumeStubs(ctx, stores); len(stores) != 1 {
				return nil, vendors.ErrInvalidData
			}
			if err := validateChatStore(ctx, stores[0], id); err != nil {
				return nil, err
			}
			rows, _, err := projectDatabase(ctx, stores[0], "cli-store", id, nil)
			if errors.Is(err, ErrBackupRowsUnrepresentable) {
				return nil, err
			}
			if err != nil || len(rows) == 0 {
				return nil, vendors.ErrInvalidData
			}
			plan.dbPaths = append(plan.dbPaths, stores[0])
			plan.Rows[id] = append(plan.Rows[id], BackupRows{SourceKey: "cli-store", Bytes: rows})
			metaPath := filepath.Join(filepath.Dir(stores[0]), "meta.json")
			info, err := os.Stat(metaPath)
			if err != nil || info.Size() > 1<<20 {
				return nil, vendors.ErrInvalidData
			}
			sidecar, err := os.ReadFile(metaPath)
			if err != nil || len(sidecar) > 1<<20 {
				return nil, vendors.ErrInvalidData
			}
			plan.Sidecars[id] = sidecar
			plan.dbPaths = append(plan.dbPaths, metaPath)
		}
		if len(plan.Rows[id]) == 0 || (lane == entrypointIDE && plan.Rows[id][0].SourceKey != "ide-state") {
			return nil, vendors.ErrInvalidData
		}
	}
	sort.Strings(plan.dbPaths)
	plan.dbPaths = slices.Compact(plan.dbPaths)
	confirmed, err := loadMetadataForSessionsWithLiveContext(ctx, home, ids, nil)
	if err != nil {
		return nil, err
	}
	for id := range groups {
		if !reflect.DeepEqual(metadata.Lookup(id), confirmed.Lookup(id)) {
			return nil, ErrBackupUnstable
		}
	}
	if !reflect.DeepEqual(baseline, databaseState(observedPaths)) {
		return nil, ErrBackupUnstable
	}
	plan.dbState = databaseState(plan.dbPaths)
	return plan, nil
}

func ParseBackupFilesContext(ctx context.Context, source vendors.ReadSource, files map[string][]string, metadata *vendors.SessionMetadata) ([]*vendors.ParsedSession, error) {
	ids := make([]string, 0, len(files))
	for id := range files {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parsed := make([]*vendors.ParsedSession, 0, len(ids))
	for _, id := range ids {
		item, err := parseTranscriptFragmentsSourceContext(ctx, source, files[id])
		if err != nil || item == nil || item.Session == nil || item.Session.ID != id {
			return nil, vendors.ErrInvalidData
		}
		parsed = append(parsed, item)
	}
	if err := applyCursorEnrichmentContext(ctx, parsed, metadata); err != nil {
		return nil, err
	}
	if err := applyRelationshipsContext(ctx, parsed, metadata); err != nil {
		return nil, err
	}
	return parsed, nil
}

// uuidShapedPattern finds UUID-shaped text of any version or variant,
// including ones transcriptIDPattern does not accept.
var uuidShapedPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// strayMayBelongToFamily reports whether a .jsonl file under the projects
// root that is not a recognized transcript could still hold part of the
// selected family. It could if its relative path names the root or a member,
// which covers anything under a member's transcript directory. It also could
// if the path names an ID this exporter does not accept, because metadata
// relationships for such IDs are dropped. Only a file that names neither
// cannot belong to the family and is left out.
func strayMayBelongToFamily(projects, path, rootID string, members map[string][]string) bool {
	relative, err := filepath.Rel(projects, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return true
	}
	for _, id := range uuidShapedPattern.FindAllString(relative, -1) {
		id = canonicalCursorID(id)
		if _, member := members[id]; member || id == rootID || !transcriptIDPattern.MatchString(id) {
			return true
		}
	}
	return false
}

type tableFilter struct{ table, where, id string }

func projectDatabase(ctx context.Context, path, label, id string, filters []tableFilter) ([]byte, bool, error) {
	db, err := openCursorDBContext(ctx, path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, true, err
	}
	defer tx.Rollback()
	if filters == nil {
		rows, err := tx.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
		if err != nil {
			return nil, true, err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return nil, true, err
			}
			filters = append(filters, tableFilter{table: name})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, true, err
		}
	}
	result := sessionbackupv1.DatabaseRows{Database: "cursor-" + label, Tables: []sessionbackupv1.DatabaseTable{}}
	for _, filter := range filters {
		table, err := projectTable(ctx, tx, id, filter)
		if err != nil {
			return nil, true, err
		}
		if len(table.Rows) > 0 {
			result.Tables = append(result.Tables, table)
		}
	}
	if len(result.Tables) == 0 {
		return nil, true, nil
	}
	data, err := sessionbackupv1.FreezeDatabaseRows(result)
	if errors.Is(err, sessionbackupv1.ErrInvalid) {
		// Every row here was selected for this member alone, so a rejected
		// projection cannot be carried by the contract; it is not unattributed.
		return nil, true, fmt.Errorf("%w: %w", ErrBackupRowsUnrepresentable, err)
	}
	return data, true, err
}

func projectTable(ctx context.Context, tx *sql.Tx, id string, filter tableFilter) (sessionbackupv1.DatabaseTable, error) {
	if !safeSQLName(filter.table) {
		return sessionbackupv1.DatabaseTable{}, vendors.ErrInvalidData
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, filter.table).Scan(&exists); err != nil {
		return sessionbackupv1.DatabaseTable{}, err
	}
	if exists == 0 {
		return sessionbackupv1.DatabaseTable{}, nil
	}
	query := `SELECT * FROM "` + filter.table + `"`
	args := []any(nil)
	if filter.where != "" {
		query += ` WHERE ` + filter.where
		args = append(args, filter.id)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return sessionbackupv1.DatabaseTable{}, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return sessionbackupv1.DatabaseTable{}, err
	}
	table := sessionbackupv1.DatabaseTable{Name: filter.table, Columns: columns, Rows: []sessionbackupv1.DatabaseRow{}}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return sessionbackupv1.DatabaseTable{}, err
		}
		if len(table.Rows) >= sessionbackupv1.MaxArtifacts {
			return sessionbackupv1.DatabaseTable{}, ErrBackupRowsUnrepresentable
		}
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return sessionbackupv1.DatabaseTable{}, err
		}
		row := sessionbackupv1.DatabaseRow{MemberID: id, Values: make([]sessionbackupv1.DatabaseValue, len(values))}
		for i, value := range values {
			converted, err := databaseValue(value)
			if err != nil {
				return sessionbackupv1.DatabaseTable{}, err
			}
			row.Values[i] = converted
		}
		table.Rows = append(table.Rows, row)
	}
	return table, rows.Err()
}

func safeSQLName(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r != '_' && (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

func databaseValue(value any) (sessionbackupv1.DatabaseValue, error) {
	switch typed := value.(type) {
	case nil:
		return sessionbackupv1.DatabaseValue{Type: "null", Value: ""}, nil
	case int64:
		return sessionbackupv1.DatabaseValue{Type: "integer", Value: strconv.FormatInt(typed, 10)}, nil
	case float64:
		return sessionbackupv1.DatabaseValue{Type: "real", Value: fmt.Sprintf("%016x", math.Float64bits(typed))}, nil
	case string:
		return sessionbackupv1.DatabaseValue{Type: "text", Value: typed}, nil
	case []byte:
		return sessionbackupv1.DatabaseValue{Type: "blob", Value: base64.StdEncoding.EncodeToString(typed)}, nil
	default:
		return sessionbackupv1.DatabaseValue{}, vendors.ErrInvalidData
	}
}

func validateChatStore(ctx context.Context, path, id string) error {
	db, err := openCursorDBContext(ctx, path)
	if err != nil {
		return err
	}
	defer db.Close()
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = '0'`).Scan(&raw); err != nil {
		return err
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil {
		return vendors.ErrInvalidData
	}
	var meta struct {
		AgentID string `json:"agentId"`
	}
	if json.Unmarshal(decoded, &meta) != nil || canonicalCursorID(meta.AgentID) != id {
		return vendors.ErrInvalidData
	}
	return nil
}
