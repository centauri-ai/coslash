package codex

import (
	"path/filepath"

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

func CollectRemote(
	source vendors.ReadSource,
	home string,
	since int64,
) (vendors.RemoteCollection, error) {
	metadata := vendors.BestEffortMetadata(vendors.AgentCodex, func() (*vendors.SessionMetadata, error) {
		return LoadRemoteMetadata(source, home)
	})
	files, err := FilesSource(source, SessionsRoot(home))
	if err != nil {
		return vendors.RemoteCollection{}, err
	}
	candidates := len(files)
	if since > 0 {
		files = FilesSinceSource(source, files, metadata.Live, since)
	}
	files, truncated := limitNewestFamiliesSource(source, files)
	fingerprints, err := vendors.FingerprintSourceFiles(source, SessionsRoot(home), files)
	if err != nil {
		return vendors.RemoteCollection{}, err
	}
	sessions, err := parseFilesSourceStrict(
		source,
		filepath.Join(home, ".codex", "archived_sessions"),
		files,
		func(string, string) bool { return true },
	)
	if err != nil {
		return vendors.RemoteCollection{}, err
	}
	return vendors.RemoteCollection{
		Sessions:       sessions,
		Metadata:       metadata,
		Fingerprints:   fingerprints,
		CandidateFiles: candidates,
		SelectedFiles:  len(files),
		Truncated:      truncated,
	}, nil
}

func limitNewestFamiliesSource(source vendors.ReadSource, files []string) ([]string, bool) {
	parents := map[string]string{}
	fileIDs := map[string]string{}
	for _, file := range files {
		id, parentID, err := readHeaderSource(source, file)
		if err != nil {
			continue
		}
		fileIDs[file] = id
		parents[id] = parentID
	}
	rootID := func(id string) string {
		seen := map[string]struct{}{}
		for {
			parentID, ok := parents[id]
			if !ok || parentID == "" {
				return id
			}
			if _, ok := seen[id]; ok {
				return id
			}
			seen[id] = struct{}{}
			id = parentID
		}
	}
	return vendors.LimitNewestSourceFileFamilies(
		source, files, vendors.MaxCandidateFilesPerAgent,
		func(file string) string {
			if id := fileIDs[file]; id != "" {
				return rootID(id)
			}
			return file
		},
	)
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
) ([]*vendors.ParsedSession, error) {
	parsed, err := vendors.ParseSourceFilesStrict(source, files, func(source vendors.ReadSource, path string) (*parsedSession, error) {
		return parseSource(source, path, needsApproval)
	})
	if err != nil {
		return nil, err
	}
	return finalizeParsedFiles(source, archivedDir, parsed), nil
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
