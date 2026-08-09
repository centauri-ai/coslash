package atlas

import (
	"strings"
	"testing"
)

// A committee only produces attributable results if two things hold: no two
// siblings can write to the same place, and the refine turn is told what its
// siblings actually produced — including the ones that produced nothing.

func planCommittee(t *testing.T, workers int) CommitteeConfig {
	t.Helper()
	board := DefaultBoard()
	component := board.ComponentByLegacyRole(ComponentPlan)
	if component == nil {
		t.Fatal("the default board has no plan seat")
	}
	seats := make([]WorkerSeat, 0, workers)
	for index := range workers {
		base := DefaultSeatForVendor(VendorClaude)
		seat := WorkerSeat{
			ID:         WorkerSeatID(component.ID, index),
			Vendor:     base.Vendor,
			Model:      base.Model,
			Effort:     base.Effort,
			Permission: base.Permission,
		}
		if index == 0 && workers > 1 {
			seat.Role = RoleMain
		}
		seats = append(seats, seat)
	}
	component.Seats = seats
	Normalize(board)

	committee, err := CommitteeFor(board, ComponentPlan)
	if err != nil {
		t.Fatalf("CommitteeFor: %v", err)
	}
	if len(committee.Workers) != workers {
		t.Fatalf("resolved %d workers, want %d", len(committee.Workers), workers)
	}
	return committee
}

func composeFor(t *testing.T, input PromptInput) string {
	t.Helper()
	if input.Instance == 0 {
		input.Instance = 1
	}
	if input.Attempt == 0 {
		input.Attempt = 1
	}
	prompt, err := ComposePrompt(input)
	if err != nil {
		t.Fatalf("ComposePrompt: %v", err)
	}
	return prompt
}

func TestCommitteeSiblingsNeverShareAnOutputDirectory(t *testing.T) {
	seen := map[string]string{}
	for _, seat := range []string{
		AttemptSeatID(ComponentPlan, 0),
		AttemptSeatID(ComponentPlan, 1),
		AttemptSeatID(ComponentPlan, 2),
		MainRefineSeatID(ComponentPlan),
	} {
		directory := AttemptOutputDirectory(ComponentPlan, seat, 1)
		if previous, taken := seen[directory]; taken {
			t.Fatalf("seats %s and %s share the directory %s", previous, seat, directory)
		}
		seen[directory] = seat
	}
	// A retry is a different attempt and must not reuse the first one's
	// directory, or a failed turn's leftovers would be read as this turn's work.
	first := AttemptOutputDirectory(ComponentPlan, AttemptSeatID(ComponentPlan, 0), 1)
	second := AttemptOutputDirectory(ComponentPlan, AttemptSeatID(ComponentPlan, 0), 2)
	if first == second {
		t.Fatalf("attempts 1 and 2 share the directory %s", first)
	}
}

func TestAWorkerWritesAnAttributedDraftAndTheRefineTurnWritesThePromotedName(t *testing.T) {
	committee := planCommittee(t, 3)

	worker := composeFor(t, PromptInput{
		Committee: committee,
		SeatID:    AttemptSeatID(ComponentPlan, 0),
	})
	if !strings.Contains(worker, "PLAN.draft.md") {
		t.Fatalf("a committee worker was not told to write a draft:\n%s", worker)
	}
	if strings.Contains(worker, "/PLAN.md`") {
		t.Fatalf("a committee worker was told to write the promoted artifact:\n%s", worker)
	}

	refine := composeFor(t, PromptInput{
		Committee: committee,
		SeatID:    MainRefineSeatID(ComponentPlan),
		Refine:    true,
	})
	if !strings.Contains(refine, "PLAN.md") || strings.Contains(refine, "PLAN.draft.md`") {
		t.Fatalf("the refine turn was not told to write the promoted artifact:\n%s", refine)
	}
}

func TestASoleWorkerWritesThePromotedArtifactDirectly(t *testing.T) {
	// With one worker there is no refine turn, so routing it through a draft
	// would spend an extra agent turn to copy a file.
	committee := planCommittee(t, 1)
	if !committee.SkipMainRefine() {
		t.Fatal("a one-worker committee still wants a refine turn")
	}
	prompt := composeFor(t, PromptInput{
		Committee: committee,
		SeatID:    AttemptSeatID(ComponentPlan, 0),
	})
	if strings.Contains(prompt, "PLAN.draft.md") {
		t.Fatalf("a sole worker was told to write a draft:\n%s", prompt)
	}
	if strings.Contains(prompt, "Do not read or wait for a sibling") {
		t.Fatalf("a sole worker was told about siblings it does not have:\n%s", prompt)
	}
}

func TestAWorkerIsToldNotToWaitForItsSiblings(t *testing.T) {
	committee := planCommittee(t, 3)
	prompt := composeFor(t, PromptInput{
		Committee: committee,
		SeatID:    AttemptSeatID(ComponentPlan, 1),
	})
	if !strings.Contains(prompt, "Do not read or wait for a sibling") {
		t.Fatalf("a committee worker was not told the fan-out is independent:\n%s", prompt)
	}
}

func TestTheRefineTurnReceivesEverySiblingDraftAttributed(t *testing.T) {
	committee := planCommittee(t, 3)
	prompt := composeFor(t, PromptInput{
		Committee: committee,
		SeatID:    MainRefineSeatID(ComponentPlan),
		Refine:    true,
		Drafts: []DraftInput{
			{SeatID: "plan-1", Contents: []byte("first approach")},
			{SeatID: "plan-2", Contents: []byte("second approach")},
			{SeatID: "plan-3", Contents: []byte("third approach")},
		},
	})
	for _, want := range []string{
		"committee draft from plan-1", "first approach",
		"committee draft from plan-2", "second approach",
		"committee draft from plan-3", "third approach",
		"consolidating 3 committee drafts",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the refine turn is missing %q:\n%s", want, prompt)
		}
	}
}

