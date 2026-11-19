package synthesis

import (
	"strings"
	"unicode"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

// Evidence is captured from structured entries during rendering. Matching the
// complete rendered block associates each entry with the chunk that kept it.
func renderedEvidence(captured []renderedDigestEntry, inputs []string) map[int][]session.DecisionEvidence {
	result := map[int][]session.DecisionEvidence{}
	for _, entry := range captured {
		for _, input := range inputs {
			if strings.Contains(input, entry.block) {
				result[entry.evidence.Turn] = append(result[entry.evidence.Turn], entry.evidence)
				break
			}
		}
	}
	return result
}

func keyTerms(text string) map[string]bool {
	stop := " a an the and or of to in on for with is are was were be been it this that we they i you use "
	terms := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if !strings.Contains(stop, " "+word+" ") {
			terms[word] = true
		}
	}
	return terms
}

func applyRecords(s *session.SessionSynthesis, evidence map[int][]session.DecisionEvidence, budget int) {
	if s.Records == nil {
		return
	}
	kept := make([]session.DecisionRecord, 0)
	counts, seen := map[string]int{}, map[string]bool{}
	for _, r := range s.Records {
		if (r.Kind != "decision" && r.Kind != "finding") || counts[r.Kind] >= budget {
			continue
		}
		switch r.Status {
		case "proposed", "decided", "applied", "superseded":
		default:
			continue
		}
		r.Text = limited(r.Text, 500)
		if r.Text == "" || seen[r.Kind+"\x00"+r.Text] {
			continue
		}
		seen[r.Kind+"\x00"+r.Text] = true
		r.Why, r.Alternative, r.Area = limited(r.Why, 500), limited(r.Alternative, 500), limited(r.Area, 100)
		if r.Kind == "finding" {
			r.Alternative = ""
		}
		switch r.DecidedBy {
		case "user", "agent_confirmed", "agent":
		default:
			r.DecidedBy = ""
		}
		turns := r.Turns
		r.Turns, r.Evidence, r.Cited = []int{}, nil, false
		terms, seenTurns := keyTerms(r.Text), map[int]bool{}
		for _, turn := range turns {
			entries := evidence[turn]
			if len(entries) == 0 || seenTurns[turn] {
				continue
			}
			seenTurns[turn] = true
			r.Turns = append(r.Turns, turn)
			var text strings.Builder
			for _, entry := range entries {
				text.WriteString(entry.Text + " ")
				if len(r.Evidence) < 3 {
					r.Evidence = append(r.Evidence, entry)
				}
			}
			words, matches := keyTerms(text.String()), 0
			for term := range terms {
				if words[term] {
					matches++
				}
			}
			if len(terms) > 0 && matches >= min(2, len(terms)) {
				r.Cited = true
			}
		}
		counts[r.Kind]++
		kept = append(kept, r)
	}
	s.Records, s.KeyDecisions = kept, []string{}
	for _, r := range kept {
		if r.Kind == "decision" && (r.Status == "decided" || r.Status == "applied") {
			s.KeyDecisions = append(s.KeyDecisions, r.Text)
		}
	}
}
