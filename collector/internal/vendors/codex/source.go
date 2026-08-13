package codex

import "github.com/centauri-ai/coslash/collector/internal/vendors"

func Collect(since int64) ([]*vendors.ParsedTranscript, *vendors.SessionMetadata, error) {
	files, err := Files()
	if err != nil {
		return nil, nil, err
	}
	metadata, err := LoadMetadata()
	if err != nil {
		return nil, nil, err
	}
	if since > 0 {
		files = FilesSince(files, metadata.Live, since)
	}
	return vendors.ParseFiles(files, Parse), metadata, nil
}

func Get(id string) (*vendors.ParsedTranscript, error) {
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
