package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func Collect(ctx context.Context, since int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}
	files, err := Files(ctx, vendors.LocalReadSource, home)
	if err != nil {
		return nil, nil, err
	}
	metadata, metadataErr := LoadMetadataForFiles(ctx, files)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if metadataErr != nil {
		log.Printf("%s session liveness failed: %v; continuing without live status", vendors.AgentCodex, metadataErr)
		if metadata == nil {
			metadata = vendors.EmptySessionMetadata()
		}
	}
	if since > 0 {
		files, err = FilesSince(ctx, vendors.LocalReadSource, files, metadata.LiveSessions(), since)
		if err != nil {
			return nil, nil, err
		}
	}
	parsed, err := ParseFiles(ctx, files)
	return parsed, metadata, err
}

// RemoteMetadata loads best-effort live/name metadata for a remote source
// without touching any transcript file.
func RemoteMetadata(ctx context.Context, source vendors.ReadSource, home string) *vendors.SessionMetadata {
	return vendors.BestEffortMetadata(vendors.AgentCodex, func() (*vendors.SessionMetadata, error) {
		return LoadRemoteMetadata(ctx, source, home)
	})
}

// ArchivedDir is the fixed archived-rollouts path under a remote home.
func ArchivedDir(home string) string {
	return filepath.Join(home, ".codex", "archived_sessions")
}

// CachedHeader is a previously observed session/parent header keyed by the
// file's identity fingerprint, reusable while that fingerprint is unchanged.
type CachedHeader struct {
	Fingerprint vendors.FileFingerprint
	SessionID   string
	ParentID    string
}

// ResolveHeaders returns the session/parent header for every file, reusing
// cached[fingerprint.Key] whenever the file's current fingerprint matches the
// cached one instead of reopening the file. It never fails the whole batch: a
// file whose header cannot be read is reported in failed and excluded from
// headers, so the caller can isolate it to its own family.
func ResolveHeaders(
	ctx context.Context,
	source vendors.ReadSource,
	files []string,
	fingerprints []vendors.FileFingerprint,
	cached map[string]CachedHeader,
) (headers map[string]CachedHeader, updated map[string]CachedHeader, failed map[string]error) {
	headers = make(map[string]CachedHeader, len(files))
	updated = make(map[string]CachedHeader, len(files))
	failed = map[string]error{}
	for index, file := range files {
		fp := fingerprints[index]
		if entry, ok := cached[fp.Key]; ok && entry.Fingerprint == fp {
			headers[file] = entry
			updated[fp.Key] = entry
			continue
		}
		id, parentID, err := readHeader(ctx, source, file)
		if err != nil {
			failed[file] = err
			continue
		}
		entry := CachedHeader{Fingerprint: fp, SessionID: id, ParentID: parentID}
		headers[file] = entry
		updated[fp.Key] = entry
	}
	return headers, updated, failed
}

// RemoteFamily is one family's contributing files and their fingerprints,
// selected for this refresh's requested window.
type RemoteFamily struct {
	Files        []string
	Fingerprints []vendors.FileFingerprint
}

