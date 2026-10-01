package cursor

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DeleteCause is a sentinel cause that callers can match with errors.Is.
type DeleteCause string

func (cause DeleteCause) Error() string { return string(cause) }

const (
	ErrDeleteMissing    DeleteCause = "Cursor session missing"
	ErrDeleteActive     DeleteCause = "Cursor session active"
	ErrDeleteUnverified DeleteCause = "Cursor session safety unverified"
	ErrDeleteFailed     DeleteCause = "Cursor session deletion failed"
	ErrDeleteInvalid    DeleteCause = "invalid Cursor session"
)

// DeleteSession removes a closed CLI session family from active local storage.
// IDE references are refused until scoped IDE database deletion is supported.
func DeleteSession(ctx context.Context, home, id string) error {
	return deleteSession(ctx, home, id, probeCursorDeletion, os.Remove)
}

func deleteSession(ctx context.Context, home, id string, probe func(context.Context) error, remove func(string) error) error {
	if !transcriptIDPattern.MatchString(id) || !filepath.IsAbs(home) {
		return ErrDeleteInvalid
	}
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDeleteUnverified, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrDeleteUnverified, err)
	}
	verifyClosed := func() error {
		if err := probe(ctx); err != nil {
			if errors.Is(err, ErrDeleteActive) {
				return err
			}
			return fmt.Errorf("%w: %w", ErrDeleteUnverified, err)
		}
		return nil
	}
	if err := verifyClosed(); err != nil {
		return err
	}
	paths, err := cursorDeleteInventory(ctx, home, canonicalCursorID(id))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDeleteUnverified, err)
	}
	if len(paths) == 0 {
		return ErrDeleteMissing
	}
	// Probe after inventory, immediately before the first mutation.
	if err := verifyClosed(); err != nil {
		return err
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %w", ErrDeleteFailed, err)
		}
		if err := cursorDeleteContained(home, path); err != nil {
			return fmt.Errorf("%w: %w", ErrDeleteFailed, err)
		}
		if err := remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %w", ErrDeleteFailed, err)
		}
	}
	remaining, err := cursorDeleteInventory(ctx, home, canonicalCursorID(id))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDeleteFailed, err)
	}
	if len(remaining) != 0 || ctx.Err() != nil {
		return ErrDeleteFailed
	}
	return nil
}

