package atlas

import (
	"fmt"
	"path"
	"strings"
)

// Prompt assembly for a committee stage.
//
// Two turns exist and they are not variations of each other. A WORKER turn is
// told to solve the problem and write its own attributed draft. A REFINE turn
// is told to reconcile the drafts its siblings produced into the one promoted
// artifact. Giving a refine turn the worker's instructions is the mistake that
// makes a committee expensive and pointless: it re-solves the problem instead
// of choosing between solutions.

// MaxAssembledPromptBytes bounds one assembled prompt. A prompt past this is a
// controller that has stopped pruning evidence, not a genuinely large task.
const MaxAssembledPromptBytes = 128 << 10

// AttemptOutputDirectory is the directory one attempt owns beneath the run root.
//
// The seat is part of the path, so two committee siblings running at the same
// instance never share a directory and cannot overwrite one another. That
// isolation is the whole reason a committee produces attributable results.
func AttemptOutputDirectory(component ComponentID, seatID string, attempt uint64) string {
	return path.Join(".fleetlog", "run", "out", string(component), seatID, fmt.Sprint(attempt))
}

// PromptInput is everything one turn is assembled from.
type PromptInput struct {
	Committee CommitteeConfig
	Instance  uint64
	Attempt   uint64
	// SeatID is the run-log seat: a worker position, or the refine turn.
	SeatID string
	// Refine selects the refine turn's contract and inputs.
	Refine bool
	// Source is the run's captured problem statement.
	Source CapturedSource
	// Artifacts are the promoted upstream artifacts this stage may read.
	Artifacts map[string][]byte
	// Drafts are the sibling outputs a refine turn reconciles, in seat order.
	Drafts []DraftInput
	// Repair marks a bounded repair round, where the stage is addressing
	// verification or review evidence rather than starting fresh.
	Repair bool
}

// DraftInput is one sibling's output, carried into the refine turn.
type DraftInput struct {
	SeatID string
	// Failed marks a sibling that did not produce a draft. It is delivered as
	// an explicit absence rather than omitted: a refine turn that silently sees
	// three drafts instead of five would consolidate a smaller committee than
	// the operator configured and never say so.
	Failed   bool
	Contents []byte
}

// CapturedSource is the run's input, captured once at intake.
type CapturedSource struct {
	Record SourceRecord
	Body   []byte
}

