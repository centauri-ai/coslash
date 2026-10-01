package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

var (
	ErrSessionInvalid      = errors.New("invalid Codex session ID")
	ErrSessionMissing      = errors.New("Codex session missing")
	ErrSessionActive       = errors.New("Codex session active")
	ErrSessionUnverified   = errors.New("Codex session safety unverified")
	ErrSessionDeleteFailed = errors.New("Codex session deletion failed")
)

// DeleteSession deletes a closed local family, including archived copies and
// attributable history. Unknown layouts and unavailable liveness refuse writes.
func DeleteSession(ctx context.Context, home, id string) error {
	if !validDeleteID(id) {
		return ErrSessionInvalid
	}
	root, sqliteRoot, err := deleteDataRoots(home)
	if err != nil {
		return err
	}
	return deleteSession(ctx, root, sqliteRoot, id, deletePlatformLiveSessions, nil)
}

func validDeleteID(id string) bool { return len(id) == 36 && rolloutID.FindString(id) == id }

func deleteDataRoots(home string) (string, string, error) {
	root, err := LocalDataRoot(func() (string, error) { return home, nil })
	if err == nil && os.Getenv("CODEX_HOME") != "" {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		return "", "", fmt.Errorf("%w: data root: %w", ErrSessionUnverified, err)
	}
	sqliteRoot := os.Getenv("CODEX_SQLITE_HOME")
	if sqliteRoot == "" {
		sqliteRoot = root
	}
	if !filepath.IsAbs(home) || !filepath.IsAbs(root) || !filepath.IsAbs(sqliteRoot) {
		return "", "", ErrSessionUnverified
	}
	// A configured database root requires full Codex config resolution. Refuse
	// rather than verifying a different database from the one the vendor uses.
	config, err := os.ReadFile(filepath.Join(root, "config.toml"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", "", fmt.Errorf("%w: config: %w", ErrSessionUnverified, err)
	}
	if bytes.Contains(config, []byte("sqlite_home")) || bytes.Contains(config, []byte("experimental_thread_store")) {
		return "", "", fmt.Errorf("%w: configured storage override", ErrSessionUnverified)
	}
	return filepath.Clean(root), filepath.Clean(sqliteRoot), nil
}

func deleteSession(ctx context.Context, root, sqliteRoot, id string,
	live func(context.Context, []string) (map[string]struct{}, error),
	beforeMutation func(context.Context, string, string, string) error) error {
	if !validDeleteID(id) {
		return ErrSessionInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	files, headers, err := deleteRollouts(ctx, root)
	if err != nil {
		return fmt.Errorf("%w: inventory: %w", ErrSessionUnverified, err)
	}
	saved, savedPaths, err := deleteJournal(root, id, nil, nil, false)
	if err != nil {
		return fmt.Errorf("%w: retry ownership: %w", ErrSessionUnverified, err)
	}
	roots := FamilyRoots(headers)
	ids := map[string]bool{}
	selected := []string{}
	for _, path := range files {
		if roots[path] == id {
			if saved != nil && !slices.Contains(savedPaths, path) {
				return fmt.Errorf("%w: retry family expanded", ErrSessionUnverified)
			}
			selected = append(selected, path)
			ids[headers[path].SessionID] = true
		}
	}
	if saved != nil {
		for member := range saved {
			ids[member] = true
		}
		selected = append(selected, savedPaths...)
	}
	if !ids[id] {
		ids[id] = true
		if err := deleteDatabaseFiles(ctx, sqliteRoot, ids, selected, true, false); err != nil {
			return ErrSessionUnverified
		}
		snapshots, err := deleteSnapshots(ctx, root, ids)
		if err != nil || len(snapshots) > 0 {
			return ErrSessionUnverified
		}
		for _, entry := range []struct{ name, field string }{{"session_index.jsonl", "id"}, {"history.jsonl", "session_id"}} {
			before, after, err := filterDeleteRows(ctx, filepath.Join(root, entry.name), entry.field, ids)
			if err != nil || !bytes.Equal(before, after) {
				return ErrSessionUnverified
			}
		}
		return ErrSessionMissing
	}
	if err := deleteDatabaseFiles(ctx, sqliteRoot, ids, selected, false, false); err != nil {
		return fmt.Errorf("%w: database inventory: %w", ErrSessionUnverified, err)
	}
	snapshots, err := deleteSnapshots(ctx, root, ids)
	if err != nil {
		return fmt.Errorf("%w: snapshots: %w", ErrSessionUnverified, err)
	}
	for _, entry := range []struct{ name, field string }{{"session_index.jsonl", "id"}, {"history.jsonl", "session_id"}} {
		_, _, err := filterDeleteRows(ctx, filepath.Join(root, entry.name), entry.field, ids)
		if err != nil {
			return fmt.Errorf("%w: metadata inventory: %w", ErrSessionUnverified, err)
		}
	}
	if err := requireDeleteClosed(ctx, files, ids, live); err != nil {
		return err
	}
	guard, err := lockDeleteStorage(root, ids)
	if err != nil {
		return err
	}
	defer guard.Close()
	if err := requireDeleteClosed(ctx, files, ids, live); err != nil {
		return err
	}
	indexPath := filepath.Join(root, "session_index.jsonl")
	indexInfo, err := os.Stat(indexPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return ErrSessionUnverified
	}
	history, err := os.OpenFile(filepath.Join(root, "history.jsonl"), os.O_RDWR, 0)
	if err == nil {
		defer history.Close()
		err = tryDeleteFileLock(history)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: history coordination: %v", ErrSessionUnverified, err)
	}
	if saved == nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, _, err := deleteJournal(root, id, ids, selected, true); err != nil {
			return fmt.Errorf("%w: retry journal: %w", ErrSessionDeleteFailed, err)
		}
	}
	if beforeMutation != nil {
		if err := beforeMutation(ctx, root, sqliteRoot, id); err != nil {
			return fmt.Errorf("%w: local mutation: %w", ErrSessionDeleteFailed, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	residue, residueHeaders, err := deleteRollouts(ctx, root)
	if err != nil {
		return fmt.Errorf("%w: residue inventory: %w", ErrSessionDeleteFailed, err)
	}
	for path, family := range FamilyRoots(residueHeaders) {
		if family == id && !ids[residueHeaders[path].SessionID] {
			return fmt.Errorf("%w: family changed during deletion", ErrSessionDeleteFailed)
		}
	}
	if err := requireDeleteClosed(ctx, residue, ids, live); err != nil {
		return err
	}
	if err := deleteDatabaseFiles(ctx, sqliteRoot, ids, selected, false, true); err != nil {
		return fmt.Errorf("%w: database cleanup: %w", ErrSessionDeleteFailed, err)
	}
	for _, path := range append(selected, snapshots...) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := deleteScopedFile(root, path); err != nil {
			return fmt.Errorf("%w: residue removal: %w", ErrSessionDeleteFailed, err)
		}
	}
	for _, entry := range []struct{ name, field string }{{"session_index.jsonl", "id"}, {"history.jsonl", "session_id"}} {
		path := filepath.Join(root, entry.name)
		before, after, err := filterDeleteRows(ctx, path, entry.field, ids)
		if err != nil {
			return fmt.Errorf("%w: metadata: %w", ErrSessionDeleteFailed, err)
		}
		if !bytes.Equal(before, after) {
			if err := replaceDeleteRows(ctx, path, before, after, history); err != nil {
				return fmt.Errorf("%w: metadata publication: %w", ErrSessionDeleteFailed, err)
			}
		}
	}
	if err := requireDeleteClosed(ctx, residue, ids, live); err != nil {
		return err
	}
	remaining, _, err := deleteRollouts(ctx, root)
	if err != nil {
		return fmt.Errorf("%w: verification: %w", ErrSessionDeleteFailed, err)
	}
	for _, path := range remaining {
		if ids[SessionIDFromRollout(path)] {
			return fmt.Errorf("%w: rollout remains", ErrSessionDeleteFailed)
		}
	}
	remaining, err = deleteSnapshots(ctx, root, ids)
	if err != nil || len(remaining) > 0 {
		return fmt.Errorf("%w: snapshot verification", ErrSessionDeleteFailed)
	}
	for _, entry := range []struct{ name, field string }{{"session_index.jsonl", "id"}, {"history.jsonl", "session_id"}} {
		before, after, err := filterDeleteRows(ctx, filepath.Join(root, entry.name), entry.field, ids)
		if err != nil || !bytes.Equal(before, after) {
			return fmt.Errorf("%w: metadata remains", ErrSessionDeleteFailed)
		}
	}
	if err := deleteDatabaseFiles(ctx, sqliteRoot, ids, selected, true, false); err != nil {
		return fmt.Errorf("%w: database verification: %w", ErrSessionDeleteFailed, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if indexInfo != nil {
		current, err := os.Stat(indexPath)
		if err != nil || !os.SameFile(indexInfo, current) {
			return fmt.Errorf("%w: index inode changed", ErrSessionDeleteFailed)
		}
	}
	return deleteScopedFile(root, filepath.Join(root, ".coslash-delete-"+id+".json"))
}

func requireDeleteClosed(ctx context.Context, files []string, ids map[string]bool,
	live func(context.Context, []string) (map[string]struct{}, error)) error {
	open, err := live(ctx, files)
	if err != nil {
		return fmt.Errorf("%w: liveness: %w", ErrSessionUnverified, err)
	}
	if open == nil {
		return ErrSessionUnverified
	}
	for member := range ids {
		if _, ok := open[member]; ok {
			return ErrSessionActive
		}
	}
	if len(open) > 0 {
		return fmt.Errorf("%w: another session may write shared metadata", ErrSessionUnverified)
	}
	return ctx.Err()
}

func deleteRollouts(ctx context.Context, root string) ([]string, map[string]FileHeader, error) {
	var files []string
	headers := map[string]FileHeader{}
	for _, name := range []string{"sessions", "archived_sessions"} {
		tree := filepath.Join(root, name)
		err := walkDeleteTree(ctx, root, tree, func(path string, entry fs.DirEntry, _ error) error {
			if entry.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".jsonl") && !strings.HasSuffix(path, ".jsonl.zst") {
				return nil
			}
			if !strings.HasPrefix(filepath.Base(path), "rollout-") {
				return errors.New("unknown rollout name")
			}
			plain := strings.TrimSuffix(path, ".zst")
			if strings.HasSuffix(path, ".zst") {
				if _, err := os.Lstat(plain); err != nil {
					return errors.New("compressed-only rollout is unsupported")
				}
				files = append(files, path)
				return nil
			}
			id, parent, err := readHeaderSourceContext(ctx, vendors.LocalReadSource, path, maxSessionIndexRowBytes)
			if err != nil || !validDeleteID(id) || (parent != "" && !validDeleteID(parent)) {
				return errors.New("unattributable rollout header")
			}
			if !strings.HasSuffix(filepath.Base(path), id+".jsonl") {
				return errors.New("rollout filename mismatch")
			}
			headers[path] = FileHeader{SessionID: id, ParentID: parent}
			files = append(files, path)
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	for _, path := range files {
		if strings.HasSuffix(path, ".zst") {
			headers[path] = headers[strings.TrimSuffix(path, ".zst")]
		}
	}
	// Duplicate active/archive records must agree on ownership.
	parents := map[string]string{}
	for _, header := range headers {
		if previous, ok := parents[header.SessionID]; ok && previous != header.ParentID {
			return nil, nil, errors.New("conflicting rollout ownership")
		}
		parents[header.SessionID] = header.ParentID
	}
	for id := range parents {
		seen := map[string]bool{}
		for current := id; current != ""; current = parents[current] {
			if seen[current] {
				return nil, nil, errors.New("cyclic rollout family")
			}
			seen[current] = true
		}
	}
	return files, headers, nil
}

func walkDeleteTree(ctx context.Context, root, tree string, visit fs.WalkDirFunc) error {
	if err := safeDeletePath(root, tree); err != nil {
		return err
	}
	count := 0
	return filepath.WalkDir(tree, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == tree {
			return nil
		}
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > vendors.MaxCandidateFilesPerAgent {
			return errors.New("deletion inventory exceeds limit")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink in vendor storage")
		}
		if !entry.IsDir() && !entry.Type().IsRegular() {
			return errors.New("nonregular vendor artifact")
		}
		return visit(path, entry, nil)
	})
}

func deleteScopedFile(root, path string) error {
	if err := safeDeletePath(root, path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func deleteSnapshots(ctx context.Context, root string, ids map[string]bool) ([]string, error) {
	var paths []string
	err := walkDeleteTree(ctx, root, filepath.Join(root, "shell_snapshots"), func(path string, entry fs.DirEntry, _ error) error {
		if entry.IsDir() {
			return nil
		}
		name := filepath.Base(path)
		// Current snapshots are <UUID>.<nonce>.<shell>; legacy ones <UUID>.<shell>.
		if len(name) > 37 && name[36] == '.' && ids[name[:36]] {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

func filterDeleteRows(ctx context.Context, path, field string, ids map[string]bool) ([]byte, []byte, error) {
	if err := safeDeletePath(filepath.Dir(path), path); err != nil {
		return nil, nil, err
	}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	reader := bufio.NewReader(contextReader{ctx: ctx, reader: file})
	var before, after bytes.Buffer
	for {
		line, err := readBoundedIndexLine(reader)
		if len(line) > 64<<20-before.Len() {
			return nil, nil, errors.New("shared metadata exceeds limit")
		}
		before.Write(line)
		if len(bytes.TrimSpace(line)) > 0 {
			var row map[string]json.RawMessage
			if json.Unmarshal(line, &row) != nil {
				return nil, nil, errors.New("malformed shared metadata")
			}
			id, ok := jsonString(row[field])
			if !ok || !validDeleteID(id) {
				return nil, nil, errors.New("unattributable shared metadata")
			}
			if ids[id] {
				for i, value := range line {
					if value != '\n' && value != '\r' {
						line[i] = ' '
					}
				}
			}
		}
		after.Write(line)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return before.Bytes(), after.Bytes(), nil
}

func replaceDeleteRows(ctx context.Context, path string, before, after []byte, history *os.File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := safeDeletePath(filepath.Dir(path), path); err != nil {
		return err
	}
	file := history
	index := filepath.Base(path) == "session_index.jsonl"
	if file == nil || index {
		var err error
		file, err = os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		defer file.Close()
		if !index {
			if err := tryDeleteFileLock(file); err != nil {
				return err
			}
		}
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	current, err := os.Stat(path)
	if err != nil || !os.SameFile(info, current) {
		return errors.New("shared metadata inode changed")
	}
	if len(before) != len(after) {
		return errors.New("shared metadata size changed")
	}
	for start := 0; start < len(before); {
		end := len(before)
		if newline := bytes.IndexByte(before[start:], '\n'); newline >= 0 {
			end = start + newline + 1
		}
		if !bytes.Equal(before[start:end], after[start:end]) {
			if err := ctx.Err(); err != nil {
				return err
			}
			span := make([]byte, end-start)
			if _, err := file.ReadAt(span, int64(start)); err != nil {
				return err
			}
			if !bytes.Equal(span, before[start:end]) {
				return errors.New("index ownership bytes changed")
			}
			if _, err := file.WriteAt(after[start:end], int64(start)); err != nil {
				return err
			}
		}
		start = end
	}
	if err := file.Sync(); err != nil {
		return err
	}
	current, err = os.Stat(path)
	if err != nil || !os.SameFile(info, current) {
		return errors.New("shared metadata inode changed")
	}
	return ctx.Err()
}

func lockDeleteStorage(root string, ids map[string]bool) (*os.File, error) {
	directory := filepath.Join(root, "thread-writer-locks")
	path := filepath.Join(directory, ".coordination.lock")
	if err := safeDeletePath(root, path); err != nil {
		return nil, ErrSessionUnverified
	}
	lock, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: writer coordination unavailable", ErrSessionUnverified)
	}
	if err = tryDeleteFileLock(lock); err == nil {
		var entries []os.DirEntry
		entries, err = os.ReadDir(directory)
		for _, entry := range entries {
			id := strings.TrimSuffix(entry.Name(), ".lock")
			if !validDeleteID(id) {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			if err = safeDeletePath(root, path); err != nil {
				break
			}
			var file *os.File
			file, err = os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				break
			}
			err = tryDeleteFileLock(file)
			file.Close()
			if err != nil {
				lock.Close()
				if ids[id] && errors.Is(err, ErrSessionActive) {
					return nil, ErrSessionActive
				}
				return nil, ErrSessionUnverified
			}
		}
	}
	if err != nil {
		lock.Close()
		return nil, fmt.Errorf("%w: writer ownership: %v", ErrSessionUnverified, err)
	}
	return lock, nil
}

func deleteJournal(root, id string, ids map[string]bool, paths []string, save bool) (map[string]bool, []string, error) {
	path := filepath.Join(root, ".coslash-delete-"+id+".json")
	if err := safeDeletePath(root, path); err != nil {
		return nil, nil, err
	}
	var state struct {
		IDs   map[string]bool
		Paths []string
	}
	if save {
		state.IDs, state.Paths = ids, paths
		data, err := json.Marshal(state)
		if err != nil {
			return nil, nil, err
		}
		if len(data) > 1<<20 {
			return nil, nil, ErrSessionUnverified
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
		if err != nil {
			return nil, nil, err
		}
		_, err = file.Write(data)
		if err == nil {
			err = file.Sync()
		}
		return ids, paths, errors.Join(err, file.Close())
	}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	if err := json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&state); err != nil {
		return nil, nil, err
	}
	if !state.IDs[id] || len(state.IDs) > vendors.MaxCandidateFilesPerAgent || len(state.Paths) > vendors.MaxCandidateFilesPerAgent {
		return nil, nil, ErrSessionUnverified
	}
	for member, owned := range state.IDs {
		if !owned || !validDeleteID(member) {
			return nil, nil, ErrSessionUnverified
		}
	}
	for _, path := range state.Paths {
		rel, err := filepath.Rel(root, path)
		if err != nil || (!strings.HasPrefix(rel, "sessions"+string(filepath.Separator)) && !strings.HasPrefix(rel, "archived_sessions"+string(filepath.Separator))) || !state.IDs[SessionIDFromRollout(path)] {
			return nil, nil, ErrSessionUnverified
		}
		if err := safeDeletePath(root, path); err != nil {
			return nil, nil, err
		}
	}
	return state.IDs, state.Paths, nil
}

func deleteWindowsLiveSessions(ctx context.Context, files []string,
	users func([]string) ([]uint32, error)) (map[string]struct{}, error) {
	live := map[string]struct{}{}
	var probe func([]string) error
	probe = func(group []string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(group) == 0 {
			return nil
		}
		pids, err := users(group)
		if err != nil {
			return err
		}
		if len(pids) == 0 {
			return nil
		}
		if len(group) == 1 {
			live[SessionIDFromRollout(group[0])] = struct{}{}
			return nil
		}
		middle := len(group) / 2
		if err := probe(group[:middle]); err != nil {
			return err
		}
		return probe(group[middle:])
	}
	for start := 0; start < len(files); start += 128 {
		if err := probe(files[start:min(start+128, len(files))]); err != nil {
			return nil, err
		}
	}
	return live, ctx.Err()
}

func deleteUnixLiveSessions(ctx context.Context, files []string) (map[string]struct{}, error) {
	// Query the supplied rollout paths, not a process-name or cached status filter.
	// lsof exit 1 with no output means no matching open files; diagnostics refuse.
	live := map[string]struct{}{}
	for start := 0; start < len(files); start += 128 {
		args := append([]string{"-nP", "-Fn", "--"}, files[start:min(start+128, len(files))]...)
		cmd := exec.CommandContext(ctx, "lsof", args...)
		cmd.WaitDelay = time.Second
		var output, diagnostics boundedDeleteOutput
		cmd.Stdout = &output
		cmd.Stderr = &diagnostics
		err := cmd.Run()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if output.overflow || diagnostics.overflow || diagnostics.Len() > 0 {
			return nil, errors.New("incomplete liveness probe")
		}
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || output.Len() != 0 {
				return nil, err
			}
		}
		group := files[start:min(start+128, len(files))]
		found := false
		for line := range strings.SplitSeq(output.String(), "\n") {
			if strings.HasPrefix(line, "n") {
				if !slices.Contains(group, line[1:]) {
					return nil, errors.New("unexpected liveness path")
				}
				live[SessionIDFromRollout(line[1:])] = struct{}{}
				found = true
			}
		}
		if output.Len() > 0 && !found {
			return nil, errors.New("incomplete liveness fields")
		}
	}
	return live, nil
}

type boundedDeleteOutput struct {
	bytes.Buffer
	overflow bool
}

func (output *boundedDeleteOutput) Write(data []byte) (int, error) {
	size := len(data)
	remaining := (1 << 20) - output.Len()
	if len(data) > remaining {
		data = data[:remaining]
		output.overflow = true
	}
	_, _ = output.Buffer.Write(data)
	return size, nil
}
