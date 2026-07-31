package synthesis

import "github.com/centauri-ai/coslash/collector/internal/session"

func Eligible(s *session.Session) bool {
	if s == nil {
		return false
	}
	return s.Turns > 5 || s.Compactions > 0 ||
		(s.ContextTokens != nil && *s.ContextTokens > 100_000)
}
