package dagama

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/terminal"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/verification"
)

func (c *Controller) runPipeline(ctx context.Context, board *Board, state *RunState, source CapturedSource, buildInstance int) (*RunState, error) {
	if state.Components[ComponentPlan].Status != ComponentSucceededStatus {
		var err error
		state, err = c.runSeat(ctx, board, state, source, ComponentPlan, 1, 1, nil)
		if err != nil {
			return state, err
		}
		if isTerminal(state.Status) || !componentSucceeded(state, ComponentPlan, 1) {
			return state, nil
		}
	}
	for {
		var err error
		if !componentSucceeded(state, ComponentBuild, buildInstance) {
			state, err = c.runSeat(ctx, board, state, source, ComponentBuild, buildInstance, buildInstance, resumeIdentity(board, state, ComponentBuild))
			if err != nil {
				return state, err
			}
			if isTerminal(state.Status) || !componentSucceeded(state, ComponentBuild, buildInstance) {
				return state, nil
			}
		}
		if !componentSucceeded(state, ComponentVerify, buildInstance) {
			state, err = c.runVerification(ctx, board, state, buildInstance)
			if err != nil {
				return state, err
			}
			if isTerminal(state.Status) || !componentSucceeded(state, ComponentVerify, buildInstance) {
				return state, nil
			}
		}
		verificationDocument, err := c.latestVerification(ctx, state)
		if err != nil {
			return c.finishFailure(ctx, state, ComponentVerify, buildInstance, "invalid_output", err)
		}
		if verificationDocument.Verdict == verification.VerdictFailed {
			if buildInstance <= MaxRepairRounds {
				buildInstance++
				continue
			}
			return c.openRepairGate(ctx, state, ComponentVerify, buildInstance, "verification failed after the bounded repair rounds")
		}
		if !componentSucceeded(state, ComponentReview, buildInstance) {
			state, err = c.runSeat(ctx, board, state, source, ComponentReview, buildInstance, buildInstance, nil)
			if err != nil {
				return state, err
			}
			if isTerminal(state.Status) || !componentSucceeded(state, ComponentReview, buildInstance) {
				return state, nil
			}
		}
		review, err := c.latestReview(ctx, state)
		if err != nil {
			return c.finishFailure(ctx, state, ComponentReview, buildInstance, "invalid_output", err)
		}
		if review.Effective != ReviewApproved {
			if buildInstance <= MaxRepairRounds {
				buildInstance++
				continue
			}
			return c.openRepairGate(ctx, state, ComponentReview, buildInstance, "review requested changes after the bounded repair rounds")
		}
		return c.openPublishGate(ctx, state)
	}
}

func componentSucceeded(state *RunState, component ComponentID, instance int) bool {
	current := state.Components[component]
	return current != nil && current.Instance == instance && current.Status == ComponentSucceededStatus
}

func (c *Controller) continueAfterAgent(ctx context.Context, board *Board, state *RunState, source CapturedSource, component ComponentID, instance int) (*RunState, error) {
	buildInstance := instance
	if component == ComponentPlan {
		buildInstance = 1
	}
	return c.runPipeline(ctx, board, state, source, buildInstance)
}

