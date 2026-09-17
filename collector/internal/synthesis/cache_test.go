package synthesis

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestLookupLatestIgnoresPreviewRevisionDrift(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	cache := NewCache()
	want := session.SessionSynthesis{Outcome: "shipped"}
	if err := cache.Store("session", Record{Revision: 42, Synthesis: want}); err != nil {
		t.Fatal(err)
	}
	if got := cache.LookupLatest("session"); got == nil || got.Outcome != want.Outcome {
		t.Fatalf("LookupLatest() = %#v", got)
	}
}
