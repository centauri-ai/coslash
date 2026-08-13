package vendors

import "log"

// SessionMetadata carries raw liveness signals, not final status strings: the final
// status needs parse output (InTurn, lastActivity), so resolveStatus computes it.
// A Live value of "interactive" gets the busy/idle refinement; anything else
// passes through. Absent means there is no external live signal; parsed data
// may still provide a persisted or derived status hint.
type SessionMetadata struct {
	Names map[string]string // id → externally-assigned name; Parsed.Name is the fallback
	Live  map[string]string // id → raw status
}

func EmptySessionMetadata() *SessionMetadata {
	return &SessionMetadata{Names: map[string]string{}, Live: map[string]string{}}
}

func BestEffortMetadata(
	agent string,
	load func() (*SessionMetadata, error),
) *SessionMetadata {
	metadata, err := load()
	if err != nil {
		log.Printf("%s session metadata failed: %v; continuing without enrichment", agent, err)
		return EmptySessionMetadata()
	}
	if metadata == nil {
		return EmptySessionMetadata()
	}
	return metadata
}
