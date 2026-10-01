package claude

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// DeleteCause identifies a safe API error category, including partial failure.
type DeleteCause string

func (e DeleteCause) Error() string { return string(e) }

const (
	ErrSessionMissing    DeleteCause = "claude session missing"
	ErrSessionActive     DeleteCause = "claude session active"
	ErrSessionUnverified DeleteCause = "claude session unverified"
	ErrSessionFailed     DeleteCause = "claude session deletion failed"
	ErrSessionInvalid    DeleteCause = "claude session invalid"
)

var deleteUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const maxDeleteRecordBytes = 64 << 20

type deleteProcess struct {
	pid    int
	claude bool
}

type deleteRewrite struct {
	path          string
	before, after []byte
	mode          fs.FileMode
}

type deleteInventory struct {
	paths    []string
	rewrites []deleteRewrite
	pids     []int
}

// DeleteSession removes one closed normal CLI/Desktop session from active storage.
// Process and inventory uncertainty is rejected before the first mutation.
func DeleteSession(ctx context.Context, home, id string) error {
	return deleteSession(ctx, home, id, listDeleteProcesses)
}

func deleteSession(ctx context.Context, home, id string, probe func(context.Context) ([]deleteProcess, error)) error {
	if !deleteUUID.MatchString(id) || !filepath.IsAbs(home) {
		return ErrSessionInvalid
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	defer root.Close()
	desktop := desktopDeleteRelative()
	current, homeErr := os.UserHomeDir()
	if homeErr == nil && filepath.Clean(current) == filepath.Clean(home) {
		config, err := os.UserConfigDir()
		if err != nil {
			return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
		}
		desktop, err = filepath.Rel(home, filepath.Join(config, "Claude", "claude-code-sessions"))
		if err != nil || !filepath.IsLocal(desktop) {
			return fmt.Errorf("%w: Desktop configuration outside home", ErrSessionUnverified)
		}
	}
	inventory, err := inventoryDeleteSession(ctx, root, desktop, id)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	if len(inventory.paths) == 0 && len(inventory.rewrites) == 0 {
		return ErrSessionMissing
	}
	processes, err := probe(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	for _, process := range processes {
		for _, pid := range inventory.pids {
			if process.pid == pid {
				return ErrSessionActive
			}
		}
	}
	// A Claude process without an exact session association may open or recreate
	// the transcript even when it currently has no file descriptor for it.
	for _, process := range processes {
		if process.claude {
			return ErrSessionUnverified
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	for _, rewrite := range inventory.rewrites {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %w", ErrSessionFailed, err)
		}
		if err := writeDeleteRecords(root, rewrite); err != nil {
			return fmt.Errorf("%w: %w", ErrSessionFailed, err)
		}
	}
	for _, path := range inventory.paths {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %w", ErrSessionFailed, err)
		}
		if err := safeDeletePath(root, path); err != nil {
			return fmt.Errorf("%w: %w", ErrSessionFailed, err)
		}
		if err := root.RemoveAll(path); err != nil {
			return fmt.Errorf("%w: %w", ErrSessionFailed, err)
		}
	}
	remaining, err := inventoryDeleteSession(ctx, root, desktop, id)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionFailed, err)
	}
	if len(remaining.paths) != 0 || len(remaining.rewrites) != 0 {
		return ErrSessionFailed
	}
	return nil
}

func desktopDeleteRelative() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join("Library", "Application Support", "Claude", "claude-code-sessions")
	case "windows":
		return filepath.Join("AppData", "Roaming", "Claude", "claude-code-sessions")
	default:
		return filepath.Join(".config", "Claude", "claude-code-sessions")
	}
}

func safeDeletePath(root *os.Root, path string) error {
	if !filepath.IsLocal(path) || path == "." {
		return errors.New("unsafe deletion path")
	}
	parts := strings.Split(filepath.Clean(path), string(filepath.Separator))
	for i := range parts {
		info, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return errors.New("symlink in deletion path")
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("special file in deletion path")
		}
	}
	return nil
}

func walkDeleteRoot(ctx context.Context, root *os.Root, path string, visit fs.WalkDirFunc) error {
	if err := safeDeletePath(root, path); err != nil {
		return err
	}
	return fs.WalkDir(root.FS(), filepath.ToSlash(path), func(path string, entry fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return errors.New("symlink in inventory")
		}
		if !entry.IsDir() && !entry.Type().IsRegular() {
			return errors.New("special file in inventory")
		}
		return visit(filepath.FromSlash(path), entry, nil)
	})
}

