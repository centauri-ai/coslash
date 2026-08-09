package atlas

import (
	"fmt"
	"slices"
	"strings"
)

// The gate on a board's executable content.
//
// A board is a JSON file in a project directory. It can be hand-edited,
// committed, shared, or arrive in a pull request — so it is untrusted input,
// and the editor's dropdowns constrain the UI rather than the file.
//
// This gate REJECTS rather than repairs. Normalize repairs; the server refuses.
// The split matters: a repaired board is silently different from the one the
// user is looking at, which is acceptable while editing and unacceptable at the
// moment something is about to be executed.

// AssertPolicy refuses a board whose executable content is not allowed. It
// returns the first violation with the field path that produced it.
func AssertPolicy(board *Board) error {
	if board == nil {
		return policyError("board", "the board is missing")
	}
	if board.Kind != "" && board.Kind != BoardKind {
		return policyError("kind", "the document is not an Atlas board")
	}
	if board.SchemaVersion != BoardSchemaVersion {
		return &Error{
			Code:    CodeSchemaVersion,
			Message: fmt.Sprintf("this build supports Atlas board schema %d", BoardSchemaVersion),
			Field:   "schemaVersion",
		}
	}
	if len(board.Components) == 0 {
		return policyError("components", "at least one agent seat is required")
	}
	if len(board.Components) > MaxComponents {
		return policyError("components", fmt.Sprintf("at most %d seats are allowed", MaxComponents))
	}

	ids := make(map[string]struct{}, len(board.Components))
	legacyRoles := make(map[ComponentID]struct{}, len(SeatComponentIDs))
	for index := range board.Components {
		if err := assertComponent(index, &board.Components[index], ids); err != nil {
			return err
		}
		role := board.Components[index].LegacyRole
		if role != nil {
			if _, duplicate := legacyRoles[*role]; duplicate {
				return policyError(fmt.Sprintf("components[%d].legacyRole", index), "the run role is assigned to more than one seat")
			}
			legacyRoles[*role] = struct{}{}
		}
	}
	if err := assertEdges(board.Edges, ids); err != nil {
		return err
	}
	if err := assertNoTriggerCycle(board); err != nil {
		return err
	}
	return assertRunPolicy(board.RunPolicy)
}

func assertComponent(index int, component *AgentComponent, ids map[string]struct{}) error {
	field := fmt.Sprintf("components[%d]", index)
	if !ValidGraphID(component.ID) {
		return policyError(field+".id", "the seat identifier is not valid")
	}
	if strings.HasSuffix(component.ID, "-prompt") || strings.HasSuffix(component.ID, "-info") {
		return policyError(field+".id", "the seat identifier collides with a companion card")
	}
	if _, duplicate := ids[component.ID]; duplicate {
		return policyError(field+".id", "the seat identifier is used twice")
	}
	ids[component.ID] = struct{}{}

	if component.LegacyRole != nil && !ValidLegacyRole(*component.LegacyRole) {
		return policyError(field+".legacyRole", "the seat claims a run role that does not exist")
	}
	if len(component.Title) > MaxTitleLength {
		return policyError(field+".title", fmt.Sprintf("a seat title may hold at most %d characters", MaxTitleLength))
	}
	if len(component.Prompt) > MaxPromptLength {
		return policyError(field+".prompt", fmt.Sprintf("a seat prompt may hold at most %d characters", MaxPromptLength))
	}
	if err := assertSeat(field+".seat", component.Seat); err != nil {
		return err
	}

	if len(component.Seats) < 1 || len(component.Seats) > MaxCommitteeWorkers {
		return policyError(field+".seats", fmt.Sprintf("a committee holds between 1 and %d workers", MaxCommitteeWorkers))
	}
	seatIDs := make(map[string]struct{}, len(component.Seats))
	mains := 0
	for seatIndex, worker := range component.Seats {
		seatField := fmt.Sprintf("%s.seats[%d]", field, seatIndex)
		if !ValidGraphID(worker.ID) {
			return policyError(seatField+".id", "the worker identifier is not valid")
		}
		if _, duplicate := seatIDs[worker.ID]; duplicate {
			return policyError(seatField+".id", "the worker identifier is used twice")
		}
		seatIDs[worker.ID] = struct{}{}
		if worker.Role != RoleMain && worker.Role != RoleWorker {
			return policyError(seatField+".role", "a worker is either main or worker")
		}
		if worker.Role == RoleMain {
			mains++
		}
		if err := assertSeat(seatField, worker.Profile()); err != nil {
			return err
		}
	}
	// The main worker writes the promoted artifact, so exactly one committee
	// member must own it — and a sole worker owns it without the role.
	switch {
	case len(component.Seats) == 1 && mains != 0:
		return policyError(field+".seats", "a sole worker must not be marked main")
	case len(component.Seats) > 1 && mains != 1:
		return policyError(field+".seats", "a committee needs exactly one main worker")
	}

	if len(component.Committee.ConsolidationPrompt) > MaxPromptLength {
		return policyError(field+".committee.consolidationPrompt",
			fmt.Sprintf("a consolidation prompt may hold at most %d characters", MaxPromptLength))
	}

	if len(component.RequiredOutputs) == 0 {
		return policyError(field+".requiredOutputs", "at least one required output is needed")
	}
	if len(component.RequiredOutputs) > MaxRequiredOutputs {
		return policyError(field+".requiredOutputs",
			fmt.Sprintf("at most %d required outputs are allowed", MaxRequiredOutputs))
	}
	outputs := make(map[string]struct{}, len(component.RequiredOutputs))
	for outputIndex, output := range component.RequiredOutputs {
		outputField := fmt.Sprintf("%s.requiredOutputs[%d]", field, outputIndex)
		if !ValidRequiredOutput(output) {
			return policyError(outputField, "the required output is not a safe file name")
		}
		if _, duplicate := outputs[output]; duplicate {
			return policyError(outputField, "the required output is listed twice")
		}
		outputs[output] = struct{}{}
	}
	return nil
}