// BuildRemoteFamilies discovers every candidate file, returns that active-file
// index for fork normalization, resolves each file's
// session/parent header (reusing cached headers whose fingerprint is
// unchanged), and groups files into families rooted at the topmost
// parent-less session. allFamilyIDs names every family found by a complete,
// unfiltered scan of the tree, independent of the requested window or the
// per-refresh family-count cap, so a caller can use it as deletion-authority
// inventory when skippedTotal is zero, even when since narrows what gets
// selected for parsing. selected
// applies the since/live window and then caps the result to the newest
// MaxCandidateFilesPerAgent files by whole family. A file whose header could
// not be resolved becomes its own singleton family, keyed by its filename
// session ID when available, so it can be reported as skipped without hiding
// any other family's results.
func BuildRemoteFamilies(
	ctx context.Context,
	source vendors.ReadSource,
	home string,
	since int64,
	live map[string]string,
	cachedHeaders map[string]CachedHeader,
) (
	selected map[string]RemoteFamily,
	activeFiles []string,
	allFamilyIDs []string,
	updatedHeaders map[string]CachedHeader,
	headerFailed map[string]error,
	candidateFiles int,
	skippedTotal int,
	truncated bool,
	err error,
) {
	root := vendors.SourcePathJoin(source, home, ".codex", "sessions")
	scan, err := Scan(ctx, source, root)
	if err != nil {
		return nil, nil, nil, nil, nil, 0, 0, false, err
	}
	files := scan.Files
	skippedTotal = scan.SkippedTotal
	candidateFiles = len(files)
	fingerprints, err := vendors.FingerprintSourceFiles(ctx, source, root, files)
	if err != nil {
		return nil, nil, nil, nil, nil, candidateFiles, skippedTotal, false, err
	}
	headers, updatedHeaders, headerFailed := ResolveHeaders(ctx, source, files, fingerprints, cachedHeaders)
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, nil, nil, candidateFiles, skippedTotal, false, err
	}

	parents := map[string]string{}
	for _, header := range headers {
		parents[header.SessionID] = header.ParentID
	}
	rootID := func(id string) string {
		seen := map[string]struct{}{}
		for {
			parentID, ok := parents[id]
			if !ok || parentID == "" {
				return id
			}
			if _, dup := seen[id]; dup {
				return id
			}
			seen[id] = struct{}{}
			id = parentID
		}
	}
	fallbackID := func(file string) string {
		if id := SessionIDFromRollout(file); id != "" {
			return id
		}
		digest := sha256.Sum256([]byte(file))
		return hex.EncodeToString(digest[:])
	}
	familyOf := make(map[string]string, len(files))
	allFamilySet := map[string]struct{}{}
	for _, file := range files {
		header, ok := headers[file]
		if !ok {
			familyOf[file] = fallbackID(file)
			allFamilySet[familyOf[file]] = struct{}{}
			continue
		}
		familyOf[file] = rootID(header.SessionID)
		allFamilySet[familyOf[file]] = struct{}{}
	}
	allFamilyIDs = make([]string, 0, len(allFamilySet))
	for id := range allFamilySet {
		allFamilyIDs = append(allFamilyIDs, id)
	}
	sort.Strings(allFamilyIDs)

	byFile := make(map[string]vendors.FileFingerprint, len(files))
	for index, file := range files {
		byFile[file] = fingerprints[index]
	}
	selectedRoots := map[string]struct{}{}
	for _, file := range files {
		header, ok := headers[file]
		sessionID := familyOf[file]
		if ok {
			sessionID = header.SessionID
		}
		_, isLive := live[sessionID]
		// A family belongs in the window when any of its members is recent or
		// live. In particular, a fork can remain active after its root has
		// aged out. Header failures are subject to the same window before
		// their singleton fallback family is selected.
		if isLive || since <= 0 || byFile[file].ModifiedAtMs >= since {
			selectedRoots[familyOf[file]] = struct{}{}
		}
	}
	windowed := map[string][]string{}
	for _, file := range files {
		if _, sel := selectedRoots[familyOf[file]]; !sel {
			continue
		}
		windowed[familyOf[file]] = append(windowed[familyOf[file]], file)
	}
	if len(windowed) == 0 {
		return map[string]RemoteFamily{}, files, allFamilyIDs, updatedHeaders, headerFailed, candidateFiles, skippedTotal, false, nil
	}
	newest := map[string]int64{}
	for id, familyFiles := range windowed {
		for _, file := range familyFiles {
			newest[id] = max(newest[id], byFile[file].ModifiedAtMs)
		}
	}
	orderedIDs := make([]string, 0, len(windowed))
	for id := range windowed {
		orderedIDs = append(orderedIDs, id)
	}
	sortFamiliesByNewest(orderedIDs, newest)
	selected = map[string]RemoteFamily{}
	total := 0
	for _, id := range orderedIDs {
		familyFiles := windowed[id]
		if total > 0 && total+len(familyFiles) > vendors.MaxCandidateFilesPerAgent {
			continue
		}
		entry := RemoteFamily{Files: familyFiles}
		for _, file := range familyFiles {
			entry.Fingerprints = append(entry.Fingerprints, byFile[file])
		}
		selected[id] = entry
		total += len(familyFiles)
	}
	return selected, files, allFamilyIDs, updatedHeaders, headerFailed, candidateFiles, skippedTotal, len(selected) < len(windowed), nil
}

func sortFamiliesByNewest(ids []string, newest map[string]int64) {
	sort.Slice(ids, func(i, j int) bool {
		if newest[ids[i]] == newest[ids[j]] {
			return ids[i] < ids[j]
		}
		return newest[ids[i]] > newest[ids[j]]
	})
}

