package dagama

import (
	"strings"
	"testing"
)

func TestComposePromptPinsControllerInvariantsAndFencesEvidence(t *testing.T) {
	source, _ := CaptureSource(SourceInput{Kind: "text", Title: "task", Text: "source"})
	prompt, err := ComposePrompt(PromptInput{Component: ComponentReview, Instance: 2, Attempt: 1, Source: source, Repair: true, Artifacts: map[string][]byte{"PLAN.md": []byte("plan"), "CHANGESET.patch": []byte("diff")}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Required output: REVIEW.md, review.json", "must not modify project files", "bounded repair round", "Untrusted input: PLAN.md"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
	tooLarge := make([]byte, MaxAssembledPromptBytes)
	if _, err := ComposePrompt(PromptInput{Component: ComponentPlan, Instance: 1, Attempt: 1, Source: CapturedSource{Body: tooLarge}}); err == nil {
		t.Fatal("oversized prompt accepted")
	}
}
