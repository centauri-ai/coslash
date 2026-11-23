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

func TestDecisionsAdapterUsesStructuredProducerMetadata(t *testing.T) {
	source := &session.SessionSynthesis{DecisionsVersion: "decisions-2026-11-v1", KeyDecisions: []string{"legacy projection must not duplicate"}, Decisions: []session.Decision{
		{ID: "choice", Text: "Use merge commits", Why: "Keep ancestry", Alternative: "Squash commits", Cited: true, ByYou: true, Evidence: []session.Evidence{{Turn: 9}, {Turn: 9}, {Turn: 12}}},
		{ID: "uncited", Text: "Keep isolation", ByYou: true, Evidence: []session.Evidence{{Turn: 4}}},
		{ID: "unattributed", Text: "Keep branch history", Cited: true, Evidence: []session.Evidence{{Turn: 15}}},
	}}
	got := decisionsForSearch(source)
	want := []decisionRecord{
		{DecisionID: "choice", Kind: "decision", Text: "Use merge commits", Why: "Keep ancestry", Alternative: "Squash commits", Status: "decided", Cited: true, DecidedBy: "user", Turns: []int{9, 12}},
		{DecisionID: "uncited", Kind: "decision", Text: "Keep isolation", Status: "decided", Turns: []int{}},
		{DecisionID: "unattributed", Kind: "decision", Text: "Keep branch history", Status: "decided", Cited: true, Turns: []int{15}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	got[0].Turns[0] = 100
	if source.Decisions[0].Evidence[0].Turn != 9 {
		t.Fatal("adapter mutated producer evidence")
	}
	source.Decisions = nil
	if got := decisionsForSearch(source); len(got) != 0 {
		t.Fatalf("empty versioned summary revived legacy decisions: %+v", got)
	}
}
