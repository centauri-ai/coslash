package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidSession      = errors.New("invalid OpenCode session")
	ErrSessionMissing      = errors.New("OpenCode session missing")
	ErrSessionActive       = errors.New("OpenCode session active")
	ErrSessionUnverified   = errors.New("OpenCode session safety unverified")
	ErrSessionDeleteFailed = errors.New("OpenCode session deletion failed")
	deleteProcesses        = listOpenCodeProcessesContext
	runSessionDelete       = deleteDatabaseSession
)

type deletionJournal struct {
	Version            int
	Database, Identity string
	Family             map[string]bool
	Keys               []deletionKey
	Files              []string
}

type deletionConnection struct {
	conn        *sql.Conn
	original    os.FileInfo
	journal     *deletionJournal
	journalPath string
}
type deletionConnectionKey struct{}

func DeleteSession(ctx context.Context, home, id string) error {
	if !sessionIDPattern.MatchString(id) || len(id) > 128 || !filepath.IsAbs(home) {
		return ErrInvalidSession
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	path, dataRoot, err := deletionRoots(ctx, home)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	journalPath := filepath.Join(home, ".coslash", "opencode-deletions", id+".json")
	for _, p := range []string{path, dataRoot, journalPath} {
		if err := checkDeletionPath(home, p); err != nil {
			return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
		}
	}
	original, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrSessionMissing
	}
	if err != nil || !original.Mode().IsRegular() {
		return ErrSessionUnverified
	}
	identity, err := deletionFileIdentity(original, path)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	uri, _ := url.Parse(readOnlyDatabaseDSN(path))
	query := uri.Query()
	query.Set("mode", "rw")
	query.Del("_query_only")
	query.Set("_pragma", "foreign_keys(1)")
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	defer conn.Close()
	journal := &deletionJournal{Version: 1, Database: path, Identity: identity}
	bound := deletionConnection{conn: conn, original: original, journal: journal, journalPath: journalPath}
	if err := bound.checkIdentity(); err != nil {
		return err
	}
	if err := validateSchemaContext(ctx, conn); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	pending, err := readDeletionJSON(ctx, journalPath, 16<<20, journal)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	if pending {
		if journal.Version != 1 || journal.Database != path || journal.Identity != identity || !journal.Family[id] || len(journal.Family) > 4096 || len(journal.Keys) > 64 || len(journal.Files) > 10000 {
			return ErrSessionUnverified
		}
		for member := range journal.Family {
			if !sessionIDPattern.MatchString(member) || len(member) > 128 {
				return ErrSessionUnverified
			}
		}
		for _, key := range journal.Keys {
			if !validDeletionKey(key) || len(key.IDs) > 100000 {
				return ErrSessionUnverified
			}
		}
		for _, file := range journal.Files {
			if !ownedDeletionPath(home, dataRoot, file, journal.Family) {
				return ErrSessionUnverified
			}
		}
	}
	family, err := deletionFamily(ctx, conn, id)
	missing := errors.Is(err, ErrSessionMissing)
	if err != nil && (!pending || !missing) {
		return err
	}
	if !missing {
		if pending && !sameDeletionFamily(family, journal.Family) {
			return ErrSessionUnverified
		}
		journal.Family = family
		journal.Keys, err = databaseDeletionKeys(ctx, conn, family)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
		}
	}
	if err := checkDeletionProcesses(ctx, journal.Family); err != nil {
		return err
	}
	files, err := deletionArtifacts(ctx, home, dataRoot, journal.Family)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	for _, file := range journal.Files {
		if err := checkDeletionArtifact(ctx, home, dataRoot, file, journal.Family); err != nil {
			return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
		}
		files = append(files, file)
	}
	sort.Strings(files)
	journal.Files = nil
	for _, file := range files {
		if len(journal.Files) == 0 || journal.Files[len(journal.Files)-1] != file {
			journal.Files = append(journal.Files, file)
		}
	}
	if len(journal.Files) > 10000 {
		return ErrSessionUnverified
	}
	if err := checkDeletionProcesses(ctx, journal.Family); err != nil {
		return err
	}
	if err := bound.checkIdentity(); err != nil {
		return err
	}
	if err := saveDeletionJournal(ctx, journalPath, journal); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionDeleteFailed, err)
	}
	ctx = context.WithValue(ctx, deletionConnectionKey{}, bound)
	if !missing {
		if err := runSessionDelete(ctx, home, path, id, journal.Family); err != nil {
			return fmt.Errorf("%w: %w", ErrSessionDeleteFailed, err)
		}
	}
	if err := bound.checkIdentity(); err != nil {
		return err
	}
	if err := verifyDeletionKeys(ctx, conn, journal.Keys); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionDeleteFailed, err)
	}
	if err := checkDeletionProcesses(ctx, journal.Family); err != nil {
		return err
	}
	for _, file := range journal.Files {
		if err := checkDeletionArtifact(ctx, home, dataRoot, file, journal.Family); err != nil {
			return fmt.Errorf("%w: %w", ErrSessionDeleteFailed, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %w", ErrSessionDeleteFailed, err)
		}
	}
	remaining, err := deletionArtifacts(ctx, home, dataRoot, journal.Family)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionDeleteFailed, err)
	}
	for _, file := range append(journal.Files, remaining...) {
		if _, err := os.Lstat(file); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: owned artifact remains", ErrSessionDeleteFailed)
		}
	}
	if err := verifyDeletionKeys(ctx, conn, journal.Keys); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionDeleteFailed, err)
	}
	if err := bound.checkIdentity(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(journalPath); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionDeleteFailed, err)
	}
	return syncDeletionDirectory(filepath.Dir(journalPath))
}

