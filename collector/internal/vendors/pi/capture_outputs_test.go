package pi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFrozenSessionRetainsCommandOutputsAndPRLinks(t *testing.T) {
	path := captureTranscript(t, "outputs", "",
		`{"type":"message","id":"a","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"pr","name":"bash","arguments":{"command":"gh pr create --fill"}}]}}`,
		`{"type":"message","id":"r","parentId":"a","timestamp":"2026-01-01T00:00:02Z","message":{"role":"toolResult","toolCallId":"pr","content":"https://github.com/example/project/pull/42"}}`,
	)
	captureEnvironment(t, filepath.Dir(path))
	plan, err := PlanSourcesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := plan.SessionInputsContext(t.Context(), "outputs")
	if err != nil {
		t.Fatal(err)
	}
	files := freezeInputs(t, inputs)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	parsed, _, err := ParseFrozenSessionContext(t.Context(), inputs, files)
	if err != nil {
		t.Fatal(err)
	}
	output := "https://github.com/example/project/pull/42"
	assertCommandOutputs(t, parsed, []*string{&output})
	if parsed.Session.Commands[0] != "gh pr create --fill" || parsed.Session.PullRequests != 1 || len(parsed.Session.PullRequestLinks) != 1 || parsed.Session.PullRequestLinks[0].URL != output {
		t.Fatalf("frozen command observations = %+v", parsed.Session)
	}
}