func assertSeat(field string, seat Seat) error {
	if !ValidVendor(seat.Vendor) {
		return policyError(field+".vendor", "the seat names a vendor that is not supported")
	}
	if !slices.Contains(ModelsFor(seat.Vendor), seat.Model) {
		return policyError(field+".model", "the model is not allowed for this vendor")
	}
	if !slices.Contains(EffortsFor(seat.Vendor, seat.Model), seat.Effort) {
		return policyError(field+".effort", "the effort is not allowed for this model")
	}
	if !slices.Contains(PermissionsFor(seat.Vendor), seat.Permission) {
		return policyError(field+".permission", "the permission is not allowed for this vendor")
	}
	return nil
}

func assertEdges(edges []Edge, componentIDs map[string]struct{}) error {
	pairs := make(map[string]struct{}, len(edges))
	ids := make(map[string]struct{}, len(edges))
	for index, edge := range edges {
		field := fmt.Sprintf("edges[%d]", index)
		if !ValidGraphID(edge.ID) {
			return policyError(field+".id", "the edge identifier is not valid")
		}
		if _, duplicate := ids[edge.ID]; duplicate {
			return policyError(field+".id", "the edge identifier is used twice")
		}
		ids[edge.ID] = struct{}{}
		if _, ok := componentIDs[edge.From]; !ok {
			return policyError(field+".from", "the edge starts at a seat that does not exist")
		}
		if _, ok := componentIDs[edge.To]; !ok {
			return policyError(field+".to", "the edge ends at a seat that does not exist")
		}
		if edge.From == edge.To {
			return policyError(field, "an edge may not connect a seat to itself")
		}
		if edge.Kind != EdgeTrigger && edge.Kind != EdgeFeedback {
			return policyError(field+".kind", "the edge kind is not supported")
		}
		if edge.Mode != TriggerAuto && edge.Mode != TriggerManual {
			return policyError(field+".mode", "the edge mode is either auto or manual")
		}
		switch edge.Kind {
		case EdgeFeedback:
			if edge.MaxRounds < DefaultFeedbackMaxRounds || edge.MaxRounds > MaxFeedbackRounds {
				return policyError(field+".maxRounds",
					fmt.Sprintf("feedback rounds must be between %d and %d", DefaultFeedbackMaxRounds, MaxFeedbackRounds))
			}
		case EdgeTrigger:
			if edge.MaxRounds != 0 {
				return policyError(field+".maxRounds", "only a feedback edge caps repair rounds")
			}
		}
		pair := edge.From + "->" + edge.To
		if _, duplicate := pairs[pair]; duplicate {
			return policyError(field, "the same pair of seats is connected twice")
		}
		pairs[pair] = struct{}{}
	}
	return nil
}

