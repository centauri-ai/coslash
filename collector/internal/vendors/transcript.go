package vendors

import "github.com/centauri-ai/coslash/collector/internal/session"

// ParsedTranscript is everything Parse extracts from one transcript file:
// the servable Session plus the cross-file facts no single file can resolve.
// Parse may read the file and its own sidecar, nothing else — no cross-file
// reads, no git, no liveness probes. Parse writes every field; later stages
// only read them, writing their results into Session.
type ParsedTranscript struct {
	Session *session.Session

	// consumed at applyForkedUsage
	ForkUsage any // claude: message.id → usage. codex: cumulative samples.

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
