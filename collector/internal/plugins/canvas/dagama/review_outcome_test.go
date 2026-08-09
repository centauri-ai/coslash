package dagama

import "testing"

func TestNormalizeReviewOutcomeFailsClosed(t *testing.T) {
	change := ChangeRecord{ChangeRevision: 4, PatchSha256: string(make([]byte, 64))}
	contents := []byte(`{"schemaVersion":1,"verdict":"approved","summary":"ok","findings":[{"severity":"blocking","file":null,"line":null,"summary":"fix it","detail":""}]}`)
	outcome, err := NormalizeReviewOutcome(contents, change, "review-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Effective != ReviewChangesRequested || outcome.ChangeRevision != 4 || outcome.Attempt != 2 {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if _, err := NormalizeReviewOutcome([]byte(`{"schemaVersion":2,"verdict":"approved"}`), change, "review-1", 1); err == nil {
		t.Fatal("future schema accepted")
	}
	if _, err := NormalizeReviewOutcome([]byte(`{"schemaVersion":1,"verdict":"approved","summary":"ok","findings":[]} {}`), change, "review-1", 1); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if _, err := NormalizeReviewOutcome([]byte(`{"schemaVersion":1,"verdict":"approved","summary":"ok"}`), change, "review-1", 1); err == nil {
		t.Fatal("missing findings accepted")
	}
}
