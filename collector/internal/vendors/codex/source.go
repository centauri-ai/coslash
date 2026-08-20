package codex

import "github.com/centauri-ai/coslash/collector/internal/vendors"

func Collect(since int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	parsed, metadata, _, err := CollectLimited(since, 0)
	return parsed, metadata, err
}

// CollectLimited discovers transcripts newest-first and parses at most maxRoots
// root sessions. A non-positive maxRoots means no discovery cap.
func CollectLimited(
	since int64,
	maxRoots int,
) ([]*vendors.ParsedSession, *vendors.SessionMetadata, bool, error) {
	files, err := Files()
	if err != nil {
		return nil, nil, false, err
	}
	metadata := vendors.BestEffortMetadata(vendors.AgentCodex, LoadMetadata)
	if since > 0 {
		files = FilesSince(files, metadata.Live, since)
	}
	truncated := false
	if maxRoots > 0 {
		files, truncated = vendors.LimitRootTranscripts(
			files,
			maxRoots,
			func(path string) string {
				_, parentID, err := readHeader(path)
				if err != nil {
					return ""
				}
				return parentID
			},
			SessionIDFromRollout,
		)
	}
	return parseFiles(files), metadata, truncated, nil
}

func GetSessionFamily(id string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	files, err := Files()
	if err != nil {
		return nil, nil, err
	}
	return parseFiles(FilesForRoot(files, id)), vendors.BestEffortMetadata(vendors.AgentCodex, LoadMetadata), nil
}

func parseFiles(files []string) []*vendors.ParsedSession {
	parsed := vendors.ParseFiles(files, parse)
	applyForkedUsage(parsed)
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