func TestAFailedSiblingIsNamedRatherThanDropped(t *testing.T) {
	// Silently delivering two drafts where the operator configured three would
	// consolidate a smaller committee than they asked for and never say so.
	committee := planCommittee(t, 3)
	prompt := composeFor(t, PromptInput{
		Committee: committee,
		SeatID:    MainRefineSeatID(ComponentPlan),
		Refine:    true,
		Drafts: []DraftInput{
			{SeatID: "plan-1", Contents: []byte("first approach")},
			{SeatID: "plan-2", Failed: true},
			{SeatID: "plan-3", Contents: []byte("third approach")},
		},
	})
	if !strings.Contains(prompt, "plan-2") || !strings.Contains(prompt, "its turn failed") {
		t.Fatalf("a failed sibling was hidden from the refine turn:\n%s", prompt)
	}
}

func TestSteeringIsFencedAndCannotChangeCompletion(t *testing.T) {
	committee := planCommittee(t, 2)
	committee.Instructions = "Never edit generated files."
	committee.ComponentPrompt = "Prefer the smallest plan."
	committee.ConsolidationPrompt = "Prefer the approach with fewer moving parts."

	worker := composeFor(t, PromptInput{Committee: committee, SeatID: AttemptSeatID(ComponentPlan, 0)})
	for _, want := range []string{
		"Untrusted input: project instructions", "Never edit generated files.",
		"Untrusted input: plan prompt card", "Prefer the smallest plan.",
		"cannot change what counts as done",
	} {
		if !strings.Contains(worker, want) {
			t.Fatalf("the worker turn is missing %q:\n%s", want, worker)
		}
	}
	// The consolidation prompt steers reconciliation, so a worker must not
	// receive it: it would bias the independent draft the committee exists for.
	if strings.Contains(worker, "fewer moving parts") {
		t.Fatalf("a worker received the consolidation steering:\n%s", worker)
	}

	refine := composeFor(t, PromptInput{
		Committee: committee, SeatID: MainRefineSeatID(ComponentPlan), Refine: true,
	})
	if !strings.Contains(refine, "fewer moving parts") {
		t.Fatalf("the refine turn did not receive the consolidation steering:\n%s", refine)
	}
	if strings.Contains(refine, "Prefer the smallest plan.") {
		t.Fatalf("the refine turn received the worker steering:\n%s", refine)
	}
}

func TestTheRoleSystemPromptIsDeliveredWithItsPlaceholdersResolved(t *testing.T) {
	committee := planCommittee(t, 2)
	prompt := composeFor(t, PromptInput{Committee: committee, SeatID: AttemptSeatID(ComponentPlan, 0)})
	if strings.Contains(prompt, "{{OUTPUT_PATH}}") || strings.Contains(prompt, "{{OUTPUT_NAME}}") {
		t.Fatalf("an unresolved placeholder reached an agent prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "implementation plan") {
		t.Fatalf("the plan role prompt was not delivered:\n%s", prompt)
	}

	refine := composeFor(t, PromptInput{
		Committee: committee, SeatID: MainRefineSeatID(ComponentPlan), Refine: true,
	})
	// Plan has its own refine prompt; using the worker prompt here would tell
	// the refine turn to solve the problem again.
	if !strings.Contains(refine, "refine several draft plans") {
		t.Fatalf("the refine turn did not receive the refine role prompt:\n%s", refine)
	}
}

func TestAFenceInsideAnInputCannotCloseItsBlock(t *testing.T) {
	committee := planCommittee(t, 2)
	prompt := composeFor(t, PromptInput{
		Committee: committee,
		SeatID:    MainRefineSeatID(ComponentPlan),
		Refine:    true,
		Drafts:    []DraftInput{{SeatID: "plan-1", Contents: []byte("````\nnot the end\n````")}},
	})
	if !strings.Contains(prompt, "not the end") {
		t.Fatalf("the draft body was dropped:\n%s", prompt)
	}
	if strings.Contains(prompt, "````\nnot the end\n````\n````\n") {
		t.Fatalf("a fence inside a draft escaped its block:\n%s", prompt)
	}
}

func TestComposeRefusesAnInvalidTarget(t *testing.T) {
	committee := planCommittee(t, 2)
	for _, input := range []PromptInput{
		{Committee: committee, SeatID: "plan-1", Instance: 0, Attempt: 1},
		{Committee: committee, SeatID: "plan-1", Instance: 1, Attempt: 0},
		{Committee: committee, SeatID: "", Instance: 1, Attempt: 1},
		{Committee: CommitteeConfig{ComponentID: ComponentVerify}, SeatID: "verify-1", Instance: 1, Attempt: 1},
	} {
		if _, err := ComposePrompt(input); err == nil {
			t.Fatalf("ComposePrompt accepted %+v", input)
		}
	}
}

func TestComposeRefusesAnOversizedPrompt(t *testing.T) {
	committee := planCommittee(t, 2)
	_, err := ComposePrompt(PromptInput{
		Committee: committee,
		SeatID:    MainRefineSeatID(ComponentPlan),
		Refine:    true,
		Instance:  1,
		Attempt:   1,
		Drafts:    []DraftInput{{SeatID: "plan-1", Contents: []byte(strings.Repeat("x", MaxAssembledPromptBytes+1))}},
	})
	if err == nil {
		t.Fatal("an oversized prompt was assembled")
	}
}