func (c *Controller) runSeat(ctx context.Context, board *Board, state *RunState, source CapturedSource, component ComponentID, instance, attempt int, resume *contracts.SessionIdentity) (*RunState, error) {
	seat := seatFor(board, component)
	artifacts, err := c.promptArtifacts(ctx, state)
	if err != nil {
		return c.finishFailure(ctx, state, component, instance, "invalid_output", err)
	}
	prompt, err := ComposePrompt(PromptInput{Component: component, Instance: instance, Attempt: attempt, Source: source, Artifacts: artifacts, Repair: instance > 1})
	if err != nil {
		return c.finishFailure(ctx, state, component, instance, "invalid_output", err)
	}
	state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &ComponentReady{ComponentInstance{ComponentID: component, Instance: instance}})
	if err != nil {
		return state, err
	}
	state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &ComponentStarted{ComponentInstance{ComponentID: component, Instance: instance}})
	if err != nil {
		return state, err
	}
	attemptID := fmt.Sprintf("%s-%s-%d-%d", state.RunID, component, instance, attempt)
	seatID := string(component) + "-1"
	tmuxName, err := terminal.Name("dagama", attemptID)
	if err != nil {
		return c.finishFailure(ctx, state, component, instance, "launch_failed", err)
	}
	ref := AttemptRef{ComponentInstance: ComponentInstance{ComponentID: component, Instance: instance}, SeatID: seatID, Attempt: attempt, AttemptID: attemptID}
	state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &AttemptLaunchRequested{AttemptRef: ref, TmuxName: tmuxName, Ownership: OwnershipAutomated})
	if err != nil {
		return state, err
	}
	request := AttemptRequest{ProjectID: state.ProjectID, RunID: state.RunID, RunRoot: state.RunRoot, BaseSha: state.BaseSha, Component: component, Instance: instance, Attempt: attempt, AttemptID: attemptID, SeatID: seatID, Seat: seat, Prompt: prompt, Resume: resume}
	result, executeErr := c.runtime.Execute(ctx, request, func(session contracts.SessionIdentity) error {
		if session.ID != "" && session.Agent != string(seat.Vendor) {
			return fmt.Errorf("the attempt returned a cross-vendor session identity")
		}
		var appendErr error
		state, appendErr = c.runs.Append(ctx, state.ProjectID, state.RunID, &AttemptLaunched{AttemptRef: ref, TmuxName: tmuxName, SessionID: session.ID, Ownership: OwnershipAutomated})
		if appendErr != nil {
			return appendErr
		}
		if session.ID != "" {
			state, appendErr = c.runs.Append(ctx, state.ProjectID, state.RunID, &AttemptSessionBound{AttemptRef: ref, SessionID: session.ID})
		}
		return appendErr
	})
	unlock := c.lock(state.ProjectID, state.RunID)
	defer unlock()
	latest, readErr := c.runs.Read(ctx, state.ProjectID, state.RunID)
	if readErr != nil {
		return state, readErr
	}
	state = latest
	current := state.Components[component]
	if isTerminal(state.Status) || current == nil || current.Attempt == nil || current.Attempt.AttemptID != attemptID {
		return state, nil
	}
	if executeErr != nil {
		return c.finishFailure(ctx, state, component, instance, classifyError(executeErr), executeErr)
	}
	if result.Session.ID != "" && result.Session.Agent != string(seat.Vendor) {
		return c.finishFailure(ctx, state, component, instance, "invalid_output", fmt.Errorf("the attempt returned a cross-vendor session identity"))
	}
	finishedAt := result.FinishedAt
	if finishedAt.IsZero() {
		finishedAt = c.now().UTC()
	}
	state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &AttemptExited{AttemptRef: ref, ExitCode: result.ExitCode, FinishedAt: finishedAt})
	if err != nil {
		return state, err
	}
	if releaseErr := c.runtime.Release(ctx, *state.Components[component].Attempt); releaseErr != nil {
		return c.finishFailure(ctx, state, component, instance, "cleanup_failed", releaseErr)
	}
	return c.finalizeSeatResult(ctx, state, component, instance, attempt, result)
}

