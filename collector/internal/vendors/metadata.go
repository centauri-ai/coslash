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

// SessionMetadata carries raw liveness signals, not final status strings: the final
// status needs parse output (InTurn, lastActivity), so resolveStatus computes it.
// A Live value of "interactive" gets the busy/idle refinement; anything else
// passes through. Absent means there is no external live signal; parsed data
// may still provide a persisted or derived status hint.
type SessionEnrichment struct {
	Name, Live, Summary, Entrypoint, WorkingDirectory, Model string
	StartedAt, LastActivityAt                                int64
	FileEdits                                                []session.FileEdit
	PullRequests                                             int
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

func (m *SessionMetadata) Lookup(id string) *SessionEnrichment {
	if m == nil {
		return nil
	}
	return m.Sessions[id]
}

func ApplySessionEnrichment(parsed *ParsedSession, enrichment *SessionEnrichment) {
	if parsed == nil || parsed.Session == nil || enrichment == nil {
		return
	}
	s := parsed.Session
	if enrichment.Summary != "" {
		s.Summary = &enrichment.Summary
	}
	if enrichment.Entrypoint != "" {
		s.Entrypoint = &enrichment.Entrypoint
	}
	if enrichment.WorkingDirectory != "" {
		s.WorkingDirectory = enrichment.WorkingDirectory
	}
	if enrichment.Model != "" {
		s.Model = &enrichment.Model
	}
	if enrichment.StartedAt != 0 {
		s.StartedAt = enrichment.StartedAt
	}
	if enrichment.LastActivityAt != 0 {
		s.LastActivityTime = enrichment.LastActivityAt
	}
	if enrichment.FileEdits != nil {
		s.FileEdits = enrichment.FileEdits
		s.EditedFileCount = len(enrichment.FileEdits)
	}
	s.PullRequests = max(s.PullRequests, enrichment.PullRequests)
	if len(enrichment.Usage.Tokens) > 0 {
		s.Tokens = cloneTokens(enrichment.Usage.Tokens)
	}
	if enrichment.Usage.ContextTokens != nil {
		s.ContextTokens = enrichment.Usage.ContextTokens
	}
	if enrichment.Usage.ContextWindow != nil {
		s.ContextWindow = enrichment.Usage.ContextWindow
	}
	if enrichment.Usage.RecordedCost != nil {
		parsed.RecordedCost = enrichment.Usage.RecordedCost
	}
}

func cloneTokens(source map[string]session.ModelTokens) map[string]session.ModelTokens {
	cloned := make(map[string]session.ModelTokens, len(source))
	for model, tokens := range source {
		cloned[model] = tokens
	}
	return cloned
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
