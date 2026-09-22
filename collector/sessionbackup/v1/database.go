package sessionbackupv1

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
)

// DatabaseRows is the canonical projection of rows selected from a shared
// metadata database. It never contains the database file itself.
type DatabaseRows struct {
	SchemaVersion string          `json:"schemaVersion"`
	Database      string          `json:"database"`
	Tables        []DatabaseTable `json:"tables"`
}

type DatabaseTable struct {
	Name    string        `json:"name"`
	Columns []string      `json:"columns"`
	Rows    []DatabaseRow `json:"rows"`
}

type DatabaseRow struct {
	MemberID string          `json:"memberId"`
	Values   []DatabaseValue `json:"values"`
}

// DatabaseValue preserves SQLite storage classes without JSON number loss.
// Value is decimal for integer, big-endian IEEE-754 bits for real, UTF-8 for
// text, RFC 4648 base64 for blob, and empty for null.
type DatabaseValue struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

func FreezeDatabaseRows(rows DatabaseRows) ([]byte, error) {
	if !databaseRowsWithinItemLimit(rows) {
		return nil, fmt.Errorf("%w: database rows exceed item limit", ErrInvalid)
	}
	rows = cloneDatabaseRows(rows)
	rows.SchemaVersion = DatabaseRowsVersion
	for tableIndex := range rows.Tables {
		table := &rows.Tables[tableIndex]
		sort.Slice(table.Rows, func(i, j int) bool {
			left, _ := json.Marshal(table.Rows[i])
			right, _ := json.Marshal(table.Rows[j])
			return bytes.Compare(left, right) < 0
		})
	}
	sort.Slice(rows.Tables, func(i, j int) bool { return rows.Tables[i].Name < rows.Tables[j].Name })
	if err := ValidateDatabaseRows(rows, ""); err != nil {
		return nil, err
	}
	return marshalBoundedDocument(rows, "database rows")
}

func DecodeDatabaseRows(data []byte, memberID string) (DatabaseRows, error) {
	if err := validateDocumentBounds(data); err != nil {
		return DatabaseRows{}, fmt.Errorf("%w: database rows bounds: %v", ErrInvalid, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var rows DatabaseRows
	if err := decoder.Decode(&rows); err != nil {
		return DatabaseRows{}, fmt.Errorf("%w: database rows decode: %v", ErrInvalid, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return DatabaseRows{}, fmt.Errorf("%w: trailing database rows value", ErrInvalid)
	}
	if err := ValidateDatabaseRows(rows, memberID); err != nil {
		return DatabaseRows{}, err
	}
	canonical, err := json.Marshal(rows)
	if err != nil || !bytes.Equal(canonical, data) {
		return DatabaseRows{}, fmt.Errorf("%w: non-canonical database rows", ErrInvalid)
	}
	return rows, nil
}

func cloneDatabaseRows(rows DatabaseRows) DatabaseRows {
	rows.Tables = append([]DatabaseTable(nil), rows.Tables...)
	for tableIndex := range rows.Tables {
		table := &rows.Tables[tableIndex]
		table.Columns = append([]string(nil), table.Columns...)
		table.Rows = append([]DatabaseRow(nil), table.Rows...)
		for rowIndex := range table.Rows {
			table.Rows[rowIndex].Values = append([]DatabaseValue(nil), table.Rows[rowIndex].Values...)
		}
	}
	return rows
}

func ValidateDatabaseRows(rows DatabaseRows, memberID string) error {
	if rows.SchemaVersion != DatabaseRowsVersion || !identifier(rows.Database) || len(rows.Tables) == 0 || !databaseRowsWithinItemLimit(rows) {
		return fmt.Errorf("%w: invalid database row envelope", ErrInvalid)
	}
	previousTable := ""
	for _, table := range rows.Tables {
		if table.Name == "" || !valueText(table.Name) || table.Name <= previousTable || len(table.Columns) == 0 || len(table.Rows) == 0 {
			return fmt.Errorf("%w: invalid or unsorted database table", ErrInvalid)
		}
		seenColumns := map[string]bool{}
		for _, column := range table.Columns {
			if column == "" || !valueText(column) || seenColumns[column] {
				return fmt.Errorf("%w: invalid database column", ErrInvalid)
			}
			seenColumns[column] = true
		}
		var previous []byte
		for _, row := range table.Rows {
			if !identifier(row.MemberID) || (memberID != "" && row.MemberID != memberID) || len(row.Values) != len(table.Columns) {
				return fmt.Errorf("%w: cross-session or malformed database row", ErrInvalid)
			}
			for _, value := range row.Values {
				if !validDatabaseValue(value) {
					return fmt.Errorf("%w: invalid database value", ErrInvalid)
				}
			}
			canonical, _ := json.Marshal(row)
			if previous != nil && bytes.Compare(previous, canonical) >= 0 {
				return fmt.Errorf("%w: database rows are not strictly ordered", ErrInvalid)
			}
			previous = canonical
		}
		previousTable = table.Name
	}
	return nil
}

func databaseRowsWithinItemLimit(rows DatabaseRows) bool {
	total := len(rows.Tables)
	if total > fullsessionv1.MaxItems {
		return false
	}
	for _, table := range rows.Tables {
		for _, count := range []int{len(table.Columns), len(table.Rows)} {
			if count > fullsessionv1.MaxItems-total {
				return false
			}
			total += count
		}
		for _, row := range table.Rows {
			if len(row.Values) > fullsessionv1.MaxItems-total {
				return false
			}
			total += len(row.Values)
		}
	}
	return true
}

func validDatabaseValue(value DatabaseValue) bool {
	switch value.Type {
	case "null":
		return value.Value == ""
	case "integer":
		parsed, err := strconv.ParseInt(value.Value, 10, 64)
		return err == nil && strconv.FormatInt(parsed, 10) == value.Value
	case "real":
		decoded, err := hex.DecodeString(value.Value)
		return err == nil && len(decoded) == 8 && strings.ToLower(value.Value) == value.Value
	case "text":
		return valueText(value.Value)
	case "blob":
		decoded, err := base64.StdEncoding.DecodeString(value.Value)
		return err == nil && len(value.Value) <= 1<<20 && base64.StdEncoding.EncodeToString(decoded) == value.Value
	default:
		return false
	}
}

func valueText(value string) bool {
	return utf8.ValidString(value) && len(value) <= 1<<20
}