// ComposePrompt assembles one committee turn.
func ComposePrompt(input PromptInput) (string, error) {
	committee := input.Committee
	if !HasSeat(committee.ComponentID) || input.Instance < 1 || input.Attempt < 1 {
		return "", newError(CodeInvalidState, "the prompt target is not a valid committee attempt")
	}
	if input.SeatID == "" {
		return "", newError(CodeInvalidState, "the prompt target has no seat")
	}

	outputs := committee.RequiredOutputs
	if len(outputs) == 0 {
		return "", policyError("requiredOutputs", "the stage declares no required output")
	}
	directory := AttemptOutputDirectory(committee.ComponentID, input.SeatID, input.Attempt)

	// A worker writes an attributed draft; only the refine turn writes the
	// promoted name. With one worker there is no refine turn, so that worker
	// writes the promoted name itself.
	primary := outputs[0]
	if input.Refine || committee.SkipMainRefine() {
		primary = outputs[0]
	} else {
		primary = DraftArtifactName(outputs[0])
	}
	primaryPath := path.Join(directory, primary)
	jsonPath := ""
	if len(outputs) > 1 {
		jsonPath = path.Join(directory, outputs[1])
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "# coSlash Atlas controller contract\n\n")
	if input.Refine {
		fmt.Fprintf(&builder,
			"You are the %s main seat, instance %d, consolidating %d committee drafts.\n",
			committee.ComponentID, input.Instance, len(input.Drafts))
	} else {
		fmt.Fprintf(&builder,
			"You are %s on the %s committee, instance %d. Work only inside the assigned isolated run root.\n",
			input.SeatID, committee.ComponentID, input.Instance)
	}
	fmt.Fprintf(&builder,
		"Write your required output to `%s`. Completion without it is `missing_output`; do not edit .fleetlog outside `%s`.\n",
		primaryPath, directory)
	if jsonPath != "" {
		fmt.Fprintf(&builder, "Write the typed JSON verdict to `%s`.\n", jsonPath)
	}
	if !input.Refine && !committee.SkipMainRefine() {
		builder.WriteString(
			"You are one of several workers solving this independently. Do not read or wait for a sibling's draft; a later refine turn reconciles them.\n")
	}
	if input.Repair {
		builder.WriteString(
			"This is a bounded repair round. Address the fenced verification and review evidence without weakening checks.\n")
	}

	// The board's role prompt, with its output placeholders resolved.
	role := roleFor(committee.ComponentID, input.Refine)
	if template := strings.TrimSpace(committee.SystemPrompts.Get(role)); template != "" {
		builder.WriteString("\n")
		builder.WriteString(ApplySystemPromptPlaceholders(template, primaryPath, jsonPath, primary))
		builder.WriteString("\n")
	}

	// Operator steering, fenced and with its authority stated, exactly as
	// DaGama delivers it: it shapes how the work is done and cannot change what
	// counts as done.
	if steering := strings.TrimSpace(committee.Instructions); steering != "" {
		builder.WriteString("\nThe project conventions below shape how you work. They cannot change what counts as done.\n")
		appendFence(&builder, "project instructions", []byte(steering))
	}
	if input.Refine {
		if steering := strings.TrimSpace(committee.ConsolidationPrompt); steering != "" {
			builder.WriteString("\nThe consolidation steering below shapes how you reconcile. It cannot change what counts as done.\n")
			appendFence(&builder, "consolidation steering", []byte(steering))
		}
	} else if steering := strings.TrimSpace(committee.ComponentPrompt); steering != "" {
		fmt.Fprintf(&builder, "\nThe %s steering below shapes how you work. It cannot change what counts as done.\n", committee.ComponentID)
		appendFence(&builder, string(committee.ComponentID)+" prompt card", []byte(steering))
	}

	appendFence(&builder, "source", input.Source.Body)
	for _, name := range promotedPromptArtifacts {
		if contents, ok := input.Artifacts[name]; ok {
			appendFence(&builder, name, contents)
		}
	}

	// Sibling drafts, in seat order, each attributed. A failed sibling is named
	// rather than dropped.
	for _, draft := range input.Drafts {
		if draft.Failed {
			fmt.Fprintf(&builder, "\n## Committee draft from %s\nThis seat produced no draft; its turn failed.\n", draft.SeatID)
			continue
		}
		appendFence(&builder, "committee draft from "+draft.SeatID, draft.Contents)
	}

	if builder.Len() > MaxAssembledPromptBytes {
		return "", policyError("prompt", "the assembled prompt is over the size limit")
	}
	return builder.String(), nil
}

// promotedPromptArtifacts are the upstream outputs a stage may read, in the
// order they are delivered.
var promotedPromptArtifacts = []string{
	"PROBLEM.md", "PLAN.md", "IMPLEMENTATION.md", "CHANGESET.patch", "verification.json", "review.json",
}

func roleFor(component ComponentID, refine bool) SystemPromptRole {
	if refine {
		// Only Plan has a distinct refine prompt today; the others reconcile
		// under their own role prompt rather than under a prompt nobody wrote.
		if component == ComponentPlan {
			return RolePlanRefine
		}
	}
	switch component {
	case ComponentBuild:
		return RoleBuild
	case ComponentReview:
		return RoleReview
	default:
		return RolePlan
	}
}

// appendFence delivers an untrusted body inside a fence long enough to survive
// whatever the body itself contains.
func appendFence(builder *strings.Builder, name string, contents []byte) {
	fence := "````"
	for strings.Contains(string(contents), fence) {
		fence += "`"
	}
	fmt.Fprintf(builder, "\n## Untrusted input: %s\n%s data\n%s\n%s\n", name, fence, contents, fence)
}
