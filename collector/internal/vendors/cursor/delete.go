package cursor

import (
	"context"
	"crypto/sha256"
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
	return deleteSession(ctx, home, id, probeCursorDeletion, nil)
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
	id = canonicalCursorID(id)
	recordPath := filepath.Join(home, ".coslash", "deletions", "cursor", id+".json")
	record, recordInfo, err := cursorDeleteLoadRecord(ctx, home, recordPath, id)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDeleteUnverified, err)
	}
	var recordIdentity string
	var known map[string]string
	if record != nil {
		known = record.Family
		recordIdentity, err = cursorDeleteFileIdentity(recordPath, recordInfo)
		if err != nil {
			return errors.Join(ErrDeleteUnverified, err)
		}
	}
	plan, err := cursorDeleteInventory(ctx, home, id, known)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDeleteUnverified, err)
	}
	if len(plan.Files) == 0 && record == nil {
		return ErrDeleteMissing
	}
	if record != nil {
		if err := cursorDeleteRecordMatches(record, plan); err != nil {
			return fmt.Errorf("%w: %w", ErrDeleteUnverified, err)
		}
	}
	if err := verifyClosed(); err != nil {
		return err
	}
	current, err := cursorDeleteInventory(ctx, home, id, plan.Family)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDeleteFailed, err)
	}
	if len(current.Files) != len(plan.Files) {
		return ErrDeleteFailed
	}
	for index, file := range plan.Files {
		if file.Path != current.Files[index].Path || file.Digest != current.Files[index].Digest || file.Identity != current.Files[index].Identity || !os.SameFile(file.info, current.Files[index].info) {
			return ErrDeleteFailed
		}
		if err := cursorDeleteCheck(ctx, home, file, plan); err != nil {
			return fmt.Errorf("%w: %w", ErrDeleteFailed, err)
		}
	}
	if record == nil {
		record = &cursorDeleteRecord{ID: id, Family: plan.Family, Files: plan.Files}
		recordInfo, err = cursorDeleteSaveRecord(home, recordPath, record)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrDeleteFailed, err)
		}
		recordIdentity, err = cursorDeleteFileIdentity(recordPath, recordInfo)
		if err != nil {
			return errors.Join(ErrDeleteFailed, err)
		}
	}
	if remove == nil {
		root, err := os.OpenRoot(home)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrDeleteFailed, err)
		}
		defer root.Close()
		remove = func(path string) error {
			relative, err := filepath.Rel(home, path)
			if err != nil {
				return err
			}
			return root.Remove(relative)
		}
	}
	for _, file := range plan.Files {
		if err := cursorDeleteCheck(ctx, home, file, plan); err != nil {
			return fmt.Errorf("%w: %w", ErrDeleteFailed, err)
		}
		if cursorDeleteCLIStorePath(home, file.Path) {
			child, parent, err := cursorDeleteCLIParent(ctx, file.Path)
			if err != nil || child != canonicalCursorID(filepath.Base(filepath.Dir(file.Path))) || parent != plan.Family[child] {
				return errors.Join(ErrDeleteFailed, err)
			}
			if err := cursorDeleteCheck(ctx, home, file, plan); err != nil {
				return errors.Join(ErrDeleteFailed, err)
			}
		}
		if err := remove(file.Path); err != nil {
			return fmt.Errorf("%w: %w", ErrDeleteFailed, err)
		}
	}
	remaining, err := cursorDeleteInventory(ctx, home, id, record.Family)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDeleteFailed, err)
	}
	if len(remaining.Files) != 0 || ctx.Err() != nil {
		return ErrDeleteFailed
	}
	for _, file := range record.Files {
		if _, err := os.Lstat(file.Path); !errors.Is(err, os.ErrNotExist) {
			return ErrDeleteFailed
		}
	}
	info, err := os.Lstat(recordPath)
	if err != nil || !os.SameFile(info, recordInfo) || info.Size() != recordInfo.Size() || !info.ModTime().Equal(recordInfo.ModTime()) {
		return errors.Join(ErrDeleteFailed, err)
	}
	if err := cursorDeleteContained(home, recordPath); err != nil {
		return errors.Join(ErrDeleteFailed, err)
	}
	identity, err := cursorDeleteFileIdentity(recordPath, info)
	if err != nil || identity != recordIdentity {
		return errors.Join(ErrDeleteFailed, err)
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrDeleteFailed, err)
	}
	if err := remove(recordPath); err != nil {
		return errors.Join(ErrDeleteFailed, err)
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

func cursorDeleteCLIStorePath(home, path string) bool {
	relative, err := filepath.Rel(filepath.Join(home, ".cursor", "chats"), path)
	parts := strings.Split(relative, string(filepath.Separator))
	return err == nil && len(parts) == 3 && parts[2] == "store.db"
}

func cursorDeleteInventory(ctx context.Context, home, id string, known map[string]string) (*cursorDeletePlan, error) {
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
	identities, nativeIDs, err := cursorDeleteCapture(home, append(slices.Clone(chatPaths), transcriptPaths...))
	if err != nil {
		return nil, err
	}
	parents := map[string]string{}
	for _, path := range chatPaths {
		relative, _ := filepath.Rel(chats, path)
		parts := strings.Split(relative, string(filepath.Separator))
		if !cursorDeleteCLIStorePath(home, path) {
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
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if seen[current] {
				return nil, ErrDeleteUnverified
			}
			seen[current] = true
		}
	}
	for child, parent := range known {
		if actual, exists := parents[child]; exists && actual != parent {
			return nil, ErrDeleteUnverified
		}
		parents[child] = parent
	}
	wanted := map[string]bool{id: true}
	for changed := true; changed; {
		changed = false
		for child, parent := range parents {
			if wanted[parent] && !wanted[child] {
				wanted[child] = true
				if len(wanted) > 256 {
					return nil, ErrDeleteUnverified
				}
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
	plan := &cursorDeletePlan{Family: map[string]string{}, identities: identities, nativeIDs: nativeIDs}
	for child := range wanted {
		plan.Family[child] = parents[child]
	}
	for _, path := range paths {
		info := identities[path]
		identity, err := cursorDeleteFileIdentity(path, info)
		if err != nil || identity != nativeIDs[path] {
			return nil, errors.Join(ErrDeleteUnverified, err)
		}
		digest, err := cursorDeleteFingerprint(ctx, path, info)
		if err != nil {
			return nil, err
		}
		plan.Files = append(plan.Files, cursorDeleteArtifact{Path: path, Digest: digest, Directory: info.IsDir(), Identity: nativeIDs[path], info: info})
	}
	return plan, nil
}

// SQLite WAL readers may create or update SHM even in read-only mode.
// Inspect retained WALs in private scratch storage, leaving vendor files intact.
func cursorDeleteOpenCLI(ctx context.Context, path string) (*sql.DB, func() error, error) {
	if _, err := os.Lstat(path + "-journal"); !errors.Is(err, os.ErrNotExist) {
		return nil, nil, errors.Join(ErrDeleteUnverified, err)
	}
	walInfo, err := os.Lstat(path + "-wal")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	hasWAL := err == nil && walInfo.Size() > 0
	if hasWAL {
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
		if (magic != 0x377f0682 && magic != 0x377f0683) || binary.BigEndian.Uint32(header[4:8]) != 3007000 {
			return nil, nil, ErrDeleteUnverified
		}
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
	suffixes := []string{""}
	if hasWAL {
		suffixes = append(suffixes, "-wal")
	}
	for _, suffix := range suffixes {
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

func cursorDeleteDBReferences(ctx context.Context, path string, ids map[string]bool) (resultErr error) {
	db, cleanup, err := cursorDeleteOpenCLI(ctx, path)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, cleanup()) }()
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

type cursorDeleteArtifact struct {
	Path      string
	Digest    string
	Identity  string
	Directory bool
	info      os.FileInfo
}

type cursorDeleteRecord struct {
	ID     string
	Family map[string]string
	Files  []cursorDeleteArtifact
}

type cursorDeletePlan struct {
	Family     map[string]string
	Files      []cursorDeleteArtifact
	identities map[string]os.FileInfo
	nativeIDs  map[string]string
}

func cursorDeleteCapture(home string, paths []string) (map[string]os.FileInfo, map[string]string, error) {
	identities := map[string]os.FileInfo{}
	nativeIDs := map[string]string{}
	for _, path := range paths {
		if err := cursorDeleteContained(home, path); err != nil {
			return nil, nil, err
		}
		for current := path; ; current = filepath.Dir(current) {
			if _, exists := identities[current]; !exists {
				info, err := os.Lstat(current)
				if err != nil {
					return nil, nil, err
				}
				identities[current] = info
				identity, err := cursorDeleteFileIdentity(current, info)
				if err != nil {
					return nil, nil, err
				}
				nativeIDs[current] = identity
			}
			if current == home {
				break
			}
		}
	}
	return identities, nativeIDs, nil
}

func cursorDeleteFingerprint(ctx context.Context, path string, expected os.FileInfo) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if expected == nil || !os.SameFile(info, expected) || info.Mode() != expected.Mode() {
		return "", ErrDeleteUnverified
	}
	if info.IsDir() {
		return "", nil
	}
	if info.Size() != expected.Size() || !info.ModTime().Equal(expected.ModTime()) || info.Size() > 256<<20 {
		return "", ErrDeleteUnverified
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(opened, expected) {
		return "", errors.Join(ErrDeleteUnverified, err)
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(file, info.Size()+1))
	if err != nil || size != info.Size() {
		return "", errors.Join(ErrDeleteUnverified, err)
	}
	after, err := file.Stat()
	if err != nil || !after.ModTime().Equal(info.ModTime()) || after.Size() != info.Size() {
		return "", errors.Join(ErrDeleteUnverified, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func cursorDeleteCheck(ctx context.Context, home string, file cursorDeleteArtifact, plan *cursorDeletePlan) error {
	if err := cursorDeleteContained(home, file.Path); err != nil {
		return err
	}
	for current := file.Path; ; current = filepath.Dir(current) {
		expected := plan.identities[current]
		info, err := os.Lstat(current)
		if err != nil || expected == nil || !os.SameFile(info, expected) {
			return errors.Join(ErrDeleteUnverified, err)
		}
		identity, err := cursorDeleteFileIdentity(current, info)
		if err != nil || identity != plan.nativeIDs[current] {
			return errors.Join(ErrDeleteUnverified, err)
		}
		if current == home {
			break
		}
	}
	digest, err := cursorDeleteFingerprint(ctx, file.Path, file.info)
	if err != nil || digest != file.Digest {
		return errors.Join(ErrDeleteUnverified, err)
	}
	return nil
}

func cursorDeleteRecordMatches(record *cursorDeleteRecord, plan *cursorDeletePlan) error {
	if len(record.Family) != len(plan.Family) {
		return ErrDeleteUnverified
	}
	for id, parent := range record.Family {
		if actual, exists := plan.Family[id]; !exists || actual != parent {
			return ErrDeleteUnverified
		}
	}
	evidence := map[string]cursorDeleteArtifact{}
	for _, file := range record.Files {
		evidence[file.Path] = file
	}
	for _, file := range plan.Files {
		original, exists := evidence[file.Path]
		if !exists || original.Directory != file.Directory || original.Digest != file.Digest || original.Identity != file.Identity {
			return ErrDeleteUnverified
		}
	}
	return nil
}

func cursorDeleteLoadRecord(ctx context.Context, home, path, id string) (*cursorDeleteRecord, os.FileInfo, error) {
	if err := cursorDeleteContained(home, path); err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if info.Size() > 8<<20 {
		return nil, nil, ErrDeleteUnverified
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(opened, info) {
		return nil, nil, errors.Join(ErrDeleteUnverified, err)
	}
	data, err := io.ReadAll(io.LimitReader(file, 8<<20+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > 8<<20 {
		return nil, nil, ErrDeleteUnverified
	}
	var record cursorDeleteRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, nil, err
	}
	if record.ID != id || len(record.Family) == 0 || len(record.Family) > 256 || len(record.Files) == 0 || len(record.Files) > 100000 {
		return nil, nil, ErrDeleteUnverified
	}
	if _, exists := record.Family[id]; !exists {
		return nil, nil, ErrDeleteUnverified
	}
	for child, parent := range record.Family {
		if parent == child || canonicalCursorID(child) != child || !transcriptIDPattern.MatchString(child) || (parent != "" && !transcriptIDPattern.MatchString(parent)) {
			return nil, nil, ErrDeleteUnverified
		}
		seen := map[string]bool{}
		for current := child; current != id; current = record.Family[current] {
			if _, exists := record.Family[current]; !exists || seen[current] {
				return nil, nil, ErrDeleteUnverified
			}
			seen[current] = true
		}
	}
	paths := map[string]bool{}
	for _, artifact := range record.Files {
		if artifact.Identity == "" || paths[artifact.Path] || (!artifact.Directory && len(artifact.Digest) != 64) || (artifact.Directory && artifact.Digest != "") {
			return nil, nil, ErrDeleteUnverified
		}
		paths[artifact.Path] = true
		if !artifact.Directory {
			if _, err := hex.DecodeString(artifact.Digest); err != nil {
				return nil, nil, ErrDeleteUnverified
			}
		}
		if err := cursorDeleteContained(home, artifact.Path); err != nil {
			return nil, nil, err
		}
		if !cursorDeleteOwnedPath(home, artifact.Path, record.Family) {
			return nil, nil, ErrDeleteUnverified
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return &record, info, nil
}

func cursorDeleteOwnedPath(home, path string, family map[string]string) bool {
	relative, err := filepath.Rel(filepath.Join(home, ".cursor"), path)
	if err != nil {
		return false
	}
	parts := strings.Split(relative, string(filepath.Separator))
	if len(parts) >= 3 && parts[0] == "chats" {
		_, exists := family[canonicalCursorID(parts[2])]
		return exists
	}
	if len(parts) < 4 || parts[0] != "projects" {
		return false
	}
	for index, part := range parts {
		if part != "agent-transcripts" || index+1 >= len(parts) {
			continue
		}
		name := strings.TrimSuffix(strings.TrimSuffix(parts[index+1], ".txt"), ".jsonl")
		if _, exists := family[canonicalCursorID(name)]; exists {
			return true
		}
		if len(parts) == index+4 && parts[index+2] == "subagents" {
			_, exists := family[IDFromPath(path)]
			return exists
		}
	}
	return false
}

func cursorDeleteSaveRecord(home, path string, record *cursorDeleteRecord) (os.FileInfo, error) {
	if err := cursorDeleteContained(home, path); err != nil {
		return nil, err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	if len(data) > 8<<20 || len(record.Family) > 256 {
		return nil, ErrDeleteUnverified
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if err := cursorDeleteContained(home, path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	info, statErr := file.Stat()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, statErr, closeErr); err != nil {
		return nil, errors.Join(err, os.Remove(path))
	}
	if runtime.GOOS != "windows" {
		for current := filepath.Dir(path); ; current = filepath.Dir(current) {
			directory, err := os.Open(current)
			if err != nil {
				return nil, err
			}
			if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
				return nil, err
			}
			if current == home {
				break
			}
		}
	}
	return info, nil
}
