package opencode

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
	backup "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

// FamilyCapture keeps the database connection used to detect writes during
// preparation. Rows and parsed sessions come from one read transaction.
type FamilyCapture struct {
	Parsed  []*vendors.ParsedSession
	Rows    map[string][]byte
	db      *sql.DB
	version int64
}

var ErrUnattributable = errors.New("OpenCode rows cannot be attributed to a complete family")
var ErrUnstable = errors.New("OpenCode database changed during capture")

func (capture *FamilyCapture) Close() error { return capture.db.Close() }

func (capture *FamilyCapture) Stable(ctx context.Context) bool {
	var current int64
	return capture.db.QueryRowContext(ctx, "PRAGMA data_version").Scan(&current) == nil && current == capture.version
}

func CaptureFamilyContext(ctx context.Context, rootID string) (_ *FamilyCapture, err error) {
	db, err := openContext(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = db.Close()
		}
	}()
	db.SetMaxOpenConns(1)
	var version int64
	if err = db.QueryRowContext(ctx, "PRAGMA data_version").Scan(&version); err != nil {
		return nil, err
	}
	source, err := sessionSourceContext(ctx, db)
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	parsed, skipped, err := loadTxContext(ctx, tx, source, activeFamiliesQuery+" AND selected_roots.id = ?", rootID)
	if err != nil {
		return nil, err
	}
	if len(skipped) != 0 || len(parsed) == 0 || parsed[0].Session.ID != rootID {
		return nil, ErrUnattributable
	}
	result := &FamilyCapture{Parsed: parsed, Rows: make(map[string][]byte, len(parsed)), db: db, version: version}
	for _, member := range parsed {
		rows, projectErr := projectMember(ctx, tx, member.Session.ID)
		if projectErr != nil {
			return nil, projectErr
		}
		result.Rows[member.Session.ID] = rows
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if !result.Stable(ctx) {
		return nil, ErrUnstable
	}
	return result, nil
}

func projectMember(ctx context.Context, tx *sql.Tx, memberID string) ([]byte, error) {
	tables, err := tx.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var names []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			tables.Close()
			return nil, err
		}
		names = append(names, name)
	}
	err = tables.Err()
	tables.Close()
	if err != nil {
		return nil, err
	}
	available := map[string]bool{}
	for _, name := range names {
		available[name] = true
	}
	sessionTable := "session"
	if available["session_v2"] {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_v2 WHERE id = ?`, memberID).Scan(&count); err != nil {
			return nil, err
		}
		if count > 0 {
			sessionTable = "session_v2"
		}
	}
	if !available[sessionTable] {
		return nil, ErrUnattributable
	}
	projection := backup.DatabaseRows{Database: "opencode", Tables: []backup.DatabaseTable{}}
	for _, name := range names {
		if name == "session" || name == "session_v2" {
			if name != sessionTable {
				continue
			}
		}
		columns, err := tableColumns(ctx, tx, name)
		if err != nil {
			return nil, err
		}
		filter := ""
		switch {
		case name == sessionTable:
			filter = `id = ?`
		case hasColumn(columns, "session_id"):
			filter = `session_id = ?`
		case hasColumn(columns, "message_id") && available["message"]:
			filter = `message_id IN (SELECT id FROM message WHERE session_id = ?)`
		default:
			continue
		}
		query := `SELECT * FROM ` + quoteName(name) + ` WHERE ` + filter
		rows, err := tx.QueryContext(ctx, query, memberID)
		if err != nil {
			return nil, err
		}
		table := backup.DatabaseTable{Name: name, Columns: columns, Rows: []backup.DatabaseRow{}}
		for rows.Next() {
			if err := ctx.Err(); err != nil {
				rows.Close()
				return nil, err
			}
			if len(table.Rows) >= backup.MaxArtifacts {
				rows.Close()
				return nil, fmt.Errorf("OpenCode row limit exceeded")
			}
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				return nil, err
			}
			row := backup.DatabaseRow{MemberID: memberID, Values: make([]backup.DatabaseValue, len(values))}
			for i, value := range values {
				converted, err := databaseValue(value)
				if err != nil {
					rows.Close()
					return nil, err
				}
				row.Values[i] = converted
			}
			table.Rows = append(table.Rows, row)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(table.Rows) > 0 {
			projection.Tables = append(projection.Tables, table)
		}
	}
	if len(projection.Tables) == 0 {
		return nil, ErrUnattributable
	}
	return backup.FreezeDatabaseRows(projection)
}

func tableColumns(ctx context.Context, tx *sql.Tx, name string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+quoteName(name)+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var index int
		var column, kind string
		var notNull, primary int
		var defaultValue any
		if err := rows.Scan(&index, &column, &kind, &notNull, &defaultValue, &primary); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

func hasColumn(columns []string, name string) bool {
	for _, column := range columns {
		if column == name {
			return true
		}
	}
	return false
}

func quoteName(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func databaseValue(value any) (backup.DatabaseValue, error) {
	switch v := value.(type) {
	case nil:
		return backup.DatabaseValue{Type: "null"}, nil
	case int64:
		return backup.DatabaseValue{Type: "integer", Value: strconv.FormatInt(v, 10)}, nil
	case float64:
		return backup.DatabaseValue{Type: "real", Value: fmt.Sprintf("%016x", math.Float64bits(v))}, nil
	case string:
		return backup.DatabaseValue{Type: "text", Value: v}, nil
	case []byte:
		return backup.DatabaseValue{Type: "blob", Value: base64.StdEncoding.EncodeToString(v)}, nil
	default:
		return backup.DatabaseValue{}, fmt.Errorf("unsupported OpenCode SQLite value %T", v)
	}
}
