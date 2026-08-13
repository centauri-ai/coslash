package vendors

import "github.com/centauri-ai/coslash/collector/internal/session"

// ParsedSession holds source-derived session data plus facts consumed by
// shared composition and enrichment stages.
type ParsedSession struct {
	Session *session.Session
	LogPath string // optional for sources that do not read transcript files

	// consumed while composing parent-child relationships
	ParentID string                    // "" for a root
	SpawnKey string                    // key into the parent's Spawns: tool ID or child session ID
	Stopped  bool                      // source-derived terminal state for aborted children
	Spawns   map[string]SpawnState     // spawn key → source-derived state
	Commands []session.SubagentCommand // labelled commands for the child projection

	// consumed at resolveNames / resolveStatus
	Name       string  // from the source: title (roots), description/agentType (children)
	InTurn     bool    // roots: refines "interactive" to busy/idle. children: still running
	StatusHint *string // persisted status used when no external live signal exists

	RecordedCost *float64 // authoritative source cost; nil means estimate from tokens
}

type SpawnState struct {
	Turn      *int
	Completed bool
}
