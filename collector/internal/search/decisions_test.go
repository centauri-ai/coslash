package search

import (
	"reflect"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestDecisionsAdapterUsesStructuredProducerMetadata(t *testing.T) {
	source := &session.SessionSynthesis{DecisionsVersion: "decisions-2026-11-v1", KeyDecisions: []string{"legacy projection must not duplicate"}, Decisions: []session.Decision{
		{ID: "choice", Text: "Use merge commits", Why: "Keep ancestry", Alternative: "Squash commits", Cited: true, ByYou: true, Evidence: []session.Evidence{{Turn: 9}, {Turn: 9}, {Turn: 12}}},
		{ID: "uncited", Text: "Keep isolation", ByYou: true, Evidence: []session.Evidence{{Turn: 4}}},
		{ID: "unattributed", Text: "Keep branch history", Cited: true, Evidence: []session.Evidence{{Turn: 15}}},
	}}
	got := decisionsForSearch(source)
	want := []decisionRecord{
		{DecisionID: "choice", Text: "Use merge commits", Why: "Keep ancestry", Alternative: "Squash commits", Cited: true, DecidedBy: "user", Turns: []int{9, 12}},
		{DecisionID: "uncited", Text: "Keep isolation", Turns: []int{}},
		{DecisionID: "unattributed", Text: "Keep branch history", Cited: true, Turns: []int{15}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	source.Decisions = nil
	if got := decisionsForSearch(source); len(got) != 0 {
		t.Fatalf("empty versioned summary revived legacy decisions: %+v", got)
	}
}
