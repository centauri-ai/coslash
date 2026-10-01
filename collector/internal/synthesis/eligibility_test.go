package synthesis

import (
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestEligibleIncludesCompactionSeed(t *testing.T) {
	if !Eligible(&session.Session{SessionDetails: session.SessionDetails{CompactionSeed: "prior compacted context"}}) {
		t.Fatal("seed-bearing session is not eligible for synthesis")
	}
}

func TestEligibleGrokSessionUsesExistingPipeline(t *testing.T) {
	t.Setenv("PATH", "")
	prompt := "add grok support"
	long := &session.Session{Agent: vendors.AgentGrok, SessionDetails: session.SessionDetails{Turns: 6, FirstPrompt: &prompt}}
	if !Eligible(long) {
		t.Fatal("grok session with 6 turns is not eligible for synthesis")
	}
	if input := BuildInput(long); !strings.Contains(input, prompt) {
		t.Fatalf("BuildInput omits the grok first prompt:\n%s", input)
	}
	if Eligible(&session.Session{Agent: vendors.AgentGrok, SessionDetails: session.SessionDetails{Turns: 1}}) {
		t.Fatal("grok session with 1 turn and no compaction seed is eligible for synthesis")
	}
}
