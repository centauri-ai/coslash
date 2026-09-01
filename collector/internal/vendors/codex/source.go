package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sort"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func Collect(since int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	files, err := Files()
	if err != nil {
		return nil, nil, err
	}
	metadata := vendors.BestEffortMetadata(vendors.AgentCodex, LoadMetadata)
	if since > 0 {
		files = FilesSince(files, metadata.Live, since)
	}
	return parseFiles(files), metadata, nil
}

func CollectSource(
	source vendors.ReadSource,
	home string,
	since int64,
	metadata *vendors.SessionMetadata,
	needsApproval func(string, string) bool,
) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	files, err := FilesSource(source, SessionsRoot(home))
	if err != nil {
		return nil, nil, err
	}
	if metadata == nil {
		metadata = vendors.EmptySessionMetadata()
	}
	if since > 0 {
		files = FilesSinceSource(source, files, metadata.Live, since)
	}
	return parseFilesSource(
		source,
		filepath.Join(home, ".codex", "archived_sessions"),
		files,
		needsApproval,
	), metadata, nil
}

// RemoteMetadata loads best-effort live/name metadata for a remote source
// without touching any transcript file.
func RemoteMetadata(source vendors.ReadSource, home string) *vendors.SessionMetadata {
	return vendors.BestEffortMetadata(vendors.AgentCodex, func() (*vendors.SessionMetadata, error) {
		return LoadRemoteMetadata(source, home)
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
		id, parentID, err := readHeaderSource(source, file)
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

// BuildRemoteFamilies discovers every candidate file, resolves each file's
// session/parent header (reusing cached headers whose fingerprint is
// unchanged), and groups files into families rooted at the topmost
// parent-less session. allFamilyIDs names every family found by a complete,
// unfiltered scan of the tree, independent of the requested window or the
// per-refresh family-count cap, so a caller can use it as deletion-authority
// inventory even when since narrows what gets selected for parsing. selected
// applies the since/live window and then caps the result to the newest
// MaxCandidateFilesPerAgent files by whole family. A file whose header could
// not be resolved becomes its own singleton family, keyed by its filename
// session ID when available, so it can be reported as skipped without hiding
// any other family's results.
func BuildRemoteFamilies(
	source vendors.ReadSource,
	home string,
	since int64,
	live map[string]string,
	cachedHeaders map[string]CachedHeader,
) (
	selected map[string]RemoteFamily,
	allFamilyIDs []string,
	updatedHeaders map[string]CachedHeader,
	headerFailed map[string]error,
	candidateFiles int,
	truncated bool,
	err error,
) {
	root := SessionsRoot(home)
	files, err := FilesSource(source, root)
	if err != nil {
		return nil, nil, nil, nil, 0, false, err
	}
	candidateFiles = len(files)
	fingerprints, err := vendors.FingerprintSourceFiles(source, root, files)
	if err != nil {
		return nil, nil, nil, nil, candidateFiles, false, err
	}
	headers, updatedHeaders, headerFailed := ResolveHeaders(source, files, fingerprints, cachedHeaders)

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
		if !ok || header.ParentID != "" {
			continue
		}
		_, isLive := live[header.SessionID]
		if isLive || since <= 0 || byFile[file].ModifiedAtMs >= since {
			selectedRoots[header.SessionID] = struct{}{}
		}
	}
	windowed := map[string][]string{}
	for _, file := range files {
		header, ok := headers[file]
		if ok {
			if _, sel := selectedRoots[rootID(header.SessionID)]; !sel {
				continue
			}
		}
		id := familyOf[file]
		windowed[id] = append(windowed[id], file)
	}
	if len(windowed) == 0 {
		return map[string]RemoteFamily{}, allFamilyIDs, updatedHeaders, headerFailed, candidateFiles, false, nil
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
	return selected, allFamilyIDs, updatedHeaders, headerFailed, candidateFiles, len(selected) < len(windowed), nil
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
	source vendors.ReadSource,
	home string,
	files []string,
) ([]*vendors.ParsedSession, []vendors.FileFailure, error) {
	return parseFilesSourceStrict(source, ArchivedDir(home), files, func(string, string) bool { return true })
}

func GetSessionFamily(id string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	files, err := Files()
	if err != nil {
		return nil, nil, err
	}
	return parseFiles(FilesForRoot(files, id)), vendors.BestEffortMetadata(vendors.AgentCodex, LoadMetadata), nil
}

func parseFiles(files []string) []*vendors.ParsedSession {
	home := ""
	if root, err := Root(); err == nil {
		home = filepath.Dir(filepath.Dir(root))
	}
	return parseFilesSource(
		vendors.LocalReadSource,
		filepath.Join(home, ".codex", "archived_sessions"),
		files,
		commandNeedsApproval,
	)
}

func parseFilesSource(
	source vendors.ReadSource,
	archivedDir string,
	files []string,
	needsApproval func(string, string) bool,
) []*vendors.ParsedSession {
	parsed := vendors.ParseSourceFiles(source, files, func(source vendors.ReadSource, path string) (*parsedSession, error) {
		return parseSource(source, path, needsApproval)
	})
	return finalizeParsedFiles(source, archivedDir, parsed)
}

func parseFilesSourceStrict(
	source vendors.ReadSource,
	archivedDir string,
	files []string,
	needsApproval func(string, string) bool,
) ([]*vendors.ParsedSession, []vendors.FileFailure, error) {
	parsed, failures, err := vendors.ParseSourceFilesStrict(source, files, func(source vendors.ReadSource, path string) (*parsedSession, error) {
		return parseSource(source, path, needsApproval)
	})
	return finalizeParsedFiles(source, archivedDir, parsed), failures, err
}

func finalizeParsedFiles(
	source vendors.ReadSource,
	archivedDir string,
	parsed []*parsedSession,
) []*vendors.ParsedSession {
	applyForkedUsageSource(source, archivedDir, parsed)
	transcripts := make([]*vendors.ParsedSession, 0, len(parsed))
	for _, item := range parsed {
		transcripts = append(transcripts, item.transcript)
	}
	return transcripts
}

func GetSessionFacts(id string) (*vendors.ParsedSession, error) {
	files, err := Files()
	if err != nil {
		return nil, err
	}
	return vendors.FindAndParse(files, id, SessionIDFromRollout, parseTranscript)
}

func Health() vendors.SourceHealth {
	root, err := Root()
	if err != nil {
		return vendors.SourceHealth{Agent: vendors.AgentCodex, Err: err}
	}
	scan, err := Scan()
	if err != nil {
		return vendors.SourceHealth{Agent: vendors.AgentCodex, Root: root, Err: err}
	}
	return vendors.FileSourceHealth(vendors.AgentCodex, root, scan, IsRootRollout)
}