// assertNoTriggerCycle refuses a cycle among trigger edges.
//
// Feedback edges are reverse by construction — Review → Build is the whole
// point — so the graph as a whole is expected to contain cycles. The forward
// subgraph must not: a trigger cycle is an auto-advance loop with no terminal
// seat, which would run agents until a human noticed.
func assertNoTriggerCycle(board *Board) error {
	successors := make(map[string][]string, len(board.Components))
	for _, edge := range board.Edges {
		if edge.Kind != EdgeTrigger {
			continue
		}
		successors[edge.From] = append(successors[edge.From], edge.To)
	}

	const (
		unvisited = 0
		onStack   = 1
		done      = 2
	)
	state := make(map[string]int, len(board.Components))
	var walk func(string) bool
	walk = func(node string) bool {
		state[node] = onStack
		for _, next := range successors[node] {
			switch state[next] {
			case onStack:
				return true
			case unvisited:
				if walk(next) {
					return true
				}
			}
		}
		state[node] = done
		return false
	}
	for _, component := range board.Components {
		if state[component.ID] != unvisited {
			continue
		}
		if walk(component.ID) {
			return policyError("edges", "trigger edges form a cycle, so the run would never finish")
		}
	}
	return nil
}

func assertRunPolicy(policy *RunPolicy) error {
	if policy == nil {
		return nil
	}
	if len(policy.Checks) > MaxChecks {
		return policyError("runPolicy.checks", fmt.Sprintf("at most %d checks are allowed", MaxChecks))
	}
	names := make(map[string]struct{}, len(policy.Checks))
	for index, check := range policy.Checks {
		field := fmt.Sprintf("runPolicy.checks[%d]", index)
		if !ValidCheckName(check.Name) {
			return policyError(field+".name", "the check name is not valid")
		}
		if _, duplicate := names[check.Name]; duplicate {
			return policyError(field+".name", "the check name is used twice")
		}
		names[check.Name] = struct{}{}
		if len(check.Argv) == 0 {
			return policyError(field+".argv", "a check needs a command")
		}
		if len(check.Argv) > MaxArgvTokens {
			return policyError(field+".argv", fmt.Sprintf("a check may hold at most %d argv tokens", MaxArgvTokens))
		}
		if !ValidCheckCommand(check.Argv[0]) {
			return policyError(field+".argv[0]", "the program is not an allowed check command")
		}
		for tokenIndex, token := range check.Argv {
			if !ValidArgvToken(token) {
				return policyError(fmt.Sprintf("%s.argv[%d]", field, tokenIndex),
					"the argv token is empty, oversized, or contains a control character")
			}
		}
	}
	if policy.Publish.Base != "" && !ValidBaseBranch(policy.Publish.Base) {
		return policyError("runPolicy.publish.base", "the publish base branch is not valid")
	}
	return nil
}

// AssertRunnable refuses a board that cannot start a run.
//
// Only the classic plan → build → review chain is executable. Freeform graphs
// may be saved and edited; starting a run on one is refused here rather than
// discovered halfway through by a controller with nowhere to advance.
func AssertRunnable(board *Board) error {
	if err := AssertPolicy(board); err != nil {
		return err
	}
	plan := board.ComponentByLegacyRole(ComponentPlan)
	build := board.ComponentByLegacyRole(ComponentBuild)
	review := board.ComponentByLegacyRole(ComponentReview)
	if plan == nil || build == nil || review == nil {
		return policyError("components",
			"Run requires plan, build, and review seats. Custom graph runtime is not available yet.")
	}
	if !board.hasTriggerEdge(plan.ID, build.ID) || !board.hasTriggerEdge(build.ID, review.ID) {
		return policyError("edges",
			"Run requires trigger edges plan → build → review. Custom graph runtime is not available yet.")
	}
	return nil
}
