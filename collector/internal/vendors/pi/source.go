package pi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func discover() (*vendors.SourceScan, string, error) { return discoverContext(context.Background()) }
func discoverContext(ctx context.Context) (*vendors.SourceScan, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	root, err := Root()
	if err != nil {
		return nil, "", err
	}
	roots := ConfiguredSessionRoots()
	roots = append([]string{root}, roots...)
	scan := &vendors.SourceScan{RootMissing: true}
	seenRoots, seenFiles := map[string]bool{}, map[string]bool{}
	seenProjects := map[string]bool{}
	addProject := func(cwd string) {
		if cwd == "" || seenProjects[cwd] {
			return
		}
		seenProjects[cwd] = true
		path := filepath.Join(cwd, ".pi", "settings.json")
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			scan.RecordSkipped(path, err)
			return
		}
		var settings struct {
			SessionDir string `json:"sessionDir"`
		}
		if err := json.Unmarshal(data, &settings); err != nil {
			scan.RecordSkipped(path, err)
			return
		}
		if settings.SessionDir != "" {
			dir := settings.SessionDir
			if strings.HasPrefix(dir, "~/") {
				home, _ := os.UserHomeDir()
				dir = filepath.Join(home, dir[2:])
			}
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(cwd, dir)
			}
			roots = append(roots, dir)
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		addProject(cwd)
	}
	addFile := func(path string) {
		abs, err := filepath.Abs(path)
		if err != nil {
			scan.RecordSkipped(path, err)
			return
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		if !seenFiles[abs] {
			seenFiles[abs] = true
			scan.Files = append(scan.Files, abs)
			// Stored cwd supplies ordinary project settings without scanning arbitrary projects.
			if file, err := os.Open(abs); err == nil {
				var h header
				if json.NewDecoder(file).Decode(&h) == nil && h.Type == "session" {
					addProject(h.CWD)
				}
				file.Close()
			}
		}
	}
	paths, err := RuntimeTranscriptPaths()
	if err != nil {
		scan.RecordSkipped("Pi runtime metadata", err)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			scan.RecordSkipped(path, err)
			continue
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			scan.RecordSkipped(path, err)
			continue
		}
		scan.RootMissing = false
		addFile(path)
		roots = append(roots, filepath.Dir(resolved))
	}
	for index := 0; index < len(roots); index++ {
		if err := ctx.Err(); err != nil {
			return nil, root, err
		}
		dir := roots[index]
		abs, err := filepath.Abs(dir)
		if err != nil {
			scan.RecordSkipped(dir, err)
			continue
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		if seenRoots[abs] {
			continue
		}
		seenRoots[abs] = true
		part, err := vendors.ScanSourceContext(ctx, vendors.LocalReadSource, abs)
		if err != nil {
			if ctx.Err() != nil {
				return nil, root, ctx.Err()
			}
			scan.RecordSkipped(abs, err)
			continue
		}
		if !part.RootMissing {
			scan.RootMissing = false
		}
		for _, file := range part.Files {
			addFile(file)
		}
		scan.Skipped = append(scan.Skipped, part.Skipped...)
		scan.SkippedTotal += part.SkippedTotal
	}
	sort.Strings(scan.Files)
	return scan, root, nil
}

func Files() ([]string, error) {
	scan, _, err := discover()
	if err != nil {
		return nil, err
	}
	return scan.Files, nil
}

// Conflicting header identities are excluded together, never picked by filename order.
func readSessions() ([]*transcript, *vendors.SourceScan, string, error) {
	return readSessionsContext(context.Background())
}
func readSessionsContext(ctx context.Context) ([]*transcript, *vendors.SourceScan, string, error) {
	scan, root, err := discoverContext(ctx)
	if err != nil {
		return nil, nil, root, err
	}
	byID := map[string][]*transcript{}
	for _, file := range scan.Files {
		t, err := parseTranscriptContext(ctx, file)
		if ctx.Err() != nil {
			return nil, nil, root, ctx.Err()
		}
		if err != nil {
			scan.RecordSkipped(file, err)
			continue
		}
		byID[t.Header.ID] = append(byID[t.Header.ID], t)
		for _, diagnostic := range t.Diagnostics {
			scan.RecordSkipped(file, errors.New(diagnostic))
		}
	}
	var sessions []*transcript
	for id, group := range byID {
		if len(group) != 1 {
			for _, t := range group {
				scan.RecordSkipped(t.Path, fmt.Errorf("conflicting Pi session identity %q in %d transcripts", id, len(group)))
			}
		} else {
			sessions = append(sessions, group[0])
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Path < sessions[j].Path })
	return sessions, scan, root, nil
}

func Collect(since int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	return CollectContext(context.Background(), since)
}
func CollectContext(ctx context.Context, since int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	items, scan, _, err := readSessionsContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	metadata := vendors.BestEffortMetadata(vendors.AgentPi, LoadMetadata)
	result := make([]*vendors.ParsedSession, 0, len(items))
	parents := parentCacheContext(ctx, items, scan)
	for _, t := range items {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if info, err := os.Stat(t.Path); err == nil && since > 0 && info.ModTime().UnixMilli() < since && !runtimeLive(metadata, t.Header.ID) {
			continue
		}
		projected, err := projectContext(ctx, t, parents(t))
		if err != nil {
			return nil, nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		result = append(result, projected)
	}
	return result, metadata, nil
}

func GetSessionFacts(id string) (*vendors.ParsedSession, error) {
	items, scan, _, err := readSessions()
	if err != nil {
		return nil, err
	}
	parents := parentCache(items, scan)
	for _, t := range items {
		if t.Header.ID == id {
			return project(t, parents(t))
		}
	}
	// A capped health diagnostic list must not hide a conflicting requested identity.
	count := 0
	for _, path := range scan.Files {
		t, err := parseTranscript(path)
		if err == nil && t.Header.ID == id {
			count++
		}
	}
	if count > 1 {
		return nil, fmt.Errorf("conflicting Pi session identity %q in multiple transcripts", id)
	}
	return nil, nil
}

func GetSessionFamily(id string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	facts, err := GetSessionFacts(id)
	if err != nil {
		return nil, nil, err
	}
	metadata := vendors.BestEffortMetadata(vendors.AgentPi, LoadMetadata)
	if facts == nil {
		return nil, metadata, nil
	}
	return []*vendors.ParsedSession{facts}, metadata, nil
}

func Health() vendors.SourceHealth {
	items, scan, root, err := readSessions()
	h := vendors.SourceHealth{Agent: vendors.AgentPi, Root: root, Err: err}
	if err != nil {
		return h
	}
	h.Entries, h.Sessions, h.Missing = len(scan.Files), len(items), scan.RootMissing
	h.Skipped, h.SkippedTotal = scan.Skipped, scan.SkippedTotal
	return h
}

func runtimeLive(metadata *vendors.SessionMetadata, id string) bool {
	item := metadata.Lookup(id)
	return item != nil && (item.Live == "busy" || item.Live == "idle" || item.Live == "waiting")
}
