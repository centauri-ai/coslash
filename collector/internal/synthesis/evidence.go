package synthesis

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"golang.org/x/text/unicode/norm"
)

const DecisionsVersion = "decisions-2026-11-v1"

func normalizeEvidenceText(value string) string {
	value = strings.ToLower(norm.NFKC.String(value))
	value = strings.NewReplacer("‘", "'", "’", "'", "“", "\"", "”", "\"", "–", "-", "—", "-", "−", "-", "`", "", "*", "", "_", "").Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

func validDecision(d session.Decision) bool {
	return strings.TrimSpace(d.Text) != "" && utf8.RuneCountInString(d.Text) <= 200 && utf8.RuneCountInString(d.Why) <= 240 && utf8.RuneCountInString(d.Alternative) <= 160
}

func unionEvidence(values ...[]session.Evidence) []session.Evidence {
	all := []session.Evidence{}
	seen := map[string]bool{}
	for _, entries := range values {
		for _, e := range entries {
			if e.Turn < 1 || e.Entry < 0 || utf8.RuneCountInString(e.Quote) > 160 {
				continue
			}
			key := fmt.Sprintf("%d:%d:%s:%s", e.Turn, e.Entry, e.SourceEntryID, normalizeEvidenceText(e.Quote))
			if !seen[key] {
				all = append(all, e)
				seen[key] = true
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Turn != all[j].Turn {
			return all[i].Turn < all[j].Turn
		}
		return all[i].Entry < all[j].Entry
	})
	return all[:min(len(all), 3)]
}

func normalizeDecisions(s *session.SessionSynthesis) {
	if s.DecisionsVersion == "" {
		return
	}
	if len(s.DesignDoc) > 500 {
		s.DesignDoc = ""
	}
	kept := []session.Decision{}
	seen := map[string]int{}
	for _, d := range s.Decisions {
		if !validDecision(d) {
			continue
		}
		d.Text = strings.TrimSpace(d.Text)
		normalized := normalizeEvidenceText(d.Text)
		if previous, ok := seen[normalized]; ok {
			kept[previous].Evidence = unionEvidence(kept[previous].Evidence, d.Evidence)
			kept[previous].From = append(kept[previous].From, d.From...)
			kept[previous].ByYou = kept[previous].ByYou || d.ByYou
			continue
		}
		if len(kept) == 5 {
			continue
		}
		sum := sha256.Sum256([]byte(normalized))
		d.ID = hex.EncodeToString(sum[:6])
		d.Evidence = unionEvidence(d.Evidence)
		seen[normalized] = len(kept)
		kept = append(kept, d)
	}
	s.Decisions = kept
}

func checkEvidence(s *session.Session, result *session.SessionSynthesis) {
	normalizeDecisions(result)
	if result.DecisionsVersion == "" {
		return
	}
	visible := sourceEvidence(s)
	validDoc := false
	if s != nil {
		for _, edit := range s.FileEdits {
			if edit.Path == result.DesignDoc && strings.HasSuffix(edit.Path, ".md") {
				validDoc = true
				break
			}
		}
	}
	if !validDoc {
		result.DesignDoc = ""
	}
	for i := range result.Decisions {
		d := &result.Decisions[i]
		matched := []session.Evidence{}
		userMatch := false
		for _, e := range d.Evidence {
			quote := normalizeEvidenceText(e.Quote)
			if len(strings.Fields(quote)) < 4 || s == nil {
				continue
			}
			for index, entry := range s.Digest {
				if entry.Turn != e.Turn {
					continue
				}
				description, answer := visible[index].description, visible[index].answer
				descriptionMatch := strings.Contains(normalizeEvidenceText(description), quote)
				answerMatch := strings.Contains(normalizeEvidenceText(answer), quote)
				if !descriptionMatch && !answerMatch {
					continue
				}
				e.Entry = index
				e.SourceEntryID = entry.SourceEntryID
				matched = append(matched, e)
				userMatch = userMatch || (descriptionMatch && (entry.Category == "user" || entry.Category == "first_prompt")) || (answerMatch && entry.Category == "question")
				break
			}
		}
		d.Evidence = unionEvidence(matched)
		d.Cited = len(d.Evidence) > 0
		d.ByYou = d.ByYou && userMatch
	}
	finalizeDecisions(result)
}

func carryEvidence(result *session.SessionSynthesis, partials []session.SessionSynthesis, indices []int) {
	available := map[string][]session.Evidence{}
	for _, p := range indices {
		for n, source := range partials[p].Decisions {
			available[fmt.Sprintf("p%d.%d", p+1, n+1)] = source.Evidence
		}
	}
	for i := range result.Decisions {
		d := &result.Decisions[i]
		d.Evidence = []session.Evidence{}
		for _, ref := range d.From {
			d.Evidence = unionEvidence(d.Evidence, available[ref])
		}
	}
}

// Derive visibility from the same renderer and budgets as source prompts, never
// from labels that transcript text could forge inside other untrusted fields.
func sourceEvidence(s *session.Session) map[int]evidenceText {
	visible := map[int]evidenceText{}
	if s == nil {
		return visible
	}
	selected := session.SelectedDigest(s.Digest)
	full := len(buildPrompt(s, selected, true, "DIGEST (chronological)")) <= maxPromptBytes
	prefix := untrustedSessionInstruction + untrustedSessionBegin + renderChunkContext(s) + "\nDIGEST CHUNK (chronological)\n"
	suffix := "\n" + limitBytes(renderFacts(s), maxMergeFactsBytes) + untrustedSessionEnd
	budget := maxPromptBytes - len(prefix) - len(suffix)
	indices := make([]int, 0, len(selected))
	for i, entry := range s.Digest {
		if entry.ContextSelected == nil || *entry.ContextSelected {
			indices = append(indices, i)
		}
	}
	offset := 0
	for _, group := range digestGroups(selected) {
		entryBudget := maxPromptBytes
		if !full && len(renderDigest(group)) > budget {
			entryBudget = budget - len(fmt.Sprintf("TURN %d CONTINUATION\n", group[0].Turn))
		}
		for _, entry := range group {
			_, text := renderDigestEntry(entry, entryBudget)
			visible[indices[offset]] = text
			offset++
		}
	}
	return visible
}
