package vendors

import "github.com/centauri-ai/coslash/collector/internal/session"

// ParsedTranscript is everything a vendor extracts for one session: the
// servable Session plus facts that later collection stages resolve. Vendor
// extraction does no git or liveness probes and writes every field; later
// stages only read them, writing their results into Session.
type ParsedTranscript struct {
	Session *session.Session

	// consumed at groupSubagents — the parent↔child linkage
	ParentID   string                    // "" for a root
	SpawnKey   string                    // key into the parent's SpawnTurns: claude toolUseId, codex agent id
	Stopped    bool                      // claude meta stoppedByUser, codex lastTurnAborted
	SpawnTurns map[string]int            // spawn key → turn, resolved for children
	Completed  map[string]struct{}       // claude: tool_result ids seen; feeds child status
	Commands   []session.SubagentCommand // labelled commands for the child projection

	// consumed at resolveNames / resolveStatus
	Name   string // from the file: transcript title (roots), meta description/agentType (children)
	InTurn bool   // roots: refines "interactive" to busy/idle. children: still running
}
