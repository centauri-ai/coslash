package codex

import "github.com/centauri-ai/coslash/collector/internal/vendors"

func Collect(since int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	files, err := Files()
	if err != nil {
		return nil, nil, err
	}
	metadata := vendors.BestEffortMetadata(vendors.AgentCodex, LoadMetadata)
	if since > 0 {
		files = FilesSince(files, metadata.Live, since)
	}
	parsed := vendors.ParseFiles(files, parse)
	applyForkedUsage(parsed)
	transcripts := make([]*vendors.ParsedSession, 0, len(parsed))
	for _, item := range parsed {
		transcripts = append(transcripts, item.transcript)
	}
	return transcripts, metadata, nil
}

func GetSessionFacts(id string) (*vendors.ParsedSession, error) {
	files, err := Files()
	if err != nil {
		return nil, err
	}
	return vendors.FindAndParse(files, id, SessionIDFromRollout, Parse)
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