// ParseRemoteFiles parses exactly the given files (already selected as one or
// more changed families) and applies fork-usage finalization. A per-file
// failure is reported alongside every file that parsed successfully so the
// caller can isolate the failure to that file's family.
func ParseRemoteFiles(
	ctx context.Context,
	source vendors.ReadSource,
	home string,
	files []string,
	knownActiveFiles []string,
) ([]*vendors.ParsedSession, []vendors.FileFailure, error) {
	parsed, failures, err := vendors.ParseSourceFilesStrict(ctx, source, files, func(ctx context.Context, source vendors.ReadSource, path string) (*parsedSession, error) {
		return parseSource(ctx, source, path, func(string, string) bool { return true })
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, nil, ctxErr
	}
	finalized, finalizeErr := finalizeParsedFiles(ctx, source, vendors.SourcePathJoin(source, home, ".codex", "archived_sessions"), knownActiveFiles, parsed)
	if finalizeErr != nil {
		return nil, nil, finalizeErr
	}
	clearPromptDerivedNames(finalized)
	return finalized, failures, err
}

// clearPromptDerivedNames prevents the parser's first-user-prompt fallback
// from crossing either remote collection boundary. Approved session-index
// metadata names remain available separately.
func clearPromptDerivedNames(parsed []*vendors.ParsedSession) {
	for _, item := range parsed {
		if item != nil {
			item.Name = ""
		}
	}
}

func GetSessionFamily(ctx context.Context, id string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}
	files, err := Files(ctx, vendors.LocalReadSource, home)
	if err != nil {
		return nil, nil, err
	}
	metadata := vendors.BestEffortMetadata(vendors.AgentCodex, func() (*vendors.SessionMetadata, error) {
		return LoadMetadataForFiles(ctx, files)
	})
	family, err := FilesForRoot(ctx, vendors.LocalReadSource, files, id)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := ParseFiles(ctx, family)
	if err != nil {
		return nil, nil, err
	}
	return parsed, metadata, nil
}

// ParseFiles parses exactly the given local rollouts, which must be whole
// families, with fork normalization. Streamed discovery calls it per batch of
// families.
func ParseFiles(ctx context.Context, files []string) ([]*vendors.ParsedSession, error) {
	home := ""
	if root, err := Root(); err == nil {
		home = filepath.Dir(filepath.Dir(root))
	}
	parsed, err := vendors.ParseSourceFiles(ctx, vendors.LocalReadSource, files,
		func(ctx context.Context, source vendors.ReadSource, path string) (*parsedSession, error) {
			return parseSourceCached(ctx, source, path, func(command, cwd string) bool {
				return commandNeedsApproval(ctx, command, cwd)
			})
		})
	if err != nil {
		return nil, err
	}
	return finalizeParsedFiles(ctx, vendors.LocalReadSource, filepath.Join(home, ".codex", "archived_sessions"), nil, parsed)
}

func finalizeParsedFiles(
	ctx context.Context,
	source vendors.ReadSource,
	archivedDir string,
	knownActiveFiles []string,
	parsed []*parsedSession,
) ([]*vendors.ParsedSession, error) {
	if err := applyForkedUsage(ctx, source, archivedDir, knownActiveFiles, parsed); err != nil {
		return nil, err
	}
	transcripts := make([]*vendors.ParsedSession, 0, len(parsed))
	for _, item := range parsed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		transcripts = append(transcripts, item.transcript)
	}
	return transcripts, nil
}

func GetSessionFacts(ctx context.Context, id string) (*vendors.ParsedSession, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	files, err := Files(ctx, vendors.LocalReadSource, home)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if SessionIDFromRollout(file) == id {
			return parseTranscript(ctx, vendors.LocalReadSource, file, func(command, cwd string) bool {
				return commandNeedsApproval(ctx, command, cwd)
			})
		}
	}
	return nil, nil
}

func Health(ctx context.Context) vendors.SourceHealth {
	home, err := os.UserHomeDir()
	if err != nil {
		return vendors.SourceHealth{Agent: vendors.AgentCodex, Err: err}
	}
	return healthForHome(ctx, vendors.LocalReadSource, home)
}

func healthForHome(ctx context.Context, source vendors.ReadSource, home string) vendors.SourceHealth {
	root := filepath.Dir(SessionsRoot(home))
	scan, err := scanHome(ctx, source, home)
	if err != nil {
		return vendors.SourceHealth{Agent: vendors.AgentCodex, Root: root, Err: err}
	}
	return vendors.FileSourceHealth(vendors.AgentCodex, root, scan, func(path string) (bool, error) {
		return IsRootRollout(ctx, source, path)
	})
}
