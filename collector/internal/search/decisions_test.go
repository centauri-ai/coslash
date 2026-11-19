package search

import (
	"reflect"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestDecisionsAdapterPreservesExistingTextWithoutInventingEvidence(t *testing.T) {
	if got := decisionsForSearch(nil); len(got) != 0 {
		t.Fatalf("nil synthesis: %+v", got)
	}
	source := &session.SessionSynthesis{KeyDecisions: []string{"Use merge commits", "", "  Keep exact wording  "}}
	got := decisionsForSearch(source)
	want := []decisionRecord{
		{Kind: "decision", Text: "Use merge commits", Status: "decided", Turns: []int{}},
		{Kind: "decision", Text: "  Keep exact wording  ", Status: "decided", Turns: []int{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	got[0].Text = "changed"
	if source.KeyDecisions[0] != "Use merge commits" {
		t.Fatal("adapter mutated synthesis")
	}
}
