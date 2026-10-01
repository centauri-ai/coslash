package claude

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
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

type deleteInventory struct {
	artifacts []*deleteArtifact
	paths     []string
	shared    []string
	pids      []int
}

// DeleteSession removes one closed normal CLI/Desktop session from active storage.
// History rows use the native lock and fixed-length blanking.
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
	defer inventory.close()
	if len(inventory.paths) == 0 && len(inventory.shared) == 0 {
		return ErrSessionMissing
	}
	processes, err := probe(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	currentInventory, err := inventoryDeleteSession(ctx, root, desktop, id)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	defer currentInventory.close()
	for _, process := range processes {
		for _, pid := range currentInventory.pids {
			if process.pid == pid {
				return ErrSessionActive
			}
		}
	}
	for _, artifact := range inventory.artifacts {
		if err := artifact.validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
		}
	}
	if !sameDeleteSelection(inventory, currentInventory) {
		return fmt.Errorf("%w: session selection changed", ErrSessionUnverified)
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
	// Claude's index writer has no cooperating exclusion protocol. A process
	// snapshot cannot authorize replacement of shared storage across startup.
	if len(inventory.shared) != 0 && (len(inventory.shared) != 1 || inventory.shared[0] != filepath.Join(".claude", "history.jsonl")) {
		return fmt.Errorf("%w: shared writer exclusion is not established", ErrSessionUnverified)
	}
	if len(inventory.shared) != 0 {
		if err := blankDeleteHistory(ctx, root, id); err != nil {
			return err
		}
	}
	for _, artifact := range inventory.artifacts {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %w", ErrSessionFailed, err)
		}
		if err := artifact.remove(); err != nil {
			return fmt.Errorf("%w: %w", ErrSessionFailed, err)
		}
	}
	remaining, err := inventoryDeleteSession(ctx, root, desktop, id)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionFailed, err)
	}
	defer remaining.close()
	if len(remaining.paths) != 0 || len(remaining.shared) != 0 {
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
	expected := filepath.ToSlash(path)
	return fs.WalkDir(root.FS(), expected, func(path string, entry fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if errors.Is(err, fs.ErrNotExist) && path == expected {
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

func readDeleteRecords(root *os.Root, path string) ([]byte, error) {
	if err := safeDeletePath(root, path); err != nil {
		return nil, err
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular record file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxDeleteRecordBytes+1))
	if len(data) > maxDeleteRecordBytes {
		return nil, errors.New("record file too large to verify")
	}
	return data, err
}

func inventoryDeleteSession(ctx context.Context, root *os.Root, desktop, id string) (*deleteInventory, error) {
	inventory := &deleteInventory{}
	complete := false
	defer func() {
		if !complete {
			inventory.close()
		}
	}()
	add := func(path string, record ...[]byte) error {
		if err := walkDeleteRoot(ctx, root, path, func(string, fs.DirEntry, error) error { return nil }); err != nil {
			return err
		}
		if _, err := root.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		artifact, err := pinDeleteArtifact(root, path)
		if err != nil {
			return err
		}
		artifact.id = id
		if len(record) != 0 {
			artifact.record = record[0]
		}
		inventory.artifacts = append(inventory.artifacts, artifact)
		if err := artifact.validate(); err != nil {
			return err
		}
		inventory.paths = append(inventory.paths, path)
		return nil
	}
	for _, known := range []string{filepath.Join(".claude", "projects"), filepath.Join(".claude", "sessions"), filepath.Join(".claude", "session-env"), filepath.Join(".claude", "file-history"), desktop} {
		err := walkDeleteRoot(ctx, root, known, func(path string, entry fs.DirEntry, _ error) error {
			if strings.HasPrefix(entry.Name(), ".coslash-delete-"+id+"-") {
				return fmt.Errorf("retained deletion artifact at %s", path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
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
			data, err := readDeleteRecords(root, path)
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
		data, err := readDeleteRecords(root, path)
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
			return add(path, data)
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
		data, err := readDeleteRecords(root, path)
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
		data, err := readDeleteRecords(root, path)
		if err != nil {
			return err
		}
		var record desktopSessionFile
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		if record.CLISessionID == id {
			return add(path, data)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	history := filepath.Join(".claude", "history.jsonl")
	data, err := readDeleteRecords(root, history)
	if errors.Is(err, fs.ErrNotExist) {
		if err := validateDeleteFamily(ctx, deleteReadSource{root}, projects, id, inventory.paths); err != nil {
			return nil, err
		}
		complete = true
		return inventory, nil
	}
	if err != nil {
		return nil, err
	}
	matched := false
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
				matched = true
			}
		}
	}
	if matched {
		inventory.shared = append(inventory.shared, history)
	}
	if err := validateDeleteFamily(ctx, deleteReadSource{root}, projects, id, inventory.paths); err != nil {
		return nil, err
	}
	complete = true
	return inventory, nil
}

func (inventory *deleteInventory) deleteIndex(root *os.Root, path, id string) error {
	data, err := readDeleteRecords(root, path)
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
	matched := false
	for _, entry := range entries {
		var row struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(entry, &row); err != nil {
			return err
		}
		if row.SessionID == id {
			matched = true
		}
	}
	if !matched {
		return nil
	}
	inventory.shared = append(inventory.shared, path)
	return nil
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

type deleteArtifact struct {
	file                 *os.File
	root, parent         *os.Root
	path, leaf, id       string
	parentInfo, original fs.FileInfo
	record               []byte
}

func pinDeleteArtifact(root *os.Root, path string) (*deleteArtifact, error) {
	parentPath := filepath.Dir(path)
	before, err := root.Lstat(parentPath)
	if err != nil || !before.IsDir() {
		return nil, errors.Join(err, errors.New("artifact parent is not a directory"))
	}
	parent, err := root.OpenRoot(parentPath)
	if err != nil {
		return nil, err
	}
	opened, err := parent.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		parent.Close()
		return nil, errors.Join(err, errors.New("artifact parent changed while opening"))
	}
	leaf := filepath.Base(path)
	original, err := parent.Lstat(leaf)
	if err != nil {
		parent.Close()
		return nil, err
	}
	file, err := parent.Open(leaf)
	if err != nil {
		parent.Close()
		return nil, err
	}
	held, err := file.Stat()
	if err != nil || !os.SameFile(original, held) || original.Mode()&fs.ModeSymlink != 0 {
		file.Close()
		parent.Close()
		return nil, errors.Join(err, errors.New("artifact changed while opening"))
	}
	return &deleteArtifact{file: file, root: root, parent: parent, path: path, leaf: leaf, parentInfo: opened, original: held}, nil
}

func (artifact *deleteArtifact) validate() error {
	if err := safeDeletePath(artifact.root, artifact.path); err != nil {
		return err
	}
	parent, err := artifact.root.Lstat(filepath.Dir(artifact.path))
	if err != nil || !os.SameFile(parent, artifact.parentInfo) {
		return errors.Join(err, errors.New("artifact parent replaced"))
	}
	current, err := artifact.parent.Lstat(artifact.leaf)
	if err != nil || !os.SameFile(current, artifact.original) || current.Mode() != artifact.original.Mode() {
		return errors.Join(err, errors.New("artifact replaced"))
	}
	if artifact.record != nil {
		data, err := readDeleteRecords(artifact.parent, artifact.leaf)
		if err != nil || !bytes.Equal(data, artifact.record) {
			return errors.Join(err, errors.New("metadata ownership changed"))
		}
	}
	return nil
}

func (artifact *deleteArtifact) remove() error {
	if err := artifact.validate(); err != nil {
		return err
	}
	// Capture the actual directory entry atomically before authorizing erasure.
	// Removals use the pinned expected parent, never re-resolve through home.
	staging := ".coslash-delete-" + artifact.id + "-" + rand.Text()
	if err := artifact.parent.Mkdir(staging, 0700); err != nil {
		return err
	}
	defer artifact.parent.Remove(staging)
	stageInfo, err := artifact.parent.Lstat(staging)
	if err != nil {
		return err
	}
	staged, err := artifact.parent.OpenRoot(staging)
	if err != nil {
		return err
	}
	defer staged.Close()
	opened, err := staged.Stat(".")
	if err != nil || !os.SameFile(stageInfo, opened) || !stageInfo.IsDir() {
		return errors.Join(err, errors.New("capture parent replaced"))
	}
	stagedPath := filepath.Join(staging, artifact.leaf)
	if err := artifact.parent.Rename(artifact.leaf, stagedPath); err != nil {
		return err
	}
	captured, err := artifact.parent.Lstat(stagedPath)
	if err == nil && (!os.SameFile(captured, artifact.original) || captured.Mode() != artifact.original.Mode()) {
		err = errors.New("captured artifact was replaced")
	}
	if err == nil && artifact.record != nil {
		data, readErr := readDeleteRecords(artifact.parent, stagedPath)
		if readErr != nil || !bytes.Equal(data, artifact.record) {
			err = errors.Join(readErr, errors.New("captured metadata ownership changed"))
		}
	}
	if err != nil {
		// A hard link restores regular files without overwriting a newly published
		// neighbor. If restoration fails, retain captured data and report failure.
		if captured != nil && captured.Mode().IsRegular() {
			restoreErr := artifact.parent.Link(stagedPath, artifact.leaf)
			if restoreErr == nil {
				restoreErr = artifact.parent.Remove(stagedPath)
			}
			return fmt.Errorf("%w; captured artifact at %s", errors.Join(err, restoreErr), filepath.Join(filepath.Dir(artifact.path), stagedPath))
		}
		return fmt.Errorf("%w; captured artifact at %s", err, filepath.Join(filepath.Dir(artifact.path), stagedPath))
	}
	return staged.RemoveAll(artifact.leaf)
}

func (inventory *deleteInventory) close() {
	for _, artifact := range inventory.artifacts {
		artifact.file.Close()
		artifact.parent.Close()
	}
}

func sameDeleteSelection(before, after *deleteInventory) bool {
	if len(before.paths) != len(after.paths) || len(before.shared) != len(after.shared) {
		return false
	}
	for i, path := range before.paths {
		if path != after.paths[i] {
			return false
		}
	}
	for i, path := range before.shared {
		if path != after.shared[i] {
			return false
		}
	}
	return true
}

type deleteReadSource struct{ root *os.Root }

func (source deleteReadSource) Open(path string) (io.ReadCloser, error) {
	return source.root.Open(path)
}
func (source deleteReadSource) Stat(path string) (fs.FileInfo, error) { return source.root.Lstat(path) }
func (source deleteReadSource) ReadDir(path string) ([]fs.DirEntry, error) {
	file, err := source.root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return file.ReadDir(-1)
}

func validateDeleteFamily(ctx context.Context, source vendors.ReadSource, projects, id string, owned []string) error {
	scan, err := vendors.ScanSourceContext(ctx, source, projects)
	if err != nil {
		return err
	}
	if scan.SkippedTotal != 0 {
		return errors.New("incomplete authoritative transcript scan")
	}
	for _, path := range scan.Files {
		if FamilyIDFromPath(path) != id {
			continue
		}
		known := false
		for _, selected := range owned {
			if path == selected || strings.HasPrefix(path, selected+string(filepath.Separator)) {
				known = true
				break
			}
		}
		if !known {
			return errors.New("target family exists outside owned layout")
		}
	}
	return nil
}

// Native history append and retention share proper-lockfile's mkdir/mtime protocol.
// Never steal a lock. Retention can fall back to truncation, so blanking also
// requires this exclusion even though it preserves append descriptors.
func blankDeleteHistory(ctx context.Context, root *os.Root, id string) (result error) {
	path := filepath.Join(".claude", "history.jsonl")
	artifact, err := pinDeleteArtifact(root, path)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	defer artifact.file.Close()
	defer artifact.parent.Close()
	lock := artifact.leaf + ".lock"
	if err := artifact.parent.Mkdir(lock, 0700); err != nil {
		return fmt.Errorf("%w: native history lock unavailable: %w", ErrSessionUnverified, err)
	}
	held, err := artifact.parent.OpenRoot(lock)
	if err != nil {
		return fmt.Errorf("%w: cannot verify created history lock: %w", ErrSessionUnverified, err)
	}
	defer held.Close()
	identity, err := held.Stat(".")
	if err != nil {
		return fmt.Errorf("%w: cannot verify created history lock: %w", ErrSessionUnverified, err)
	}
	var mutex sync.Mutex
	var compromised error
	stop, done := make(chan struct{}), make(chan struct{})
	refresh := func() error {
		current, err := artifact.parent.Lstat(lock)
		if err != nil || !os.SameFile(identity, current) {
			return errors.Join(err, errors.New("native history lock replaced"))
		}
		now := time.Now()
		return held.Chtimes(".", now, now)
	}
	if err := refresh(); err != nil {
		current, checkErr := artifact.parent.Lstat(lock)
		if checkErr == nil && os.SameFile(identity, current) {
			artifact.parent.Remove(lock)
		}
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				mutex.Lock()
				if compromised == nil {
					compromised = refresh()
				}
				mutex.Unlock()
			}
		}
	}()
	defer func() {
		close(stop)
		<-done
		current, err := artifact.parent.Lstat(lock)
		if err != nil || !os.SameFile(identity, current) {
			result = errors.Join(result, fmt.Errorf("%w: history lock changed during release", ErrSessionFailed))
		} else if err := artifact.parent.Remove(lock); err != nil {
			result = errors.Join(result, fmt.Errorf("%w: release history lock: %w", ErrSessionFailed, err))
		}
	}()
	// Read again while excluding native truncation, rather than using stale offsets.
	if err := artifact.validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	file, err := artifact.parent.OpenFile(artifact.leaf, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	defer file.Close()
	current, err := file.Stat()
	if err != nil || !os.SameFile(artifact.original, current) {
		return fmt.Errorf("%w: history replaced", ErrSessionUnverified)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxDeleteRecordBytes+1))
	if err != nil || len(data) > maxDeleteRecordBytes {
		return fmt.Errorf("%w: history read incomplete", ErrSessionUnverified)
	}
	type span struct {
		offset int64
		body   []byte
	}
	var rows []span
	var offset int64
	for line := range bytes.SplitAfterSeq(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) != 0 {
			var row struct {
				SessionID string `json:"sessionId"`
			}
			if err := json.Unmarshal(line, &row); err != nil {
				return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
			}
			if row.SessionID == id {
				blank := bytes.Repeat([]byte(" "), len(line))
				for i, b := range line {
					if b == '\n' || b == '\r' {
						blank[i] = b
					}
				}
				rows = append(rows, span{offset, blank})
			}
		}
		offset += int64(len(line))
	}
	check := func() error {
		mutex.Lock()
		defer mutex.Unlock()
		return errors.Join(compromised, refresh(), artifact.validate())
	}
	if err := errors.Join(ctx.Err(), check()); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionUnverified, err)
	}
	for _, row := range rows {
		if n, err := file.WriteAt(row.body, row.offset); err != nil || n != len(row.body) {
			return fmt.Errorf("%w: history blanking incomplete: %w", ErrSessionFailed, errors.Join(err, io.ErrShortWrite))
		}
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionFailed, err)
	}
	if err := check(); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionFailed, err)
	}
	return nil
}
