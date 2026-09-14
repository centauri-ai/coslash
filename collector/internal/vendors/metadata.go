package vendors

import (
	"log"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

type SessionUsage struct {
	Tokens        map[string]session.ModelTokens
	ContextTokens *int
	ContextWindow *int
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
type SessionEnrichment struct {
	Name, Live, Summary, Entrypoint, WorkingDirectory, Model string
	StartedAt, LastActivityAt                                int64
	FileEdits                                                []session.FileEdit
	CommitObservations                                       []session.CommitObservation
	PullRequests                                             int
	Relationship                                             SessionRelationship
	Usage                                                    SessionUsage
}

type SessionMetadata struct {
	Sessions map[string]*SessionEnrichment
}

func EmptySessionMetadata() *SessionMetadata {
	return &SessionMetadata{Sessions: map[string]*SessionEnrichment{}}
}

func (m *SessionMetadata) Session(id string) *SessionEnrichment {
	if m.Sessions[id] == nil {
		m.Sessions[id] = &SessionEnrichment{}
	}
	return m.Sessions[id]
}

func (m *SessionMetadata) LiveSessions() map[string]string {
	live := map[string]string{}
	for id, value := range m.Sessions {
		if value.Live != "" {
			live[id] = value.Live
		}
	}
	return live
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