func (c *Controller) finalizeSeatResult(ctx context.Context, state *RunState, component ComponentID, instance, attempt int, result AttemptResult) (*RunState, error) {
	var err error
	if result.ExitCode != 0 {
		return c.finishFailure(ctx, state, component, instance, "nonzero_exit", fmt.Errorf("the provider exited with status %d", result.ExitCode))
	}
	if result.ReviewerMutated {
		return c.finishFailure(ctx, state, component, instance, "reviewer_mutated_worktree", fmt.Errorf("the reviewer modified project files"))
	}
	expectedSeat := string(component) + "-1"
	for _, artifact := range result.Artifacts {
		producer := artifact.Producer
		if producer.ComponentID != component || producer.Instance != instance || producer.SeatID != expectedSeat || producer.Attempt != attempt {
			return c.finishFailure(ctx, state, component, instance, "invalid_output", fmt.Errorf("an output artifact has mismatched provenance"))
		}
	}
	if component == ComponentBuild {
		if result.Change == nil || result.Change.PatchBytes == 0 || len(result.Change.ChangedFiles) == 0 {
			return c.finishFailure(ctx, state, component, instance, "no_change_captured", fmt.Errorf("the build produced no captured change"))
		}
		change := changeRecord(*result.Change, state.BaseSha)
		state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &ChangeCaptured{
			ChangeRevision: change.ChangeRevision, TreeOID: change.TreeOID, PatchSha256: change.PatchSha256,
			PatchBytes: change.PatchBytes, Insertions: change.Insertions, Deletions: change.Deletions,
			ChangedFiles: change.ChangedFiles, BaseSha: change.BaseSha,
		})
		if err != nil {
			return state, err
		}
	}
	if component == ComponentReview {
		if state.Change == nil {
			return c.finishFailure(ctx, state, component, instance, "invalid_output", fmt.Errorf("review requires a captured revision"))
		}
		var reviewArtifact *ArtifactRecord
		for index := range result.Artifacts {
			if result.Artifacts[index].Name == "review.json" {
				reviewArtifact = &result.Artifacts[index]
				break
			}
		}
		if reviewArtifact == nil {
			return c.finishFailure(ctx, state, component, instance, "missing_output", fmt.Errorf("review.json is missing"))
		}
		contents, readErr := c.runtime.ReadArtifact(ctx, state.RunRoot, *reviewArtifact)
		if readErr != nil {
			return c.finishFailure(ctx, state, component, instance, "invalid_output", readErr)
		}
		outcome, normalizeErr := NormalizeReviewOutcome(contents, *state.Change, expectedSeat, attempt)
		if normalizeErr != nil {
			return c.finishFailure(ctx, state, component, instance, "invalid_output", normalizeErr)
		}
		encoded, encodeErr := json.MarshalIndent(outcome, "", "  ")
		if encodeErr != nil {
			return c.finishFailure(ctx, state, component, instance, "invalid_output", encodeErr)
		}
		encoded = append(encoded, '\n')
		canonical, promoteErr := c.runtime.RecordControllerArtifact(ctx, state.RunRoot, "review.json", encoded, ProducerRef{Component: ComponentReview, Instance: instance, SeatID: expectedSeat, Attempt: attempt})
		if promoteErr != nil {
			return c.finishFailure(ctx, state, component, instance, "invalid_output", promoteErr)
		}
		*reviewArtifact = canonical
	}
	outputs := make([]string, 0, len(result.Artifacts))
	names := make(map[string]bool, len(result.Artifacts))
	for _, artifact := range result.Artifacts {
		state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &ArtifactPromoted{Artifact: artifact})
		if err != nil {
			return state, err
		}
		outputs = append(outputs, artifact.ArtifactID)
		names[artifact.Name] = true
	}
	for _, required := range requiredOutputs(component) {
		if !names[required] {
			return c.finishFailure(ctx, state, component, instance, "missing_output", fmt.Errorf("required artifact %s is missing", required))
		}
	}
	return c.runs.Append(ctx, state.ProjectID, state.RunID, &ComponentSucceeded{ComponentInstance: ComponentInstance{ComponentID: component, Instance: instance}, Outputs: outputs})
}

func seatFor(board *Board, component ComponentID) Seat {
	switch component {
	case ComponentPlan:
		return board.Components.Plan.Seat
	case ComponentBuild:
		return board.Components.Build.Seat
	default:
		return board.Components.Review.Seat
	}
}

func resumeIdentity(board *Board, state *RunState, component ComponentID) *contracts.SessionIdentity {
	current := state.Components[component]
	if current == nil || current.Attempt == nil || current.Attempt.SessionID == "" {
		return nil
	}
	return &contracts.SessionIdentity{Agent: string(seatFor(board, component).Vendor), ID: current.Attempt.SessionID}
}

