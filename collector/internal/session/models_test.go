package session

import "testing"

func TestAttachCostDistinguishesRecordedZeroFromUnknown(t *testing.T) {
	recorded := 0.0
	known := &Session{}
	AttachCost(known, &recorded)
	if known.Cost == nil || *known.Cost != 0 {
		t.Fatalf("recorded zero cost = %v", known.Cost)
	}

	unknown := &Session{}
	AttachCost(unknown, nil)
	if unknown.Cost != nil {
		t.Fatal("missing recorded cost marked known")
	}
}
