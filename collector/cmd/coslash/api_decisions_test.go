package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestSynthesisJSONCollectionsPreservesUncitedAndCaller(t *testing.T) {
	original := &session.SessionSynthesis{
		DecisionsVersion: "decisions-2026-11-v1",
		Decisions:        []session.Decision{{Text: "Uncited decision"}},
	}
	got := synthesisWithJSONCollections(original)
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"goals":[]`, `"keyDecisions":[]`, `"designDoc":""`, `"evidence":[]`, `"cited":false`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
	got.Decisions[0].Text = "changed"
	if original.Decisions[0].Text != "Uncited decision" || original.Decisions[0].Evidence != nil || original.Goals != nil {
		t.Fatal("API normalization mutated the cached summary")
	}
	empty, err := json.Marshal(synthesisWithJSONCollections(&session.SessionSynthesis{}))
	if err != nil || !strings.Contains(string(empty), `"decisions":[]`) {
		t.Fatalf("empty decisions must be an array: %s, %v", empty, err)
	}
	if synthesisWithJSONCollections(nil) != nil {
		t.Fatal("missing synthesis became an empty summary")
	}
}