func readDeleteRecords(root *os.Root, path string) ([]byte, fs.FileMode, error) {
	if err := safeDeletePath(root, path); err != nil {
		return nil, 0, err
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, errors.New("not a regular record file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxDeleteRecordBytes+1))
	if len(data) > maxDeleteRecordBytes {
		return nil, 0, errors.New("record file too large to verify")
	}
	return data, info.Mode().Perm(), err
}

func inventoryDeleteSession(ctx context.Context, root *os.Root, desktop, id string) (*deleteInventory, error) {
	inventory := &deleteInventory{}
	add := func(path string) error {
		if err := walkDeleteRoot(ctx, root, path, func(string, fs.DirEntry, error) error { return nil }); err != nil {
			return err
		}
		if _, err := root.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		inventory.paths = append(inventory.paths, path)
		return nil
	}
	projects := filepath.Join(".claude", "projects")
	err := walkDeleteRoot(ctx, root, projects, func(path string, entry fs.DirEntry, _ error) error {
		relative, err := filepath.Rel(projects, path)
		if err != nil {
			return err
		}
		depth := len(strings.Split(relative, string(filepath.Separator)))
		if relative == "." || depth == 1 {
			return nil
		}
		if depth > 2 {
			return fs.SkipDir
		}
		if entry.IsDir() {
			if entry.Name() == id {
				if err := add(path); err != nil {
					return err
				}
			}
			return fs.SkipDir
		}
		if entry.Name() == id+".jsonl" {
			data, _, err := readDeleteRecords(root, path)
			if err != nil {
				return err
			}
			decoder := json.NewDecoder(bytes.NewReader(data))
			for {
				var row struct {
					SessionKind string `json:"sessionKind"`
				}
				err := decoder.Decode(&row)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return err
				}
				if row.SessionKind == "bg" {
					return errors.New("background family requires separate verification")
				}
			}
			return add(path)
		}
		if entry.Name() == "sessions-index.json" {
			return inventory.deleteIndex(root, path, id)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{"session-env", "file-history"} {
		if err := add(filepath.Join(".claude", dir, id)); err != nil {
			return nil, err
		}
	}
	sessions := filepath.Join(".claude", "sessions")
	err = walkDeleteRoot(ctx, root, sessions, func(path string, entry fs.DirEntry, _ error) error {
		if entry.IsDir() {
			if path != sessions {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		data, _, err := readDeleteRecords(root, path)
		if err != nil {
			return err
		}
		var record liveSessionFile
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		if record.SessionID == id {
			if record.PID <= 0 {
				return errors.New("session process ID missing")
			}
			inventory.pids = append(inventory.pids, record.PID)
			return add(path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Background job state is outside this adapter's supported deletion scope.
	jobs := filepath.Join(".claude", "jobs")
	err = walkDeleteRoot(ctx, root, jobs, func(path string, entry fs.DirEntry, _ error) error {
		relative, _ := filepath.Rel(jobs, path)
		depth := len(strings.Split(relative, string(filepath.Separator)))
		if entry.IsDir() {
			if depth >= 2 {
				return fs.SkipDir
			}
			return nil
		}
		if depth != 2 || entry.Name() != "state.json" {
			return nil
		}
		data, _, err := readDeleteRecords(root, path)
		if err != nil {
			return err
		}
		var record jobStateFile
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		if record.SessionID == id {
			return errors.New("background job cannot be verified")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = walkDeleteRoot(ctx, root, desktop, func(path string, entry fs.DirEntry, _ error) error {
		relative, _ := filepath.Rel(desktop, path)
		depth := strings.Count(relative, string(filepath.Separator))
		if entry.IsDir() {
			if depth >= 2 {
				return fs.SkipDir
			}
			return nil
		}
		if depth != 2 || !strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		data, _, err := readDeleteRecords(root, path)
		if err != nil {
			return err
		}
		var record desktopSessionFile
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		if record.CLISessionID == id {
			return add(path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	history := filepath.Join(".claude", "history.jsonl")
	data, mode, err := readDeleteRecords(root, history)
	if errors.Is(err, fs.ErrNotExist) {
		return inventory, nil
	}
	if err != nil {
		return nil, err
	}
	var kept []byte
	for line := range bytes.SplitAfterSeq(data, []byte("\n")) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(line)) != 0 {
			var row struct {
				SessionID string `json:"sessionId"`
			}
			if err := json.Unmarshal(line, &row); err != nil {
				return nil, err
			}
			if row.SessionID == id {
				continue
			}
		}
		kept = append(kept, line...)
	}
	if !bytes.Equal(data, kept) {
		inventory.rewrites = append(inventory.rewrites, deleteRewrite{history, data, kept, mode})
	}
	return inventory, nil
}

func (inventory *deleteInventory) deleteIndex(root *os.Root, path, id string) error {
	data, mode, err := readDeleteRecords(root, path)
	if err != nil {
		return err
	}
	var index map[string]json.RawMessage
	if err := json.Unmarshal(data, &index); err != nil {
		return err
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(index["entries"], &entries); err != nil {
		return err
	}
	kept := entries[:0]
	for _, entry := range entries {
		var row struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(entry, &row); err != nil {
			return err
		}
		if row.SessionID != id {
			kept = append(kept, entry)
		}
	}
	if len(kept) == len(entries) {
		return nil
	}
	index["entries"], err = json.Marshal(kept)
	if err != nil {
		return err
	}
	after, err := json.Marshal(index)
	if err != nil {
		return err
	}
	inventory.rewrites = append(inventory.rewrites, deleteRewrite{path, data, after, mode})
	return nil
}

func writeDeleteRecords(root *os.Root, rewrite deleteRewrite) error {
	current, _, err := readDeleteRecords(root, rewrite.path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, rewrite.before) {
		return errors.New("shared records changed before deletion")
	}
	// Exclusive sibling creation and rename keep neighboring records intact on
	// write failure, without truncating the vendor's shared file in place.
	temporary := rewrite.path + ".coslash-delete-" + strconv.Itoa(os.Getpid())
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, rewrite.mode)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(rewrite.after)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	current, _, err = readDeleteRecords(root, rewrite.path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, rewrite.before) {
		return errors.New("shared records changed during deletion")
	}
	return root.Rename(temporary, rewrite.path)
}

func listDeleteProcesses(ctx context.Context) ([]deleteProcess, error) {
	var output []byte
	var err error
	if runtime.GOOS == "windows" {
		output, err = exec.CommandContext(ctx, "tasklist", "/FO", "CSV", "/NH").Output()
	} else {
		output, err = exec.CommandContext(ctx, "ps", "-ww", "-axo", "pid=,comm=,args=").Output()
	}
	if err != nil {
		return nil, err
	}
	return parseDeleteProcesses(string(output), runtime.GOOS == "windows")
}

func parseDeleteProcesses(output string, windows bool) ([]deleteProcess, error) {
	var processes []deleteProcess
	for line := range strings.SplitSeq(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		pidField := 0
		if len(fields) < 2 {
			return nil, errors.New("incomplete process inventory")
		}
		candidate := false
		for index, argument := range fields[1:] {
			argument = strings.ToLower(filepath.ToSlash(strings.Trim(argument, "\"")))
			name := filepath.Base(argument)
			if (index < 2 && (name == "claude" || name == "claude.exe")) || strings.Contains(argument, "/claude.app/") || strings.Contains(argument, "/@anthropic-ai/claude-code/") || strings.Contains(argument, "/claude/versions/") {
				candidate = true
			}
		}
		if windows {
			var err error
			fields, err = csv.NewReader(strings.NewReader(line)).Read()
			if err != nil {
				return nil, err
			}
			pidField = 1
			// tasklist does not expose the command line of Node-hosted Claude CLIs.
			candidate = strings.EqualFold(fields[0], "claude.exe") || strings.EqualFold(fields[0], "node.exe")
		}
		if len(fields) < 2 {
			return nil, errors.New("incomplete process inventory")
		}
		pid, err := strconv.Atoi(fields[pidField])
		if err != nil || pid <= 0 {
			return nil, errors.New("invalid process inventory")
		}
		processes = append(processes, deleteProcess{pid: pid, claude: candidate})
	}
	if len(processes) == 0 {
		return nil, errors.New("empty process inventory")
	}
	return processes, nil
}
