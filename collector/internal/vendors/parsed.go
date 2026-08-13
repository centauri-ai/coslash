package vendors

import "github.com/centauri-ai/coslash/collector/internal/session"

// ParsedSession is everything a vendor extracts for one session: the
// servable Session plus facts that later collection stages resolve. Vendor
// extraction does no git or liveness probes and writes every field; later
// stages only read them, writing their results into Session.
type ParsedSession struct {
	Session *session.Session

	// consumed while composing parent-child relationships
	ParentID string                    // "" for a root
	SpawnKey string                    // key into the parent's Spawns: claude toolUseId, codex agent id
	Stopped  bool                      // claude meta stoppedByUser, codex lastTurnAborted
	Spawns   map[string]SpawnState     // spawn key → source-derived state
	Commands []session.SubagentCommand // labelled commands for the child projection

	// consumed at resolveNames / resolveStatus
	Name       string  // from the source: title (roots), description/agentType (children)
	InTurn     bool    // roots: refines "interactive" to busy/idle. children: still running
	StatusHint *string // persisted status used when no external live signal exists
}

type SpawnState struct {
	Turn      *int
	Completed bool
}
