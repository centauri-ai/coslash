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

func TestAttachCostEstimatesWhenTokenRowsExist(t *testing.T) {
	s := &Session{Tokens: map[string]ModelTokens{"gpt-5": {InputTokens: 1}}}
	AttachCost(s, nil)
	if s.Cost == nil {
		t.Fatal("cost is nil, want estimate")
	}
}

func TestAttachCostLeavesCostUnknownWithoutRecordedCostOrTokens(t *testing.T) {
	s := &Session{}
	AttachCost(s, nil)
	if s.Cost != nil {
		t.Fatalf("cost = %v, want nil", s.Cost)
	}
}
