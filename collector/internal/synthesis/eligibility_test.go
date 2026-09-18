package synthesis

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestEligibleIncludesCompactionSeed(t *testing.T) {
	if !Eligible(&session.Session{SessionDetails: session.SessionDetails{CompactionSeed: "prior compacted context"}}) {
		t.Fatal("seed-bearing session is not eligible for synthesis")
	}
}
