package sessioncanvas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
)

// CollectorResolver parses exactly one vendor-scoped transcript and applies
// that vendor's server-known name/status and filesystem probes. It never calls
// the all-session list path and therefore cannot turn a detail request into a
// whole-machine projection.
type CollectorResolver struct{}

func (CollectorResolver) Resolve(ctx context.Context, identity contracts.SessionIdentity) (ResolvedSession, error) {
	var (
		files    []string
		metadata *vendors.SessionMetadata
		parse    func(string) (*vendors.ParsedTranscript, error)
		match    func(string) bool
		err      error
	)
	switch identity.Agent {
	case vendors.AgentClaude:
		files, err = claude.Files()
		parse = claude.Parse
		match = func(path string) bool {
			return claude.ParentIDFromPath(path) == "" && claude.IDFromPath(path) == identity.ID
		}
		if err == nil {
			metadata, err = claude.LoadMetadata()
		}
	case vendors.AgentCodex:
		files, err = codex.Files()
		parse = codex.Parse
		match = func(path string) bool { return codex.SessionIDFromRollout(path) == identity.ID }
		if err == nil {
			metadata, err = codex.LoadMetadata()
		}
	default:
		return ResolvedSession{}, ErrSessionNotFound
	}
	if err != nil {
		return ResolvedSession{}, err
	}
	path := ""
	for _, candidate := range files {
		if err := ctx.Err(); err != nil {
			return ResolvedSession{}, err
		}
		if !match(candidate) {
			continue
		}
		if path != "" {
			return ResolvedSession{}, ErrSessionAmbiguous
		}
		path = candidate
	}
	if path == "" {
		return ResolvedSession{}, ErrSessionNotFound
	}
	parsed, err := parse(path)
	if err != nil {
		return ResolvedSession{}, err
	}
	if parsed == nil || parsed.Session == nil || parsed.ParentID != "" || parsed.Session.Agent != identity.Agent || parsed.Session.ID != identity.ID {
		return ResolvedSession{}, ErrSessionNotFound
	}
	known := *parsed.Session
	if metadata != nil {
		if name := metadata.Names[identity.ID]; name != "" {
			known.Name = &name
		} else if parsed.Name != "" {
			name := parsed.Name
			known.Name = &name
		}
		if raw, live := metadata.Live[identity.ID]; live {
			status := raw
			if raw == "interactive" {
				status = session.LiveStatus(parsed.InTurn, known.LastActivityTime, time.Now().UnixMilli())
			}
			known.Status = &status
		}
	}
	known.LastEditAt = session.LatestFileModificationTime(known.FileEdits)
	known.Git = session.BranchDrift(known.WorkingDirectory, known.Branch)
	known.GitProbed = true
	if known.WorkingDirectory != "" {
		repository := session.CanonicalRepositoryName(known.WorkingDirectory)
		known.Repository = &repository
	}
	return ResolvedSession{Session: known, TranscriptPath: path}, nil
}

// MetadataRenamer updates only the vendor-owned name index/state locations.
// A session is resolved before this is called, so it never creates a name for
// an unknown composite identity.
type MetadataRenamer struct{ Home string }

func (renamer MetadataRenamer) Rename(ctx context.Context, identity contracts.SessionIdentity, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	home := renamer.Home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return err
		}
	}
	switch identity.Agent {
	case vendors.AgentClaude:
		return renameClaude(ctx, home, identity.ID, name)
	case vendors.AgentCodex:
		return renameCodex(ctx, home, identity.ID, name)
	default:
		return ErrRenameUnsupported
	}
}

func renameClaude(ctx context.Context, home, id, name string) error {
	locations := []string{filepath.Join(home, ".claude", "sessions")}
	jobs := filepath.Join(home, ".claude", "jobs")
	if err := refuseSymlinkDirectory(jobs); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	jobEntries, err := os.ReadDir(jobs)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, entry := range jobEntries {
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			locations = append(locations, filepath.Join(jobs, entry.Name()))
		}
	}
	for _, directory := range locations {
		if err := refuseSymlinkDirectory(directory); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return err
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			var record map[string]any
			data, err := os.ReadFile(path)
			if err != nil || len(data) > 2<<20 || json.Unmarshal(data, &record) != nil || record["sessionId"] != id {
				continue
			}
			record["name"] = name
			record["nameSource"] = "user"
			return writeJSONAtomically(path, record)
		}
	}
	return ErrRenameUnsupported
}

func renameCodex(ctx context.Context, home, id, name string) error {
	directory := filepath.Join(home, ".codex")
	if err := refuseSymlinkDirectory(directory); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	path := filepath.Join(directory, "session_index.jsonl")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 64<<20 {
			return errors.New("session canvas: unsafe Codex session index")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	row, err := json.Marshal(map[string]any{"id": id, "thread_name": name, "updated_at": time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.OpenFile("session_index.jsonl", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	linked, err := os.Lstat(path)
	if err != nil || linked.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, linked) {
		return errors.New("session canvas: unsafe Codex session index")
	}
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(append(row, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func refuseSymlinkDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("session canvas: unsafe metadata directory")
	}
	return nil
}

func writeJSONAtomically(path string, value any) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("session canvas: unsafe metadata file")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".coslash-rename-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
