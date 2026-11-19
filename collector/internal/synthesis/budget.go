package synthesis

import "github.com/centauri-ai/coslash/collector/internal/session"

const (
	humanTurnsPerRecord = 6
	maxRecordsPerKind   = 8
)

func decisionBudget(s *session.Session) int {
	n := 0
	for _, e := range session.SelectedDigest(s.Digest) {
		switch e.Category {
		case session.DigestFirstPrompt, session.DigestUser, session.DigestQuestion:
			n++
		}
	}
	return min(maxRecordsPerKind, max(1, (n+humanTurnsPerRecord-1)/humanTurnsPerRecord))
}
