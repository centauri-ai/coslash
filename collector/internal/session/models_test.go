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
