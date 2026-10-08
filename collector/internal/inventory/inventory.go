// Package inventory takes a stat-only inventory of the local agent roots so
// Local can report a content-free summary within seconds of pairing and feed
// discovery an ordered file plan without a second walk.
//
// Per-vendor rules (files are counted only when discovery would read them):
//
//   - Codex: every .jsonl under ~/.codex/sessions and ~/.codex/archived_sessions.
//     A rollout whose name carries a session UUID counts as one session; an
//     archived copy of an active session ID is skipped. Subagent and fork
//     rollouts cannot be told apart from roots without reading their header,
//     so the inventory counts them as sessions and discovery corrects it.
//   - Claude Code: every .jsonl under ~/.claude/projects except workflow
//     journals and run records. A file with no "subagents" ancestor is a
//     session; subagent transcripts join the family of the enclosing session.
//     .meta.json and workflow .json sidecars are excluded.
//   - Cursor: every transcript accepted by cursor.IsTranscript under
//     ~/.cursor/projects (IDE and CLI); agent-transcripts/<id>/<id>.jsonl is a
//     session and subagents/<id>.jsonl joins its parent's family. SQLite
//     stores under Cursor's global storage are excluded.
//   - OpenCode: the configured SQLite database is one file (plus its WAL when
//     present) whose bytes fall in the window of its modification time. The
//     OPENCODE_DB override is resolved without invoking the CLI; a custom path
//     discoverable only through the CLI is omitted from this stat-only
//     inventory. Its sessions are counted by discovery, not by the inventory,
//     because counting them would read the database.
package inventory

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
	"github.com/centauri-ai/coslash/collector/internal/vendors/cursor"
	"github.com/centauri-ai/coslash/collector/internal/vendors/opencode"
)

const largeFileBytes = 10 << 20

// File is one counted source file. Paths never leave this process.
type File struct {
	Agent     string
	Path      string
	Size      int64
	ModTimeMs int64
	// Session marks a file the inventory counts as a session root.
	Session bool
	// FamilyID groups files that discovery parses together, from paths only.
	FamilyID string
}

// Snapshot is the result of one inventory pass.
type Snapshot struct {
	ScannedAt time.Time
	Duration  time.Duration
	Files     []File
	// Missing lists agents whose root does not exist.
	Missing []string
	// Skipped counts directories or files the walk could not read.
	Skipped int
}

// Options selects the roots. Empty fields use the real home and the
// configured OpenCode database.
type Options struct {
	Home       string
	OpenCodeDB string
	Now        time.Time
	Tracker    *Tracker
}

// OpenCodeDatabasePath returns the configured OpenCode database path without
// invoking the OpenCode CLI. Relative OPENCODE_DB values use OpenCode's data
// directory, matching the source reader's path resolution.
func OpenCodeDatabasePath(home string) string {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	if override := os.Getenv("OPENCODE_DB"); override != "" {
		if filepath.IsAbs(override) {
			return filepath.Clean(override)
		}
		return filepath.Join(dataHome, "opencode", override)
	}
	return filepath.Join(dataHome, "opencode", "opencode.db")
}

// Tracker exposes the live "files so far" count while a scan runs.
type Tracker struct {
	files   atomic.Int64
	running atomic.Bool
}

// FilesSoFar returns the number of files counted by the current or last scan.
func (t *Tracker) FilesSoFar() int64 { return t.files.Load() }

// Running reports whether a scan is in progress.
func (t *Tracker) Running() bool { return t.running.Load() }

type root struct {
	agent  string
	path   string
	accept func(path string) (File, bool)
}

