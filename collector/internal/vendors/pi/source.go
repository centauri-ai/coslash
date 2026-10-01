package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func discover() (*vendors.SourceScan, string, error) { return discoverContext(context.Background()) }
func discoverContext(ctx context.Context) (*vendors.SourceScan, string, error) {
	scan, root, _, err := discoverHeadersContext(ctx)
	return scan, root, err
}
func discoverHeadersContext(ctx context.Context) (*vendors.SourceScan, string, map[string]header, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", nil, err
	}
	root, err := Root()
	if err != nil {
		return nil, "", nil, err
	}
	roots := ConfiguredSessionRoots()
	roots = append([]string{root}, roots...)
	scan := &vendors.SourceScan{RootMissing: true}
	seenRoots, seenFiles := map[string]bool{}, map[string]bool{}
	seenProjects := map[string]bool{}
	headers := map[string]header{}
	addProject := func(cwd string) {
		if cwd == "" || seenProjects[cwd] {
			return
		}
		seenProjects[cwd] = true
		path := filepath.Join(cwd, ".pi", "settings.json")
		data, err := readProjectSettings(ctx, path)
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
			if dir == "~" || strings.HasPrefix(dir, "~/") {
				dir, err = ResolveDirectory(dir)
				if err != nil {
					scan.RecordSkipped(path, err)
					return
				}
			} else if !filepath.IsAbs(dir) {
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
			h, err := readTranscriptHeader(ctx, abs)
			if err != nil {
				scan.RecordSkipped(abs, err)
				return
			}
			headers[abs] = h
			addProject(h.CWD)
		}
	}
	paths, err := RuntimeTranscriptPathsContext(ctx)
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
			return nil, root, nil, err
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
				return nil, root, nil, ctx.Err()
			}
			if !errors.Is(err, os.ErrNotExist) {
				scan.RootMissing = false
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
	if err := ctx.Err(); err != nil {
		return nil, root, nil, err
	}
	return scan, root, headers, nil
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
	return readSessionsSinceContext(ctx, 0, nil)
}
func readSessionsSinceContext(ctx context.Context, since int64, metadata *vendors.SessionMetadata) ([]*transcript, *vendors.SourceScan, string, error) {
	scan, root, headers, err := discoverHeadersContext(ctx)
	if err != nil {
		return nil, nil, root, err
	}
	identities := map[string][]string{}
	for path, h := range headers {
		identities[h.ID] = append(identities[h.ID], path)
	}
	selected := map[string]bool{}
	var eligible []string
	for _, file := range scan.Files {
		if err := ctx.Err(); err != nil {
			return nil, nil, root, err
		}
		h, ok := headers[file]
		if !ok {
			continue
		}
		if len(identities[h.ID]) != 1 {
			scan.RecordSkipped(file, fmt.Errorf("conflicting Pi session identity %q in %d transcripts", h.ID, len(identities[h.ID])))
			continue
		}
		if info, err := os.Stat(file); err == nil && since > 0 && info.ModTime().UnixMilli() < since && !runtimeLive(metadata, h.ID) {
			continue
		}
		selected[file] = true
		eligible = append(eligible, file)
	}
	// Older fork ancestors still supply inherited-usage evidence; unrelated archives stay unread.
	for _, file := range eligible {
		for depth := 0; depth < 256; depth++ {
			if err := ctx.Err(); err != nil {
				return nil, nil, root, err
			}
			h := headers[file]
			if h.ParentSession == "" {
				break
			}
			parent := canonicalPath(h.ParentSession)
			h, ok := headers[parent]
			if !ok || len(identities[h.ID]) != 1 || selected[parent] {
				break
			}
			selected[parent], file = true, parent
		}
	}
	var sessions []*transcript
	for _, file := range scan.Files {
		if !selected[file] {
			continue
		}
		t, err := parseTranscriptContext(ctx, file)
		if ctx.Err() != nil {
			return nil, nil, root, ctx.Err()
		}
		if err != nil {
			scan.RecordSkipped(file, err)
			continue
		}
		if t.Header.ID != headers[file].ID || t.Header.ParentSession != headers[file].ParentSession {
			scan.RecordSkipped(file, fmt.Errorf("session header changed during discovery"))
			continue
		}
		sessions = append(sessions, t)
		for _, diagnostic := range t.Diagnostics {
			scan.RecordSkipped(file, errors.New(diagnostic))
		}
	}

	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Path < sessions[j].Path })
	return sessions, scan, root, nil
}

func Collect(since int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	return CollectContext(context.Background(), since)
}
func CollectContext(ctx context.Context, since int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	snapshot, _ := LoadRuntimeSnapshot()
	ctx = WithRuntimeSnapshot(ctx, snapshot)
	metadata := vendors.BestEffortMetadata(vendors.AgentPi, func() (*vendors.SessionMetadata, error) { return LoadMetadataContext(ctx) })
	items, scan, _, err := readSessionsSinceContext(ctx, since, metadata)
	if err != nil {
		return nil, nil, err
	}
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
	snapshot, _ := LoadRuntimeSnapshot()
	return getSessionFactsContext(WithRuntimeSnapshot(context.Background(), snapshot), id)
}
func getSessionFactsContext(ctx context.Context, id string) (*vendors.ParsedSession, error) {
	items, scan, _, err := readSessionsContext(ctx)
	if err != nil {
		return nil, err
	}
	parents := parentCacheContext(ctx, items, scan)
	for _, t := range items {
		if t.Header.ID == id {
			return projectContext(ctx, t, parents(t))
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
	snapshot, _ := LoadRuntimeSnapshot()
	ctx := WithRuntimeSnapshot(context.Background(), snapshot)
	facts, err := getSessionFactsContext(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	metadata := vendors.BestEffortMetadata(vendors.AgentPi, func() (*vendors.SessionMetadata, error) { return LoadMetadataContext(ctx) })
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

func readTranscriptHeader(ctx context.Context, path string) (header, error) {
	var h header
	if err := ctx.Err(); err != nil {
		return h, err
	}
	file, err := os.Open(path)
	if err != nil {
		return h, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return h, err
	}
	if info.Size() > maxTranscriptBytes {
		return h, fmt.Errorf("transcript limit exceeded: maximum %d bytes", maxTranscriptBytes)
	}
	reader, total := bufio.NewReader(file), 0
	var data []byte
	for {
		data, err = readTranscriptRecord(ctx, reader, maxTranscriptBytes-total)
		total += len(data)
		if err != nil && err != io.EOF {
			return h, err
		}
		if len(bytes.TrimSpace(data)) > 0 || err == io.EOF {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return h, err
	}
	if err := json.Unmarshal(data, &h); err != nil {
		return h, err
	}
	if h.Type != "session" || h.ID == "" {
		return h, fmt.Errorf("expected session header with identity")
	}
	if h.Version != 3 {
		return h, fmt.Errorf("unsupported Pi transcript schema %d (verified schema: 3, Pi 0.99.1)", h.Version)
	}
	return h, nil
}

func readProjectSettings(ctx context.Context, path string) ([]byte, error) {
	const limit = 1 << 20
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var data []byte
	for {
		record, err := readTranscriptRecord(ctx, reader, limit-len(data))
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("project settings read failed (maximum %d bytes): %w", limit, err)
		}
		data = append(data, record...)
		if err == io.EOF {
			return data, ctx.Err()
		}
	}
}
