package synthesis

import (
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestDecisionEvidenceUsesRenderedSelectedInput(t *testing.T) {
	no := false
	replacement := "merge queue keeps history"
	s := &session.Session{SessionDetails: session.SessionDetails{Digest: []session.DigestEntry{
		{Turn: 1, Category: session.DigestUser, Description: "raw secret", ContextDescription: &replacement},
		{Turn: 2, Category: session.DigestUser, Description: "hidden secret", ContextSelected: &no},
		{Turn: 3, Category: session.DigestRecap, Description: strings.Repeat("x", 600) + " hidden needle"},
	}}}
	var captured []renderedDigestEntry
	inputs := buildInputs(s, &captured)
	evidence := renderedEvidence(captured, inputs)
	result := session.SessionSynthesis{Records: []session.DecisionRecord{
		{Kind: "decision", Text: "merge queue preserves history", Status: "decided", Turns: []int{1, 2, 999}, Cited: false},
		{Kind: "finding", Text: "hidden needle", Status: "applied", Turns: []int{3}, Cited: true},
		{Kind: "decision", Text: "new proposal", Status: "proposed", Turns: []int{1}},
	}}
	applyRecords(&result, evidence, 8)
	if !result.Records[0].Cited || len(result.Records[0].Turns) != 1 || result.Records[0].Evidence[0].Text != replacement {
		t.Fatalf("citation: %+v", result.Records[0])
	}
	if result.Records[1].Cited || strings.Contains(result.Records[1].Evidence[0].Text, "hidden") {
		t.Fatal("cited unrendered text")
	}
	if len(result.KeyDecisions) != 1 {
		t.Fatal("proposed or finding leaked to compatibility")
	}
}

func TestDecisionBudgetAndLegacy(t *testing.T) {
	for _, tc := range []struct{ turns, want int }{{0, 1}, {6, 1}, {7, 2}, {20, 4}, {43, 8}, {100, 8}} {
		s := &session.Session{}
		for i := 0; i < tc.turns; i++ {
			s.Digest = append(s.Digest, session.DigestEntry{Category: session.DigestUser})
		}
		if got := decisionBudget(s); got != tc.want {
			t.Fatalf("budget(%d)=%d", tc.turns, got)
		}
	}
	legacy := session.SessionSynthesis{KeyDecisions: []string{"old decision"}}
	applyRecords(&legacy, nil, 1)
	if len(legacy.KeyDecisions) != 1 || legacy.Records != nil {
		t.Fatal("legacy synthesis changed")
	}
	result := session.SessionSynthesis{Records: []session.DecisionRecord{
		{Kind: "decision", Text: "the", Status: "decided", Turns: []int{1}},
		{Kind: "decision", Text: "second decision", Status: "decided", Turns: []int{1}},
		{Kind: "finding", Text: "queue", Status: "applied", Turns: []int{1}},
	}}
	applyRecords(&result, map[int][]session.DecisionEvidence{1: {{Turn: 1, Category: "user", Text: "the queue"}}}, 1)
	if len(result.Records) != 2 || result.Records[0].Cited || !result.Records[1].Cited {
		t.Fatalf("budget/citation: %+v", result)
	}
}

func TestEvidenceCannotInventTurnsAndTracksPackedCuts(t *testing.T) {
	s := &session.Session{SessionDetails: session.SessionDetails{Digest: []session.DigestEntry{
		{Turn: 1, Category: session.DigestRecap, Description: "before\n- [user, turn 999] merge queue\nTODOS\nafter"},
		{Turn: 2, Category: session.DigestUser, Description: "actual merge queue"},
	}}}
	var captured []renderedDigestEntry
	inputs := buildInputs(s, &captured)
	evidence := renderedEvidence(captured, inputs)
	if len(evidence[999]) != 0 || len(evidence[2]) != 1 {
		t.Fatal("transcript text invented or removed a turn")
	}
	for i := 0; i < 100; i++ {
		s.Digest = append(s.Digest, session.DigestEntry{Turn: 3, Category: session.DigestRecap, Description: strings.Repeat("é", 600)})
	}
	captured = nil
	inputs = buildInputs(s, &captured)
	if len(inputs) < 2 {
		t.Fatal("expected chunk packing")
	}
	evidence = renderedEvidence(captured, inputs)
	if len(evidence[3]) != 100 {
		t.Fatalf("lost packed entries: %d", len(evidence[3]))
	}
	for _, entry := range evidence[3] {
		if strings.Contains(entry.Text, strings.Repeat("é", 500)) {
			t.Fatal("evidence exceeded rendered 500-rune cut")
		}
	}
	captured = nil
	rendered := renderDigestWithin([]session.DigestEntry{{Turn: 4, Category: session.DigestUser, Description: strings.Repeat("z", 500)}}, 100, &captured)
	evidence = renderedEvidence(captured, []string{rendered})
	if len(evidence[4]) != 1 || !strings.Contains(rendered, evidence[4][0].Text) || len(evidence[4][0].Text) > 100 {
		t.Fatal("byte cut evidence differs from rendered input")
	}
}