// Scan walks the vendor roots with directory reads and stat calls only.
func Scan(ctx context.Context, opts Options) (*Snapshot, error) {
	home := opts.Home
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return nil, err
		}
	}
	tracker := opts.Tracker
	if tracker == nil {
		tracker = &Tracker{}
	}
	tracker.files.Store(0)
	tracker.running.Store(true)
	defer tracker.running.Store(false)
	started := time.Now()
	snapshot := &Snapshot{ScannedAt: started.UTC()}
	if !opts.Now.IsZero() {
		snapshot.ScannedAt = opts.Now.UTC()
	}

	roots := []root{
		{agent: vendors.AgentCodex, path: codex.SessionsRoot(home), accept: acceptCodex},
		{agent: vendors.AgentCodex, path: codex.ArchivedDir(home), accept: acceptCodex},
		{agent: vendors.AgentClaude, path: claude.ProjectsRoot(home), accept: acceptClaude},
		{agent: vendors.AgentCursor, path: cursor.ProjectsRoot(home), accept: acceptCursor},
	}
	results := make([][]File, len(roots))
	missing := make([]bool, len(roots))
	skipped := make([]int, len(roots))
	errs := make([]error, len(roots))
	var wg sync.WaitGroup
	for index, item := range roots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[index], missing[index], skipped[index], errs[index] = walk(ctx, item, tracker)
		}()
	}
	wg.Wait()
	for index := range roots {
		if errs[index] != nil {
			return nil, errs[index]
		}
		snapshot.Files = append(snapshot.Files, results[index]...)
		snapshot.Skipped += skipped[index]
	}
	codexMissing := missing[0] && missing[1]
	for index, item := range roots[2:] {
		if missing[index+2] {
			snapshot.Missing = append(snapshot.Missing, item.agent)
		}
	}
	if codexMissing {
		snapshot.Missing = append(snapshot.Missing, vendors.AgentCodex)
	}
	snapshot.Files = dedupeArchivedRollouts(snapshot.Files)

	dbPath := opts.OpenCodeDB
	if dbPath == "" {
		// Inventory cannot invoke the OpenCode CLI to resolve a database path:
		// that command may read content. Environment configuration is stat-only.
		dbPath = OpenCodeDatabasePath(home)
	}
	dbFiles, dbMissing := statDatabase(dbPath)
	if dbMissing {
		snapshot.Missing = append(snapshot.Missing, vendors.AgentOpenCode)
	}
	snapshot.Files = append(snapshot.Files, dbFiles...)
	tracker.files.Store(int64(len(snapshot.Files)))
	sort.Strings(snapshot.Missing)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot.Duration = time.Since(started)
	return snapshot, nil
}

func walk(ctx context.Context, item root, tracker *Tracker) (files []File, missing bool, skipped int, err error) {
	info, statErr := os.Lstat(item.path)
	if statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return nil, true, 0, nil
		}
		return nil, false, 1, nil
	}
	if !info.IsDir() {
		return nil, true, 0, nil
	}
	stack := []string{item.path}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, false, skipped, err
		}
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			skipped++
			continue
		}
		for _, entry := range entries {
			switch {
			case entry.IsDir():
				stack = append(stack, filepath.Join(dir, entry.Name()))
			case entry.Type().IsRegular():
				path := filepath.Join(dir, entry.Name())
				file, ok := item.accept(path)
				if !ok {
					continue
				}
				info, infoErr := entry.Info()
				if infoErr != nil {
					skipped++
					continue
				}
				file.Agent, file.Size, file.ModTimeMs = item.agent, info.Size(), info.ModTime().UnixMilli()
				files = append(files, file)
				tracker.files.Add(1)
			}
		}
	}
	return files, false, skipped, nil
}

func acceptCodex(path string) (File, bool) {
	if !strings.HasSuffix(path, ".jsonl") {
		return File{}, false
	}
	id := codex.SessionIDFromRollout(path)
	family := id
	if family == "" {
		family = path
	}
	return File{Path: path, Session: id != "", FamilyID: family}, true
}

func acceptClaude(path string) (File, bool) {
	if !claude.AcceptsTranscript(path) {
		return File{}, false
	}
	return File{Path: path, Session: claude.ParentIDFromPath(path) == "", FamilyID: claude.FamilyIDFromPath(path)}, true
}

func acceptCursor(path string) (File, bool) {
	if !cursor.IsTranscript(path) {
		return File{}, false
	}
	parent := cursor.ParentIDFromPath(path)
	family := parent
	if family == "" {
		family = cursor.IDFromPath(path)
	}
	return File{Path: path, Session: parent == "", FamilyID: family}, true
}

// dedupeArchivedRollouts keeps the active copy of a Codex session present in
// both trees, as discovery does.
func dedupeArchivedRollouts(files []File) []File {
	seen := map[string]bool{}
	kept := files[:0]
	for _, file := range files {
		if file.Agent == vendors.AgentCodex && file.Session {
			if seen[file.FamilyID] {
				continue
			}
			seen[file.FamilyID] = true
		}
		kept = append(kept, file)
	}
	return kept
}

func statDatabase(path string) ([]File, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, true
	}
	files := []File{{Agent: vendors.AgentOpenCode, Path: path, Size: info.Size(), ModTimeMs: info.ModTime().UnixMilli(), FamilyID: "database"}}
	if wal, err := os.Stat(path + "-wal"); err == nil {
		files = append(files, File{Agent: vendors.AgentOpenCode, Path: path + "-wal", Size: wal.Size(), ModTimeMs: wal.ModTime().UnixMilli(), FamilyID: "database"})
	}
	return files, false
}

