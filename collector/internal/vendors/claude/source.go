package claude

import (
	"path/filepath"
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

func CollectRemote(
	source vendors.ReadSource,
	home string,
	since int64,
	now time.Time,
) (vendors.RemoteCollection, error) {
	metadata := vendors.BestEffortMetadata(vendors.AgentClaude, func() (*vendors.SessionMetadata, error) {
		return LoadRemoteMetadata(source, home, now, func(pid int) bool {
			if pid <= 0 {
				return false
			}
			_, err := source.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
			return err == nil
		})
	})
	files, err := FilesSource(source, ProjectsRoot(home))
	if err != nil {
		return vendors.RemoteCollection{}, err
	}
	candidates := len(files)
	if since > 0 {
		files = FilesSinceSource(source, files, metadata.Live, since)
	}
	files, truncated := vendors.LimitNewestSourceFileFamilies(
		source, files, vendors.MaxCandidateFilesPerAgent, familyIDFromPath,
	)
	fingerprints, err := vendors.FingerprintSourceFiles(source, ProjectsRoot(home), files)
	if err != nil {
		return vendors.RemoteCollection{}, err
	}
	sessions, err := parseFilesSourceStrict(source, files)
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

func familyIDFromPath(file string) string {
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
	selected := files[:0]
	for _, file := range files {
		rootID := ParentIDFromPath(file)
		if rootID == "" {
			rootID = IDFromPath(file)
		}
		if rootID == id {
			selected = append(selected, file)
		}
	}
	return parseFiles(selected), vendors.BestEffortMetadata(vendors.AgentClaude, LoadMetadata), nil
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
) ([]*vendors.ParsedSession, error) {
	parsed, err := vendors.ParseSourceFilesStrict(source, files, parseSource)
	if err != nil {
		return nil, err
	}
	return finalizeParsedFiles(source, parsed), nil
}

func finalizeParsedFiles(
	source vendors.ReadSource,
	parsed []*parsedSession,
) []*vendors.ParsedSession {
	applyForkedUsageSource(source, parsed)
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