func probeCursorDeletion(ctx context.Context) error {
	command := exec.CommandContext(ctx, "ps", "-ww", "-axo", "pid=,command=")
	if runtime.GOOS == "windows" {
		command = exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; try { $processes=@(Get-CimInstance Win32_Process -ErrorAction Stop | Where-Object { $_.Name -match '^(Cursor.*|cursor-agent|agent|node)\.exe$' } | Select-Object Name,ExecutablePath,CommandLine); ConvertTo-Json -InputObject $processes -Compress } catch { exit 1 }`)
	}
	command.WaitDelay = time.Second
	output, err := command.Output()
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return cursorDeletionWindowsProcesses(string(output))
	}
	return cursorDeletionProcesses(string(output))
}

func cursorDeletionWindowsProcesses(output string) error {
	var processes []struct{ Name, ExecutablePath, CommandLine string }
	if err := json.Unmarshal([]byte(output), &processes); err != nil {
		return ErrDeleteUnverified
	}
	if processes == nil {
		return ErrDeleteUnverified
	}
	for _, process := range processes {
		name := strings.ToLower(process.Name)
		if strings.HasPrefix(name, "cursor") || name == "agent.exe" {
			return ErrDeleteActive
		}
		if name != "node.exe" || process.ExecutablePath == "" || process.CommandLine == "" {
			return ErrDeleteUnverified
		}
		path := strings.ToLower(strings.ReplaceAll(process.ExecutablePath+" "+process.CommandLine, `\`, "/"))
		if strings.Contains(path, "/cursor-agent/") || strings.Contains(path, "/cursor.app/") {
			return ErrDeleteActive
		}
	}
	return nil
}

func cursorDeletionProcesses(output string) error {
	if strings.TrimSpace(output) == "" {
		return ErrDeleteUnverified
	}
	for line := range strings.SplitSeq(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			if strings.TrimSpace(line) != "" {
				return ErrDeleteUnverified
			}
			continue
		}
		if pid, err := strconv.Atoi(fields[0]); err != nil || pid <= 0 {
			return ErrDeleteUnverified
		}
		executable := strings.ToLower(filepath.Base(fields[1]))
		// Refuse every Cursor process rather than guessing which chat it may open next.
		if executable == "cursor" || executable == "cursor-agent" || executable == "agent" || strings.HasPrefix(executable, "cursor helper") {
			return ErrDeleteActive
		}
		for _, argument := range fields[1:] {
			path := filepath.ToSlash(argument)
			if strings.Contains(path, "/cursor-agent/") || strings.Contains(path, "/Cursor.app/") {
				return ErrDeleteActive
			}
		}
	}
	return nil
}

func cursorDeleteContained(home, path string) error {
	relative, err := filepath.Rel(home, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ErrDeleteUnverified
	}
	for current := path; current != home; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return ErrDeleteUnverified
		}
	}
	return nil
}

func cursorDeleteWalk(ctx context.Context, home, root string) ([]string, error) {
	if err := cursorDeleteContained(home, root); err != nil {
		return nil, err
	}
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) && path == root {
			return nil
		}
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 || (!entry.IsDir() && !entry.Type().IsRegular()) {
			return ErrDeleteUnverified
		}
		if len(paths) >= 100000 {
			return ErrDeleteUnverified
		}
		paths = append(paths, path)
		return nil
	})
	return paths, err
}

func cursorDeleteInventory(ctx context.Context, home, id string) ([]string, error) {
	chats := filepath.Join(home, ".cursor", "chats")
	projects := ProjectsRoot(home)
	chatPaths, err := cursorDeleteWalk(ctx, home, chats)
	if err != nil {
		return nil, err
	}
	transcriptPaths, err := cursorDeleteWalk(ctx, home, projects)
	if err != nil {
		return nil, err
	}
	parents := map[string]string{}
	for _, path := range chatPaths {
		relative, _ := filepath.Rel(chats, path)
		parts := strings.Split(relative, string(filepath.Separator))
		if len(parts) != 3 || parts[2] != "store.db" {
			continue
		}
		if !transcriptIDPattern.MatchString(parts[1]) {
			return nil, ErrDeleteUnverified
		}
		child, parent, err := cursorDeleteCLIParent(ctx, path)
		if err != nil {
			return nil, err
		}
		if child != canonicalCursorID(parts[1]) {
			return nil, ErrDeleteUnverified
		}
		if previous, ok := parents[child]; ok && previous != parent {
			return nil, ErrDeleteUnverified
		}
		parents[child] = parent
	}
	for _, path := range transcriptPaths {
		if parent := ParentIDFromPath(path); parent != "" && IsTranscript(path) {
			child := IDFromPath(path)
			if previous, exists := parents[child]; exists && previous != parent {
				return nil, ErrDeleteUnverified
			}
			parents[child] = parent
		}
	}
	for child := range parents {
		seen := map[string]bool{}
		for current := child; current != ""; current = parents[current] {
			if seen[current] {
				return nil, ErrDeleteUnverified
			}
			seen[current] = true
		}
	}
	wanted := map[string]bool{id: true}
	for changed := true; changed; {
		changed = false
		for child, parent := range parents {
			if wanted[parent] && !wanted[child] {
				wanted[child] = true
				changed = true
			}
		}
	}
	if err := cursorDeleteIDEReferences(ctx, home, wanted); err != nil {
		return nil, err
	}
	var paths []string
	for _, path := range chatPaths {
		relative, _ := filepath.Rel(chats, path)
		parts := strings.Split(relative, string(filepath.Separator))
		if len(parts) >= 2 && wanted[canonicalCursorID(parts[1])] {
			paths = append(paths, path)
		}
	}
	for _, path := range transcriptPaths {
		relative, _ := filepath.Rel(projects, path)
		parts := strings.Split(relative, string(filepath.Separator))
		for index, part := range parts {
			if part != "agent-transcripts" || index+1 >= len(parts) {
				continue
			}
			name := parts[index+1]
			for _, suffix := range []string{".jsonl", ".txt"} {
				name = strings.TrimSuffix(name, suffix)
			}
			if wanted[canonicalCursorID(name)] {
				paths = append(paths, path)
				break
			}
			if len(parts) == index+4 && parts[index+2] == "subagents" && wanted[IDFromPath(path)] {
				paths = append(paths, path)
				break
			}
		}
	}
	slices.SortFunc(paths, func(a, b string) int {
		if depth := strings.Count(b, string(filepath.Separator)) - strings.Count(a, string(filepath.Separator)); depth != 0 {
			return depth
		}
		return strings.Compare(a, b)
	})
	return paths, nil
}

// SQLite WAL readers may create or update SHM even in read-only mode.
// Inspect retained WALs in private scratch storage, leaving vendor files intact.
func cursorDeleteOpenCLI(ctx context.Context, path string) (*sql.DB, func() error, error) {
	if _, err := os.Lstat(path + "-wal"); errors.Is(err, os.ErrNotExist) {
		db, err := openCursorDBContext(ctx, path)
		if err != nil {
			return nil, nil, err
		}
		return db, db.Close, nil
	} else if err != nil {
		return nil, nil, err
	}
	wal, err := os.Open(path + "-wal")
	if err != nil {
		return nil, nil, err
	}
	var header [32]byte
	_, readErr := io.ReadFull(wal, header[:])
	closeErr := wal.Close()
	magic := binary.BigEndian.Uint32(header[:4])
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, nil, err
	}
	if magic != 0x377f0682 && magic != 0x377f0683 {
		return nil, nil, ErrDeleteUnverified
	}
	scratch, err := os.MkdirTemp("", "coslash-cursor-delete-")
	if err != nil {
		return nil, nil, err
	}
	var db *sql.DB
	cleanup := func() error {
		var err error
		if db != nil {
			err = db.Close()
		}
		return errors.Join(err, os.RemoveAll(scratch))
	}
	snapshot := filepath.Join(scratch, "store.db")
	for _, suffix := range []string{"", "-wal"} {
		if err := ctx.Err(); err != nil {
			return nil, nil, errors.Join(err, cleanup())
		}
		input, err := os.Open(path + suffix)
		if err != nil {
			return nil, nil, errors.Join(err, cleanup())
		}
		info, err := input.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<20 {
			input.Close()
			return nil, nil, errors.Join(ErrDeleteUnverified, err, cleanup())
		}
		output, err := os.OpenFile(snapshot+suffix, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			input.Close()
			return nil, nil, errors.Join(err, cleanup())
		}
		copied, copyErr := io.CopyN(output, input, info.Size()+1)
		if errors.Is(copyErr, io.EOF) {
			copyErr = nil
		}
		if copied != info.Size() {
			copyErr = errors.Join(copyErr, ErrDeleteUnverified)
		}
		err = errors.Join(copyErr, input.Close(), output.Close(), ctx.Err())
		if err != nil {
			return nil, nil, errors.Join(err, cleanup())
		}
	}
	db, err = openCursorDBContext(ctx, snapshot)
	if err != nil {
		return nil, nil, errors.Join(err, cleanup())
	}
	return db, cleanup, nil
}

func cursorDeleteCLIParent(ctx context.Context, path string) (childID, parentID string, resultErr error) {
	db, cleanup, err := cursorDeleteOpenCLI(ctx, path)
	if err != nil {
		return "", "", err
	}
	defer func() { resultErr = errors.Join(resultErr, cleanup()) }()
	var value string
	if err := db.QueryRowContext(ctx, `SELECT substr(value,1,1048577) FROM meta WHERE key='0'`).Scan(&value); err != nil {
		return "", "", err
	}
	if len(value) > 1048576 {
		return "", "", ErrDeleteUnverified
	}
	data, err := hex.DecodeString(value)
	if err != nil {
		return "", "", err
	}
	var meta struct {
		AgentID      string `json:"agentId"`
		SubagentInfo struct {
			ParentAgentID string `json:"parentAgentId"`
		} `json:"subagentInfo"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", "", err
	}
	child, parent := canonicalCursorID(meta.AgentID), canonicalCursorID(meta.SubagentInfo.ParentAgentID)
	if !transcriptIDPattern.MatchString(child) || (parent != "" && !transcriptIDPattern.MatchString(parent)) {
		return "", "", ErrDeleteUnverified
	}
	return child, parent, nil
}