// Family is a group of files discovery parses together, keyed by agent and
// path-derived family ID, with the newest modification time of its members.
type Family struct {
	Agent      string
	ID         string
	ActivityMs int64
	Bytes      int64
	Sessions   int
	Files      []File
}

// Families groups the snapshot by agent and family, newest first.
func (s *Snapshot) Families() []Family {
	type key struct{ agent, id string }
	index := map[key]int{}
	families := []Family{}
	for _, file := range s.Files {
		k := key{file.Agent, file.FamilyID}
		position, ok := index[k]
		if !ok {
			position = len(families)
			index[k] = position
			families = append(families, Family{Agent: file.Agent, ID: file.FamilyID})
		}
		family := &families[position]
		family.Files = append(family.Files, file)
		family.Bytes += file.Size
		family.ActivityMs = max(family.ActivityMs, file.ModTimeMs)
		if file.Session {
			family.Sessions++
		}
	}
	sort.SliceStable(families, func(i, j int) bool {
		if families[i].ActivityMs != families[j].ActivityMs {
			return families[i].ActivityMs > families[j].ActivityMs
		}
		if families[i].Agent != families[j].Agent {
			return families[i].Agent < families[j].Agent
		}
		return families[i].ID < families[j].ID
	})
	return families
}

// Inventory projects the snapshot to the content-free check-in shape.
func (s *Snapshot) Inventory() hubclient.DeviceInventory {
	inventory := hubclient.DeviceInventory{
		ScannedAt:  s.ScannedAt.UTC().Format(time.RFC3339),
		DurationMs: s.Duration.Milliseconds(),
		Agents:     []hubclient.InventoryAgent{},
	}
	agents := map[string]*hubclient.InventoryAgent{}
	for _, file := range s.Files {
		inventory.Files++
		inventory.Bytes += file.Size
		inventory.LargestBytes = max(inventory.LargestBytes, file.Size)
		if file.Size > largeFileBytes {
			inventory.FilesOver10MiB++
		}
		agent := agents[file.Agent]
		if agent == nil {
			agent = &hubclient.InventoryAgent{Agent: file.Agent}
			agents[file.Agent] = agent
		}
		agent.Files++
		agent.Bytes += file.Size
	}
	for _, name := range []string{vendors.AgentCodex, vendors.AgentClaude, vendors.AgentCursor, vendors.AgentOpenCode} {
		if agent := agents[name]; agent != nil {
			inventory.Agents = append(inventory.Agents, *agent)
		}
	}
	now := s.ScannedAt.UnixMilli()
	for _, family := range s.Families() {
		age := time.Duration(now-family.ActivityMs) * time.Millisecond
		buckets := []*hubclient.InventoryWindow{&inventory.Windows.All}
		if age <= 30*24*time.Hour {
			buckets = append(buckets, &inventory.Windows.D30)
		}
		if age <= 10*24*time.Hour {
			buckets = append(buckets, &inventory.Windows.D10)
		}
		if age <= 7*24*time.Hour {
			buckets = append(buckets, &inventory.Windows.D7)
		}
		if age <= 3*24*time.Hour {
			buckets = append(buckets, &inventory.Windows.D3)
		}
		if age <= 24*time.Hour {
			buckets = append(buckets, &inventory.Windows.H24)
		}
		for _, bucket := range buckets {
			bucket.Sessions += int64(family.Sessions)
			bucket.Bytes += family.Bytes
		}
	}
	return inventory
}

// KeepCached returns the predicate that keeps a parse-cache entry whose
// source the snapshot still lists: transcript paths for Claude Code and
// Codex, session IDs for Cursor, and root IDs for OpenCode families.
func KeepCached(ctx context.Context, snapshot *Snapshot) func(agent, identity string) bool {
	type key struct{ agent, identity string }
	keep := map[key]bool{}
	for _, file := range snapshot.Files {
		switch file.Agent {
		case vendors.AgentCursor:
			keep[key{file.Agent, cursor.IDFromPath(file.Path)}] = true
		case vendors.AgentOpenCode:
		default:
			keep[key{file.Agent, file.Path}] = true
		}
	}
	families, err := opencode.FamiliesContext(ctx)
	for _, family := range families {
		keep[key{vendors.AgentOpenCode, family.ID}] = true
	}
	return func(agent, identity string) bool {
		if agent == vendors.AgentOpenCode && err != nil {
			return true
		}
		return keep[key{agent, identity}]
	}
}
