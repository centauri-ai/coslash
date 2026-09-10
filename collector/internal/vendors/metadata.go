package vendors

import (
	"log"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

type SessionUsage struct {
	Tokens        map[string]session.ModelTokens
	ContextTokens *int
	RecordedCost  *float64
}

type SessionRelationship struct {
	ParentID  string
	SpawnKey  string
	Task      string
	Time      int64
	Completed bool
	Active    bool
}

// SessionMetadata carries raw liveness signals, not final status strings: the final
// status needs parse output (InTurn, lastActivity), so resolveStatus computes it.
// A Live value of "interactive" gets the busy/idle refinement; anything else
// passes through. Absent means there is no external live signal; parsed data
// may still provide a persisted or derived status hint.
type SessionMetadata struct {
	Names              map[string]string // id → externally-assigned name; Parsed.Name is the fallback
	Live               map[string]string // id → raw status
	Summaries          map[string]string // id → recorded or side-store summary
	Entrypoints        map[string]string // id → exact source lane
	WorkingDirectories map[string]string // id → authoritative side-store working directory
	StartedAt          map[string]int64  // id → authoritative side-store creation time
	LastActivityAt     map[string]int64  // id → authoritative side-store update time
	FileEdits          map[string][]session.FileEdit
	Models             map[string]string // id → last model observed in source data
	PullRequests       map[string]int    // id → distinct confirmed or reported PR URLs
	Relationships      map[string]SessionRelationship
	Usage              map[string]SessionUsage
}

func EmptySessionMetadata() *SessionMetadata {
	return &SessionMetadata{
		Names:              map[string]string{},
		Live:               map[string]string{},
		Summaries:          map[string]string{},
		Entrypoints:        map[string]string{},
		WorkingDirectories: map[string]string{},
		StartedAt:          map[string]int64{},
		LastActivityAt:     map[string]int64{},
		FileEdits:          map[string][]session.FileEdit{},
		Models:             map[string]string{},
		PullRequests:       map[string]int{},
		Relationships:      map[string]SessionRelationship{},
		Usage:              map[string]SessionUsage{},
	}
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
