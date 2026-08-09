package dagama

import (
	"fmt"
	"strings"
)

const MaxAssembledPromptBytes = 128 << 10

type PromptInput struct {
	Component ComponentID
	Instance  int
	Attempt   int
	Source    CapturedSource
	Artifacts map[string][]byte
	Repair    bool
}

func ComposePrompt(input PromptInput) (string, error) {
	if !HasSeat(input.Component) || input.Instance < 1 || input.Attempt < 1 {
		return "", newError(CodeInvalidState, "the prompt target is not a valid seat attempt")
	}
	output := requiredOutputs(input.Component)
	var builder strings.Builder
	fmt.Fprintf(&builder, "# coSlash DaGama controller contract\n\n")
	fmt.Fprintf(&builder, "You are the %s seat, instance %d. Work only inside the assigned isolated run root.\n", input.Component, input.Instance)
	fmt.Fprintf(&builder, "Required output: %s. Completion without it is `missing_output`; do not edit .fleetlog outside your attempt output directory.\n", strings.Join(output, ", "))
	fmt.Fprintf(&builder, "Write required output files beneath `.fleetlog/run/out/%s/%s-1/%d/` relative to the run root.\n", input.Component, input.Component, input.Attempt)
	if input.Component == ComponentReview {
		builder.WriteString("Review must not modify project files. The controller compares the tree before and after and fails closed on mutation.\n")
		builder.WriteString("Write review.json as exactly this agent-authored schema: {\"schemaVersion\":1,\"verdict\":\"approved|changes_requested\",\"summary\":\"...\",\"findings\":[{\"severity\":\"blocking|advisory\",\"file\":null,\"line\":null,\"summary\":\"...\",\"detail\":\"...\"}]}.\n")
		builder.WriteString("Do not write controller fields changeRevision, patchSha256, effectiveVerdict, seatId, or attempt. The controller derives them and approved requires zero blocking findings.\n")
	}
	if input.Repair {
		builder.WriteString("This is a bounded repair round. Address the fenced verification/review evidence without weakening checks.\n")
	}
	appendPromptFence(&builder, "source", input.Source.Body)
	for _, name := range []string{"PROBLEM.md", "PLAN.md", "CHANGESET.patch", "verification.json", "review.json"} {
		if contents, ok := input.Artifacts[name]; ok {
			appendPromptFence(&builder, name, contents)
		}
	}
	if builder.Len() > MaxAssembledPromptBytes {
		return "", policyError("prompt", "the assembled prompt is over the size limit")
	}
	return builder.String(), nil
}

func requiredOutputs(component ComponentID) []string {
	switch component {
	case ComponentPlan:
		return []string{"PLAN.md"}
	case ComponentBuild:
		return []string{"IMPLEMENTATION.md"}
	case ComponentReview:
		return []string{"REVIEW.md", "review.json"}
	default:
		return nil
	}
}

func appendPromptFence(builder *strings.Builder, name string, contents []byte) {
	fence := "````"
	for strings.Contains(string(contents), fence) {
		fence += "`"
	}
	fmt.Fprintf(builder, "\n## Untrusted input: %s\n%s data\n%s\n%s\n", name, fence, contents, fence)
}
