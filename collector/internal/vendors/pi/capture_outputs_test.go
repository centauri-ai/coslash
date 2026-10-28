package pi

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestFrozenSessionRetainsCommandOutputsAndPRLinks(t *testing.T) {
	path := captureTranscript(t, "outputs", "",
		`{"type":"message","id":"a","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"pr","name":"bash","arguments":{"command":"gh pr create --fill"}},{"type":"toolCall","id":"skill","name":"read","arguments":{"path":"skills/review/SKILL.md"}}]}}`,
		`{"type":"message","id":"r","parentId":"a","timestamp":"2026-01-01T00:00:02Z","message":{"role":"toolResult","toolCallId":"pr","content":"https://github.com/example/project/pull/42"}}`,
		`{"type":"message","id":"s","parentId":"r","timestamp":"2026-01-01T00:00:03Z","message":{"role":"toolResult","toolCallId":"skill","toolName":"read","content":"captured skill body"}}`,
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
	if !reflect.DeepEqual(parsed.Session.Skills, []session.Skill{{Name: "review", Content: "captured skill body"}}) {
		t.Fatalf("frozen skills = %+v", parsed.Session.Skills)
	}
}