func (b deletionConnection) checkIdentity() error {
	if err := checkDeletionPath("", b.journal.Database); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionDeleteFailed, err)
	}
	info, err := os.Lstat(b.journal.Database)
	if err != nil || !os.SameFile(info, b.original) {
		return fmt.Errorf("%w: database path changed", ErrSessionDeleteFailed)
	}
	identity, err := deletionFileIdentity(info, b.journal.Database)
	if err != nil || identity != b.journal.Identity {
		return fmt.Errorf("%w: database identity changed", ErrSessionDeleteFailed)
	}
	return nil
}

func sameDeletionFamily(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if !b[id] {
			return false
		}
	}
	return true
}

func deletionRoots(ctx context.Context, home string) (path, dataRoot string, err error) {
	path, err = effectiveDatabaseRoot(ctx, home)
	dataRoot = filepath.Join(home, ".local", "share", "opencode")
	if data := os.Getenv("XDG_DATA_HOME"); data != "" {
		dataRoot = filepath.Join(data, "opencode")
	}
	return path, dataRoot, err
}

func checkDeletionPath(_ string, path string) error {
	if !filepath.IsAbs(path) {
		return ErrSessionUnverified
	}
	volume := filepath.VolumeName(path)
	current := volume + string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(path), current), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if runtime.GOOS == "darwin" && (current == "/var" || current == "/tmp" || current == "/etc") {
				resolved, err := filepath.EvalSymlinks(current)
				if err == nil && resolved == "/private"+current {
					continue
				}
			}
			return fmt.Errorf("symlink in deletion path")
		}
	}
	return nil
}

