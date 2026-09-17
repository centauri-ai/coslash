package vendors

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestApplySessionEnrichmentCopiesTokenMap(t *testing.T) {
	metadataTokens := map[string]session.ModelTokens{"gpt-5": {InputTokens: 10}}
	parsed := &ParsedSession{Session: &session.Session{}}
	ApplySessionEnrichment(parsed, &SessionEnrichment{Usage: SessionUsage{Tokens: metadataTokens}})

	used := parsed.Session.Tokens["gpt-5"]
	used.Cost = 1
	parsed.Session.Tokens["gpt-5"] = used
	if got := metadataTokens["gpt-5"].Cost; got != 0 {
		t.Fatalf("metadata token cost mutated to %v", got)
	}
}
