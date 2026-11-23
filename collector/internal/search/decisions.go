package search

import "github.com/centauri-ai/coslash/collector/internal/session"

// decisionsForSearch maps producer-owned decisions into the search contract.
// Versioned summaries are authoritative even when empty; KeyDecisions is only
// their cited-text projection and remains a fallback for legacy summaries.
func decisionsForSearch(synthesis *session.SessionSynthesis) []decisionRecord {
	if synthesis == nil {
		return nil
	}
	if synthesis.DecisionsVersion != "" {
		decisions := make([]decisionRecord, 0, len(synthesis.Decisions))
		for _, d := range synthesis.Decisions {
			if d.Text == "" {
				continue
			}
			decision := decisionRecord{DecisionID: d.ID, Kind: "decision", Text: d.Text, Why: d.Why, Alternative: d.Alternative, Status: "decided", Cited: d.Cited, Turns: []int{}}
			if d.Cited {
				seen := make(map[int]bool, len(d.Evidence))
				for _, evidence := range d.Evidence {
					if evidence.Turn > 0 && !seen[evidence.Turn] {
						decision.Turns = append(decision.Turns, evidence.Turn)
						seen[evidence.Turn] = true
					}
				}
				if d.ByYou {
					decision.DecidedBy = "user"
				}
			}
			decisions = append(decisions, decision)
		}
		return decisions
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