func cursorDeleteIDEReferences(ctx context.Context, home string, ids map[string]bool) error {
	roots := []string{cursorGlobalStorage(home), filepath.Join(filepath.Dir(cursorGlobalStorage(home)), "workspaceStorage"),
		filepath.Join(home, ".config", "Cursor", "User", "globalStorage"), filepath.Join(home, ".config", "Cursor", "User", "workspaceStorage"),
		filepath.Join(home, "Library", "Application Support", "Cursor", "AgentStores"), filepath.Join(home, ".cursor", "ai-tracking")}
	for _, root := range roots {
		paths, err := cursorDeleteWalk(ctx, home, root)
		if err != nil {
			return err
		}
		for _, path := range paths {
			for id := range ids {
				if strings.Contains(strings.ToLower(path), id) {
					return ErrDeleteUnverified
				}
			}
			if filepath.Ext(path) != ".vscdb" && filepath.Ext(path) != ".db" && filepath.Ext(path) != ".sqlite" {
				continue
			}
			if err := cursorDeleteDBReferences(ctx, path, ids); err != nil {
				return err
			}
		}
	}
	return nil
}

func cursorDeleteDBReferences(ctx context.Context, path string, ids map[string]bool) error {
	if _, err := os.Lstat(path + "-wal"); !errors.Is(err, os.ErrNotExist) {
		return ErrDeleteUnverified
	}
	db, err := openCursorDBContext(ctx, path)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, table)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, table := range tables {
		switch table {
		case "cursorDiskKV", "ItemTable", "composerHeaders", "conversations", "agentKv", "ai_code_hashes":
		default:
			return ErrDeleteUnverified
		}
		quote := func(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }
		rows, err := db.QueryContext(ctx, `SELECT * FROM `+quote(table)+` LIMIT 0`)
		if err != nil {
			return err
		}
		columns, err := rows.Columns()
		rows.Close()
		if err != nil {
			return err
		}
		var conditions []string
		var args []any
		for _, column := range columns {
			for id := range ids {
				conditions = append(conditions, `instr(lower(CAST(`+quote(column)+` AS TEXT)),?)>0`)
				args = append(args, id)
			}
		}
		var found bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+quote(table)+` WHERE `+strings.Join(conditions, " OR ")+`)`, args...).Scan(&found); err != nil {
			return err
		}
		if found {
			return ErrDeleteUnverified
		}
	}
	return nil
}
