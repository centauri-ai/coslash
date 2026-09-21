package session

import "testing"

func TestAttachCostPreservesAuthoritativeZero(t *testing.T) {
	s := &Session{}
	zero := 0.0
	AttachCost(s, &zero)
	if s.Cost == nil || *s.Cost != 0 {
		t.Fatalf("cost = %v, want non-nil zero", s.Cost)
	}
}

func TestAttachCostDoesNotMarkAuthoritativeCostIncomplete(t *testing.T) {
	cost := 1.25
	s := &Session{Tokens: map[string]ModelTokens{
		"gpt-5":          {InputTokens: 100, Cost: .75},
		"unlisted-model": {InputTokens: 100},
	}}
	AttachCost(s, &cost)
	if s.Cost == nil || *s.Cost != cost || s.UnpricedModels == nil || len(s.UnpricedModels) != 0 {
		t.Fatalf("cost = %v, unpriced = %#v; want authoritative cost and empty unpriced list", s.Cost, s.UnpricedModels)
	}
	if got := s.Tokens["gpt-5"].Cost; got != .75 {
		t.Fatalf("model cost = %v, want authoritative provider cost 0.75", got)
	}
}

func TestAttachCostEstimatesWhenTokenRowsExist(t *testing.T) {
	s := &Session{Tokens: map[string]ModelTokens{"gpt-5": {InputTokens: 1_000_000}}}
	AttachCost(s, nil)
	if s.Cost == nil {
		t.Fatal("cost is nil, want estimate")
	}
	if used := s.Tokens["gpt-5"]; used.Cost <= 0 || used.Cost != *s.Cost {
		t.Fatalf("model cost = %v, aggregate = %v; want matching positive estimates", used.Cost, *s.Cost)
	}
}

func TestAttachCostLeavesCostUnknownWithoutRecordedCostOrTokens(t *testing.T) {
	s := &Session{}
	AttachCost(s, nil)
	if s.Cost != nil {
		t.Fatalf("cost = %v, want nil", s.Cost)
	}
}

func TestCloneDeepCopiesFileEditChangeIDs(t *testing.T) {
	source := &Session{SessionDetails: SessionDetails{FileEdits: []FileEdit{
		FileEditWithIdentifiedChanges("example.go", 1, 1, 1, false, []string{"change-1"}, nil),
	}}}

	cloned := Clone(source)
	cloned.FileEdits[0].ChangeIDs[0] = "changed"

	if got := source.FileEdits[0].ChangeIDs[0]; got != "change-1" {
		t.Fatalf("source change ID = %q, want %q", got, "change-1")
	}
}

func TestComposer25FastPricing(t *testing.T) {
	tokens := map[string]ModelTokens{
		"composer-2.5-fast": {InputTokens: 1_000_000, OutputTokens: 1_000_000},
	}
	if got := EstimatedCost(tokens); got != 18 {
		t.Fatalf("cost = %v, want 18", got)
	}
}

func TestLegacyGMIModelStaysPriced(t *testing.T) {
	tokens := map[string]ModelTokens{
		"gmi/google/gemini-3-pro-preview": {InputTokens: 1_000_000, OutputTokens: 1_000_000},
	}
	if got := EstimatedCost(tokens); got != 14 {
		t.Fatalf("cost = %v, want 14", got)
	}
	if got := ContextWindowFor("gmi/google/gemini-3-pro-preview"); got == nil || *got != 1_048_576 {
		t.Fatalf("context window = %v, want 1048576", got)
	}
}

func TestLegacyOpenCodeModelKeepsContextWindow(t *testing.T) {
	if got := ContextWindowFor("opencode/jev-latest"); got == nil || *got != 64_000 {
		t.Fatalf("context window = %v, want 64000", got)
	}
}