func deletionFamily(ctx context.Context, db deletionReader, id string) (map[string]bool, error) {
	sources := []string{}
	for _, table := range []string{"session", "session_v2"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, table).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			sources = append(sources, `SELECT id,parent_id FROM `+table)
		}
	}
	if len(sources) == 0 {
		return nil, ErrSessionUnverified
	}
	rows, err := db.QueryContext(ctx, `WITH RECURSIVE sessions AS (`+strings.Join(sources, ` UNION ALL `)+`), family(id) AS (
 SELECT ? UNION SELECT sessions.id FROM sessions JOIN family ON sessions.parent_id=family.id LIMIT 4097
 ) SELECT id,parent_id FROM sessions WHERE id IN (SELECT id FROM family) LIMIT 8193`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	family := map[string]bool{}
	parents := map[string]string{}
	for rows.Next() {
		var member string
		var parent sql.NullString
		if err := rows.Scan(&member, &parent); err != nil {
			return nil, err
		}
		if !sessionIDPattern.MatchString(member) || len(member) > 128 {
			return nil, ErrSessionUnverified
		}
		if old, ok := parents[member]; ok && old != parent.String {
			return nil, ErrSessionUnverified
		}
		parents[member] = parent.String
		family[member] = true
		if len(family) > 4096 {
			return nil, ErrSessionUnverified
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !family[id] {
		return nil, ErrSessionMissing
	}
	if parents[id] != "" {
		return nil, ErrInvalidSession
	}
	return family, nil
}

func checkDeletionProcesses(ctx context.Context, family map[string]bool) error {
	processes, err := deleteProcesses(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	for _, process := range processes {
		if family[process.sessionID] {
			return ErrSessionActive
		}
	}
	// A TUI can switch sessions after launch; run/serve/desktop may host sessions
	// without an ID in argv. Absence of a match cannot establish closure.
	if len(processes) > 0 {
		return ErrSessionUnverified
	}
	return ctx.Err()
}

// The vendor CLI applies migrations on startup. Delete directly so an older
// collector-supported database cannot trigger unrelated migrations or hooks.
func deleteDatabaseSession(ctx context.Context, _ string, path, id string, expected map[string]bool) error {
	bound, ok := ctx.Value(deletionConnectionKey{}).(deletionConnection)
	if !ok || bound.journal.Database != path {
		return ErrSessionUnverified
	}
	if err := bound.checkIdentity(); err != nil {
		return err
	}
	tx, err := bound.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	family, err := deletionFamily(ctx, tx, id)
	if err != nil {
		return err
	}
	if !sameDeletionFamily(family, expected) {
		return ErrSessionUnverified
	}
	keys, err := databaseDeletionKeys(ctx, tx, family)
	if err != nil {
		return err
	}
	if err := checkDeletionProcesses(ctx, family); err != nil {
		return err
	}
	if err := bound.checkIdentity(); err != nil {
		return err
	}
	bound.journal.Keys = keys
	if err := saveDeletionJournal(ctx, bound.journalPath, bound.journal); err != nil {
		return err
	}
	if err := checkDeletionProcesses(ctx, family); err != nil {
		return err
	}
	if err := bound.checkIdentity(); err != nil {
		return err
	}
	for _, key := range keys {
		for id := range key.IDs {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+quoteDeletionName(key.Table)+` WHERE `+quoteDeletionName(key.Column)+`=?`, id); err != nil {
				return err
			}
		}
	}
	if err := verifyDeletionKeys(ctx, tx, keys); err != nil {
		return err
	}
	return tx.Commit()
}

type deletionReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	PrepareContext(context.Context, string) (*sql.Stmt, error)
}

type deletionKey struct {
	Table, Column string
	IDs           map[string]bool
}

func quoteDeletionName(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func databaseDeletionKeys(ctx context.Context, db deletionReader, family map[string]bool) ([]deletionKey, error) {
	messages := map[string]bool{}
	// Message IDs must be retained before removing messages, including layouts
	// where parts have a message_id but no independent session_id column.
	for _, table := range []string{"message", "session_message"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, table).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		for member := range family {
			rows, err := db.QueryContext(ctx, `SELECT id FROM `+quoteDeletionName(table)+` WHERE session_id=?`, member)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return nil, err
				}
				messages[id] = true
				if len(messages) > 100000 {
					rows.Close()
					return nil, fmt.Errorf("message inventory exceeds deletion limit")
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		return nil, err
	}
	tables := []string{}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			return nil, err
		}
		tables = append(tables, table)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	keys := []deletionKey{}
	for _, table := range tables {
		cols, err := db.QueryContext(ctx, `PRAGMA table_info(`+quoteDeletionName(table)+`)`)
		if err != nil {
			return nil, err
		}
		for cols.Next() {
			var n, pk, notnull int
			var name, typ string
			var defaultValue any
			if err := cols.Scan(&n, &name, &typ, &notnull, &defaultValue, &pk); err != nil {
				cols.Close()
				return nil, err
			}
			ids := family
			owner := name == "session_id" || name == "aggregate_id"
			if name == "message_id" {
				owner = true
				ids = messages
			}
			if name == "id" || name == "parent_id" {
				owner = table == "session" || table == "session_v2"
			}
			if owner {
				if !validDeletionKey(deletionKey{Table: table, Column: name}) {
					cols.Close()
					return nil, fmt.Errorf("unsupported session storage table")
				}
				keys = append(keys, deletionKey{Table: table, Column: name, IDs: ids})
			}
		}
		err = cols.Err()
		cols.Close()
		if err != nil {
			return nil, err
		}
	}
	// Delete dependents before parent rows even when an older database has no
	// cascading foreign keys. The two session representations remain scoped.
	ordered := []deletionKey{}
	for _, table := range []string{"part", "todo", "session_message", "session_input", "session_context_epoch", "session_share", "message", "event", "event_sequence", "session_v2", "session"} {
		for _, key := range keys {
			if key.Table == table {
				ordered = append(ordered, key)
			}
		}
	}
	return ordered, nil
}

func verifyDeletionKeys(ctx context.Context, db deletionReader, keys []deletionKey) error {
	for _, key := range keys {
		for id := range key.IDs {
			var count int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteDeletionName(key.Table)+` WHERE `+quoteDeletionName(key.Column)+`=?`, id).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return fmt.Errorf("session data remains in %s", key.Table)
			}
		}
	}
	return nil
}

func readDeletionJSON(ctx context.Context, path string, limit int64, value any) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := checkDeletionPath("", path); err != nil {
		return false, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return false, ErrSessionUnverified
	}
	decoder := json.NewDecoder(io.LimitReader(file, limit+1))
	if err := decoder.Decode(value); err != nil {
		return false, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return false, ErrSessionUnverified
	}
	return true, ctx.Err()
}

