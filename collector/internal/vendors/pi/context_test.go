package pi

import (
	"path/filepath"
	"testing"
)

func TestSourcePlanSharesRuntimeWithLegacyCollection(t *testing.T) {
	path := captureTranscript(t, "shared-plan", "", userRow("r", "", "shared", "2026-01-01T00:00:01Z"), assistantRow("a", "r", "earlier", 2), assistantRow("b", "r", "latest", 3))
	captureEnvironment(t, filepath.Dir(path))
	leaf := "a"
	owner := runtimeEvidence{Record: RuntimeRecord{RuntimeID: "synthetic", SessionID: "shared-plan", TranscriptPath: path, LeafID: &leaf}}
	snapshot := &RuntimeSnapshot{owners: map[string][]runtimeEvidence{"shared-plan\x00" + path: {owner}}, states: map[string]string{runtimeOwnerKey(owner): "live"}}
	plan, err := PlanSourcesContext(WithRuntimeSnapshot(t.Context(), snapshot))
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithSourcePlan(t.Context(), plan)
	if shared, ok := SourcePlanFromContext(ctx); !ok || shared != plan {
		t.Fatal("source plan was not retained")
	}
	items, _, err := CollectContext(ctx, 0)
	if err != nil || len(items) != 1 || items[0].Session.Summary == nil || *items[0].Session.Summary != "earlier" {
		t.Fatalf("legacy collection replaced the caller's runtime snapshot: %+v, %v", items, err)
	}
}
