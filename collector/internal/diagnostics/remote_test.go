package diagnostics

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRemoteDiagnosticsNeverSerializeStderr(t *testing.T) {
	reason := "helper_failed"
	snapshot := CollectWithRemote(context.Background(), "test", false, &RemoteHealth{
		SourceID: "r_0123456789abcdef", Label: "agent-box", State: "error", Complete: false,
		Reason: &reason, Error: "collection helper failed", Transport: "helper",
	})
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(encoded)), "stderr") {
		t.Fatalf("diagnostics exposed stderr: %s", encoded)
	}
}
