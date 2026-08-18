package claude

import "github.com/centauri-ai/coslash/collector/internal/vendors"

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