func (c *Controller) runVerification(ctx context.Context, board *Board, state *RunState, instance int) (*RunState, error) {
	if state.Change == nil {
		return c.finishFailure(ctx, state, ComponentVerify, instance, "no_change_captured", fmt.Errorf("verification requires a captured change"))
	}
	var err error
	state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &ComponentReady{ComponentInstance{ComponentID: ComponentVerify, Instance: instance}})
	if err != nil {
		return state, err
	}
	state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &ComponentStarted{ComponentInstance{ComponentID: ComponentVerify, Instance: instance}})
	if err != nil {
		return state, err
	}
	document, artifact, verifyErr := c.runtime.Verify(ctx, VerifyRequest{ProjectID: state.ProjectID, RunID: state.RunID, RunRoot: state.RunRoot, Instance: instance, Change: *state.Change, Checks: board.Components.Verify.Checks})
	if verifyErr != nil {
		return c.finishFailure(ctx, state, ComponentVerify, instance, classifyError(verifyErr), verifyErr)
	}
	if document.ChangeRevision != state.Change.ChangeRevision || document.PatchSha256 != state.Change.PatchSha256 {
		return c.finishFailure(ctx, state, ComponentVerify, instance, "revision_stale", fmt.Errorf("verification does not match the current revision"))
	}
	state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &ArtifactPromoted{Artifact: artifact})
	if err != nil {
		return state, err
	}
	return c.runs.Append(ctx, state.ProjectID, state.RunID, &ComponentSucceeded{ComponentInstance: ComponentInstance{ComponentID: ComponentVerify, Instance: instance}, Outputs: []string{artifact.ArtifactID}})
}

func (c *Controller) openPublishGate(ctx context.Context, state *RunState) (*RunState, error) {
	if state.Change == nil {
		return c.finishFailure(ctx, state, ComponentPublish, 1, "revision_stale", fmt.Errorf("publish requires a current revision"))
	}
	state, err := c.runs.Append(ctx, state.ProjectID, state.RunID, &ComponentReady{ComponentInstance{ComponentID: ComponentPublish, Instance: 1}})
	if err != nil {
		return state, err
	}
	revision := state.Change.ChangeRevision
	return c.runs.Append(ctx, state.ProjectID, state.RunID, &GateOpened{ComponentInstance: ComponentInstance{ComponentID: ComponentPublish, Instance: 1}, Reason: "blocked_by_gate", Message: "publication requires explicit approval", ChangeRevision: &revision})
}

func (c *Controller) promptArtifacts(ctx context.Context, state *RunState) (map[string][]byte, error) {
	result := map[string][]byte{}
	for _, artifact := range state.Artifacts {
		if _, wanted := map[string]bool{"PROBLEM.md": true, "PLAN.md": true, "CHANGESET.patch": true, "verification.json": true, "review.json": true}[artifact.Name]; !wanted {
			continue
		}
		contents, err := c.runtime.ReadArtifact(ctx, state.RunRoot, artifact)
		if err != nil {
			return nil, err
		}
		result[artifact.Name] = contents
	}
	return result, nil
}

func (c *Controller) latestVerification(ctx context.Context, state *RunState) (verification.Document, error) {
	artifact, ok := latestArtifact(state, "verification.json")
	if !ok {
		return verification.Document{}, fmt.Errorf("verification.json is missing")
	}
	contents, err := c.runtime.ReadArtifact(ctx, state.RunRoot, artifact)
	if err != nil {
		return verification.Document{}, err
	}
	var document verification.Document
	if err := json.Unmarshal(contents, &document); err != nil {
		return verification.Document{}, err
	}
	return document, nil
}

func (c *Controller) latestReview(ctx context.Context, state *RunState) (ReviewOutcome, error) {
	artifact, ok := latestArtifact(state, "review.json")
	if !ok {
		return ReviewOutcome{}, fmt.Errorf("review.json is missing")
	}
	contents, err := c.runtime.ReadArtifact(ctx, state.RunRoot, artifact)
	if err != nil {
		return ReviewOutcome{}, err
	}
	outcome, err := DecodeReviewOutcome(contents)
	if err != nil {
		return ReviewOutcome{}, err
	}
	component := state.Components[ComponentReview]
	if state.Change == nil || component == nil || component.Attempt == nil || ValidateReviewOutcome(outcome, *state.Change, "review-1", component.Attempt.Attempt) != nil {
		return ReviewOutcome{}, fmt.Errorf("review outcome is stale")
	}
	return outcome, nil
}

func latestArtifact(state *RunState, name string) (ArtifactRecord, bool) {
	for index := len(state.Artifacts) - 1; index >= 0; index-- {
		if state.Artifacts[index].Name == name {
			return state.Artifacts[index], true
		}
	}
	return ArtifactRecord{}, false
}
