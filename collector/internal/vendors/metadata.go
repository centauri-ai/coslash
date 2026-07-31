package vendors

// SessionMetadata carries raw liveness signals, not final status strings: the final
// status needs parse output (InTurn, lastActivity), so resolveStatus computes it.
// A Live value of "interactive" gets the busy/idle refinement; anything else
// passes through. Absent means not live.
type SessionMetadata struct {
	Names map[string]string // id → externally-assigned name; Parsed.Name is the fallback
	Live  map[string]string // id → raw status
}
