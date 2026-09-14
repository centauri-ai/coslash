package session

import "testing"

func TestAttachCostDistinguishesRecordedZeroFromUnknown(t *testing.T) {
	recorded := 0.0
	known := &Session{}
	AttachCost(known, &recorded)
	if !known.CostKnown || known.Cost != 0 {
		t.Fatalf("recorded zero cost = known %v, cost %v", known.CostKnown, known.Cost)
	}

	unknown := &Session{}
	AttachCost(unknown, nil)
	if unknown.CostKnown {
		t.Fatal("missing recorded cost marked known")
	}
}
