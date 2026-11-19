package search

import "github.com/centauri-ai/coslash/collector/internal/session"

// decisionsForSearch is the boundary between synthesis and the search contract.
// The current producer supplies strings only. A future structured schema can be
// mapped here without changing the index, API, CLI, or browser response types.
func decisionsForSearch(synthesis *session.SessionSynthesis) []decisionRecord {
	if synthesis == nil {
		return nil
	}
	decisions := make([]decisionRecord, 0, len(synthesis.KeyDecisions))
	for _, text := range synthesis.KeyDecisions {
		if text == "" {
			continue
		}
		decisions = append(decisions, decisionRecord{Kind: "decision", Text: text, Status: "decided", Turns: []int{}})
	}
	return decisions
}
