package claude

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func Collect(since int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	files, err := Files()
	if err != nil {
		return nil, nil, err
	}
	metadata := vendors.BestEffortMetadata(vendors.AgentClaude, LoadMetadata)
	if since > 0 {
		files = FilesSince(files, metadata.Live, since)
	}
	return parseFiles(files), metadata, nil
}

func CollectSource(
	source vendors.ReadSource,
	root string,
	since int64,
	metadata *vendors.SessionMetadata,
) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	files, err := FilesSource(source, root)
	if err != nil {
		return nil, nil, err
	}
	if metadata == nil {
		metadata = vendors.EmptySessionMetadata()
	}
	if since > 0 {
		files = FilesSinceSource(source, files, metadata.Live, since)
	}
	return parseFilesSource(source, files), metadata, nil
}

// RemoteMetadata loads best-effort live/name metadata for a remote source
// without touching any transcript file.
func RemoteMetadata(source vendors.ReadSource, home string, now time.Time) *vendors.SessionMetadata {
	return vendors.BestEffortMetadata(
		vendors.AgentClaude,
		func() (*vendors.SessionMetadata, error) {
			return LoadRemoteMetadata(source, home, now, func(pid int) bool {
				if pid <= 0 {
					return false
				}
				_, err := source.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
				return err == nil
			})
		},
	)
}

// RemoteFamily is one family's contributing files and their fingerprints,
// selected for this refresh's requested window.
type RemoteFamily struct {
	Files        []string
	Fingerprints []vendors.FileFingerprint
}

// BuildRemoteFamilies discovers, selects, and fingerprints candidate remote
// files without opening or parsing any of them, grouping the window-selected
// result by FamilyIDFromPath. allFamilyIDs names every family found by a
// complete, unfiltered scan of the tree, independent of the requested window
// or the per-refresh family-count cap, so a caller can use it as
// deletion-authority inventory when skippedTotal is zero, even when since
// narrows what gets selected.
func BuildRemoteFamilies(
	source vendors.ReadSource,
	home string,
	since int64,
	live map[string]string,
) (
	selected map[string]RemoteFamily,
	allFamilyIDs []string,
	candidateFiles int,
	skippedTotal int,
	truncated bool,
	err error,
) {
	root := ProjectsRoot(home)
	scan, err := ScanSource(source, root)
	if err != nil {
		return nil, nil, 0, 0, false, err
	}
	allFiles := scan.Files
	skippedTotal = scan.SkippedTotal
	candidateFiles = len(allFiles)
	allFamilySet := map[string]struct{}{}
	for _, file := range allFiles {
		allFamilySet[FamilyIDFromPath(file)] = struct{}{}
	}
	allFamilyIDs = make([]string, 0, len(allFamilySet))
	for id := range allFamilySet {
		allFamilyIDs = append(allFamilyIDs, id)
	}
	sort.Strings(allFamilyIDs)

	files := allFiles
	if since > 0 {
		files = FilesSinceSource(source, files, live, since)
	}
	files, truncated = vendors.LimitNewestSourceFileFamilies(
		source, files, vendors.MaxCandidateFilesPerAgent, FamilyIDFromPath,
	)
	fingerprints, err := vendors.FingerprintSourceFiles(source, root, files)
	if err != nil {
		return nil, nil, candidateFiles, skippedTotal, truncated, err
	}
	selected = map[string]RemoteFamily{}
	for index, file := range files {
		id := FamilyIDFromPath(file)
		entry := selected[id]
		entry.Files = append(entry.Files, file)
		entry.Fingerprints = append(entry.Fingerprints, fingerprints[index])
		selected[id] = entry
	}
	return selected, allFamilyIDs, candidateFiles, skippedTotal, truncated, nil
}

// ParseRemoteFiles parses exactly the given files (already selected as one or
// more changed families) and applies the same fork-usage and background-rehome
// finalization as local collection. A per-file failure is reported alongside
// every file that parsed successfully so the caller can isolate the failure to
// that file's family.
func ParseRemoteFiles(
	source vendors.ReadSource,
	files []string,
) ([]*vendors.ParsedSession, []vendors.FileFailure, error) {
	return parseFilesSourceStrict(source, files)
}

func FamilyIDFromPath(file string) string {
	if parentID := ParentIDFromPath(file); parentID != "" {
		return parentID
	}
	return IDFromPath(file)
}

func GetSessionFamily(id string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	files, err := Files()
	if err != nil {
		return nil, nil, err
	}
	return parseFiles(familyFiles(vendors.LocalReadSource, files, id)),
		vendors.BestEffortMetadata(vendors.AgentClaude, LoadMetadata), nil
}

// picks the transcripts that compose one card: the requested root, its subagents
// any root a re-home superseded whose subagent directory the re-home did not copy
func familyFiles(source vendors.ReadSource, files []string, id string) []string {
	selected, target := []string{}, ""
	owners := map[string][]string{}
	for _, file := range files {
		if FamilyIDFromPath(file) == id {
			selected = append(selected, file)
		}
		if parent := ParentIDFromPath(file); parent != "" {
			owners[parent] = append(owners[parent], file)
		} else if IDFromPath(file) == id {
			target = file
		}
	}
	if target == "" || len(owners) == 0 {
		return selected
	}
	parsed, err := parseSource(source, target)
	if err != nil || !parsed.background {
		return selected
	}
	for _, file := range files {
		root := IDFromPath(file)
		if root == id || ParentIDFromPath(file) != "" || len(owners[root]) == 0 {
			continue
		}
		if sharesConversation(source, file, parsed.rowUUIDs) {
			selected = append(selected, file)
			selected = append(selected, owners[root]...)
		}
	}
	return selected
}

func sharesConversation(source vendors.ReadSource, path string, want map[string]struct{}) bool {
	file, err := source.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	for {
		var row struct {
			UUID    string          `json:"uuid"`
			Message json.RawMessage `json:"message"`
		}
		if decoder.Decode(&row) != nil {
			return false
		}
		if row.UUID == "" || len(row.Message) == 0 {
			continue
		}
		_, ok := want[row.UUID]
		return ok
	}
}

func parseFiles(files []string) []*vendors.ParsedSession {
	return parseFilesSource(vendors.LocalReadSource, files)
}

func parseFilesSource(source vendors.ReadSource, files []string) []*vendors.ParsedSession {
	parsed := vendors.ParseSourceFiles(source, files, parseSource)
	return finalizeParsedFiles(source, parsed)
}

func parseFilesSourceStrict(
	source vendors.ReadSource,
	files []string,
) ([]*vendors.ParsedSession, []vendors.FileFailure, error) {
	parsed, failures, err := vendors.ParseSourceFilesStrict(source, files, parseSource)
	return finalizeParsedFiles(source, parsed), failures, err
}

func finalizeParsedFiles(
	source vendors.ReadSource,
	parsed []*parsedSession,
) []*vendors.ParsedSession {
	applyForkedUsageSource(source, parsed)
	parsed = collapseBackgroundRehomes(parsed)
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
	return vendors.FindAndParse(files, id, IDFromPath, parseTranscript)
}

func Health() vendors.SourceHealth {
	root, err := Root()
	if err != nil {
		return vendors.SourceHealth{Agent: vendors.AgentClaude, Err: err}
	}
	scan, err := Scan()
	if err != nil {
		return vendors.SourceHealth{Agent: vendors.AgentClaude, Root: root, Err: err}
	}
	return vendors.FileSourceHealth(
		vendors.AgentClaude,
		root,
		scan,
		func(path string) (bool, error) { return ParentIDFromPath(path) == "", nil },
	)
}