func saveDeletionJournal(ctx context.Context, path string, journal *deletionJournal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkDeletionPath("", path); err != nil {
		return err
	}
	data, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	if len(data) > 16<<20 {
		return ErrSessionUnverified
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	for _, parent := range []string{filepath.Dir(directory), filepath.Dir(filepath.Dir(directory))} {
		if err := syncDeletionDirectory(parent); err != nil {
			return err
		}
	}
	file, err := os.CreateTemp(directory, ".delete-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := checkDeletionPath("", path); err != nil {
		return err
	}
	if err := replaceFile(file.Name(), path); err != nil {
		return err
	}
	return syncDeletionDirectory(directory)
}

func validDeletionKey(key deletionKey) bool {
	switch key.Table {
	case "session", "session_v2":
		return key.Column == "id" || key.Column == "parent_id"
	case "event", "event_sequence":
		return key.Column == "aggregate_id"
	case "part":
		return key.Column == "session_id" || key.Column == "message_id"
	case "message", "todo", "session_message", "session_input", "session_context_epoch", "session_share":
		return key.Column == "session_id"
	}
	return false
}

func deletionDirectory(path string) ([]os.DirEntry, error) {
	if err := checkDeletionPath("", path); err != nil {
		return nil, err
	}
	directory, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(10001)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) > 10000 {
		return nil, ErrSessionUnverified
	}
	return entries, nil
}

func ownedDeletionPath(home, dataRoot, path string, family map[string]bool) bool {
	parent := filepath.Dir(path)
	name := filepath.Base(path)
	if sameDirectory(parent, filepath.Join(home, ".coslash", "opencode-permissions")) {
		return name != "." && name != ".." && len(name) <= 256
	}
	if sameDirectory(parent, filepath.Join(home, ".coslash", "opencode-clients")) || sameDirectory(parent, filepath.Join(dataRoot, "storage", "session_diff")) {
		return strings.HasSuffix(name, ".json") && family[strings.TrimSuffix(name, ".json")]
	}
	return false
}

func checkDeletionArtifact(ctx context.Context, home, dataRoot, path string, family map[string]bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ownedDeletionPath(home, dataRoot, path, family) {
		return ErrSessionUnverified
	}
	if err := checkDeletionPath("", path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return ErrSessionUnverified
	}
	if sameDirectory(filepath.Dir(path), filepath.Join(home, ".coslash", "opencode-permissions")) {
		var record pendingPermission
		if _, err := readDeletionJSON(ctx, path, 64<<10, &record); err != nil {
			return err
		}
		if !family[record.SessionID] {
			return ErrSessionUnverified
		}
	}
	return nil
}

func deletionArtifacts(ctx context.Context, home, dataRoot string, family map[string]bool) ([]string, error) {
	storage := filepath.Join(dataRoot, "storage")
	entries, err := deletionDirectory(storage)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		path := filepath.Join(storage, entry.Name())
		if err := checkDeletionPath("", path); err != nil {
			return nil, err
		}
		switch entry.Name() {
		case "migration":
			info, err := entry.Info()
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() || info.Size() > 16 {
				return nil, ErrSessionUnverified
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			marker := strings.TrimSpace(string(data))
			if marker == "" || strings.Trim(marker, "0123456789") != "" {
				return nil, ErrSessionUnverified
			}
		case "session_diff":
			if !entry.IsDir() {
				return nil, ErrSessionUnverified
			}
			diffs, err := deletionDirectory(path)
			if err != nil {
				return nil, err
			}
			for _, diff := range diffs {
				id := strings.TrimSuffix(diff.Name(), ".json")
				info, err := diff.Info()
				if err != nil {
					return nil, err
				}
				if !strings.HasSuffix(diff.Name(), ".json") || !sessionIDPattern.MatchString(id) || len(id) > 128 || !info.Mode().IsRegular() {
					return nil, ErrSessionUnverified
				}
			}
		default:
			return nil, fmt.Errorf("unverified filesystem session store")
		}
	}
	files := []string{}
	for member := range family {
		files = append(files, filepath.Join(home, ".coslash", "opencode-clients", member+".json"), filepath.Join(storage, "session_diff", member+".json"))
	}
	permissions := filepath.Join(home, ".coslash", "opencode-permissions")
	requests, err := deletionDirectory(permissions)
	if err != nil {
		return nil, err
	}
	var total int64
	for _, request := range requests {
		info, err := request.Info()
		if err != nil {
			return nil, err
		}
		total += info.Size()
		if total > 16<<20 || !info.Mode().IsRegular() {
			return nil, ErrSessionUnverified
		}
		path := filepath.Join(permissions, request.Name())
		var record pendingPermission
		if _, err := readDeletionJSON(ctx, path, 64<<10, &record); err != nil {
			return nil, err
		}
		if !sessionIDPattern.MatchString(record.SessionID) || len(record.SessionID) > 128 {
			return nil, ErrSessionUnverified
		}
		if family[record.SessionID] {
			files = append(files, path)
		}
	}
	for _, path := range files {
		if err := checkDeletionArtifact(ctx, home, dataRoot, path, family); err != nil {
			return nil, err
		}
	}
	return files, nil
}
