package atlas

import (
	"fmt"
	"strings"
)

// Committee resolution: what a board says one run stage should launch.
//
// The controller reads a run's board SNAPSHOT, not the live board, so this
// layer takes a decoded board and answers questions about it without touching
// the filesystem. Everything here is pure and total: a board that has already
// passed Normalize always yields a usable committee.

// CommitteeConfig is one stage's resolved launch plan.
type CommitteeConfig struct {
	// ComponentID is the run stage this configuration drives.
	ComponentID ComponentID
	// SeatID is the board seat that carries it, which may differ from the run
	// stage id on a renamed graph.
	SeatID string
	// Workers are the committee members in board order.
	Workers []WorkerSeat
	// MainIndex is the worker that writes the promoted artifact: the designated
	// main when N > 1, and the sole worker otherwise.
	MainIndex int
	// ConsolidationPrompt steers the main worker's refine turn.
	ConsolidationPrompt string
	// ComponentPrompt is the seat's own steering.
	ComponentPrompt string
	// Instructions is the board's shared context, delivered to every seat.
	Instructions string
	// SystemPrompts are the board's role prompts.
	SystemPrompts SystemPrompts
	// RequiredOutputs are the artifacts the stage must produce.
	RequiredOutputs []string
}

// Main returns the worker that writes the promoted artifact.
func (c CommitteeConfig) Main() WorkerSeat {
	if len(c.Workers) == 0 {
		return WorkerSeat{}
	}
	if c.MainIndex < 0 || c.MainIndex >= len(c.Workers) {
		return c.Workers[0]
	}
	return c.Workers[c.MainIndex]
}

// SkipMainRefine reports whether the stage runs without a refine turn.
//
// One worker writes the promoted artifact directly. Refining a single draft
// against no siblings would spend a whole extra agent turn to reproduce what
// the worker already wrote.
func (c CommitteeConfig) SkipMainRefine() bool { return len(c.Workers) <= 1 }

// CommitteeFor resolves one run stage from a board.
//
// A board without a seat for the stage is an error rather than a default: the
// controller would otherwise launch an agent with a model nobody chose.
func CommitteeFor(board *Board, componentID ComponentID) (CommitteeConfig, error) {
	if board == nil {
		return CommitteeConfig{}, policyError("board", "the board is missing")
	}
	if !HasSeat(componentID) {
		return CommitteeConfig{}, policyError("componentId", "only plan, build, and review run a committee")
	}
	component := board.ComponentByLegacyRole(componentID)
	if component == nil {
		component = board.ComponentByID(string(componentID))
	}
	if component == nil {
		return CommitteeConfig{}, policyError(
			fmt.Sprintf("components.%s", componentID),
			fmt.Sprintf("the board has no %s seat", componentID),
		)
	}
	if len(component.Seats) == 0 {
		return CommitteeConfig{}, policyError(
			fmt.Sprintf("components.%s.seats", componentID),
			"the seat has no configured worker",
		)
	}

	workers := append([]WorkerSeat(nil), component.Seats...)
	mainIndex := 0
	for index, worker := range workers {
		if worker.Role == RoleMain {
			mainIndex = index
			break
		}
	}
	if len(workers) == 1 {
		mainIndex = 0
	}

	return CommitteeConfig{
		ComponentID:         componentID,
		SeatID:              component.ID,
		Workers:             workers,
		MainIndex:           mainIndex,
		ConsolidationPrompt: component.Committee.ConsolidationPrompt,
		ComponentPrompt:     component.Prompt,
		Instructions:        board.Instructions,
		SystemPrompts:       board.SystemPrompts,
		RequiredOutputs:     append([]string(nil), component.RequiredOutputs...),
	}, nil
}

// AttemptSeatID names the run-log seat a worker occupies.
//
// The identifier is derived from the component and the worker's position, not
// from the worker's own id, so a board edit that renames a worker cannot make
// an in-flight run's history unreadable.
func AttemptSeatID(componentID ComponentID, index int) string {
	return fmt.Sprintf("%s-%d", componentID, index+1)
}

// MainRefineSeatID names the main worker's refine turn in the run log.
func MainRefineSeatID(componentID ComponentID) string {
	return fmt.Sprintf("%s-main-refine", componentID)
}

// IsWorkerSeatID reports whether a run-log seat is a committee worker turn.
func IsWorkerSeatID(componentID ComponentID, seatID string) bool {
	suffix, found := strings.CutPrefix(seatID, string(componentID)+"-")
	if !found || suffix == "" {
		return false
	}
	for _, character := range suffix {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// IsMainRefineSeatID reports whether a run-log seat is the refine turn.
func IsMainRefineSeatID(componentID ComponentID, seatID string) bool {
	return seatID == MainRefineSeatID(componentID)
}

// DraftArtifactName is the sibling-attributed name a worker writes before the
// refine turn promotes one result.
//
// Every worker writing the promoted name directly would make the last writer
// win and destroy the attribution the committee exists to produce.
func DraftArtifactName(promoted string) string {
	if base, found := strings.CutSuffix(promoted, ".md"); found {
		return base + ".draft.md"
	}
	return promoted + ".draft"
}
