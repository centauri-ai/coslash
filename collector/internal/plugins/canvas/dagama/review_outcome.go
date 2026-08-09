package dagama

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/publication"
)

const ReviewSchemaVersion uint64 = 1

type ReviewVerdict string

const (
	ReviewApproved         ReviewVerdict = "approved"
	ReviewChangesRequested ReviewVerdict = "changes_requested"
)

type ReviewFinding struct {
	Severity string  `json:"severity"`
	File     *string `json:"file"`
	Line     *int    `json:"line"`
	Summary  string  `json:"summary"`
	Detail   string  `json:"detail"`
}

// GateDecisionOptions carries the operator intents an approval can express.
type GateDecisionOptions struct {
	// SkipPublication ends an approved publish gate without commit, push, or
	// pull request. It is a distinct intent, not a variant of approval: the
	// change is accepted, and the operator has chosen to carry it out
	// themselves. Publishing anyway would perform an outward-facing action they
	// explicitly declined.
	SkipPublication bool
}

// DecideGate records an operator gate decision and continues the run.
func (c *Controller) DecideGate(ctx context.Context, projectID, runID string, decision GateDecision, message string) (*RunState, error) {
	return c.DecideGateWithOptions(ctx, projectID, runID, decision, message, GateDecisionOptions{})
}

// DecideGateWithOptions is DecideGate with the approval intents spelled out.
func (c *Controller) DecideGateWithOptions(
	ctx context.Context,
	projectID, runID string,
	decision GateDecision,
	message string,
	options GateDecisionOptions,
) (*RunState, error) {
	unlock := c.lock(projectID, runID)
	defer unlock()
	state, err := c.runs.Read(ctx, projectID, runID)
	if err != nil {
		return nil, err
	}
	if err := CanDecideGate(state); err != nil {
		return nil, err
	}
	var revision *uint64
	if state.Change != nil {
		value := state.Change.ChangeRevision
		revision = &value
	}
	gateComponent, gateInstance := state.Gate.ComponentID, state.Gate.Instance
	if decision == GateRejected {
		reportState := *state
		gate := *state.Gate
		gate.Decision = decision
		gate.ChangeRevision = revision
		gate.Message = message
		decidedAt := c.now().UTC()
		gate.DecidedAt = &decidedAt
		reportState.Gate = &gate
		reportState.Failure = &RunFailure{Reason: "gate_rejected", Message: message}
		state, err = c.writeReport(ctx, &reportState, RunFailed)
		if err != nil {
			return state, err
		}
	}
	state, err = c.runs.Append(ctx, projectID, runID, &GateDecided{ComponentInstance: ComponentInstance{ComponentID: gateComponent, Instance: gateInstance}, Decision: decision, ChangeRevision: revision, Message: message})
	if err != nil || decision == GateRejected {
		return state, err
	}
	if gateComponent != ComponentPublish {
		board, loadErr := c.boardForRun(ctx, state)
		if loadErr != nil {
			return state, loadErr
		}
		source, sourceErr := c.sourceForRun(ctx, state)
		if sourceErr != nil {
			return state, sourceErr
		}
		return c.runPipeline(ctx, board, state, source, state.Components[ComponentBuild].Instance+1)
	}
	if options.SkipPublication {
		return c.finishWithoutPublication(ctx, state)
	}
	return c.publishLocked(ctx, state)
}

// PublishSkippedMessage is the recorded reason a succeeded run carries no
// publication record.
const PublishSkippedMessage = "the operator approved the change without publishing it"

// finishWithoutPublication completes an approved run that the operator chose
// not to publish. The change stays in the run root exactly as it was verified
// and reviewed; nothing is committed, pushed, or opened.
func (c *Controller) finishWithoutPublication(ctx context.Context, state *RunState) (*RunState, error) {
	state, err := c.runs.Append(ctx, state.ProjectID, state.RunID, &ComponentSucceeded{
		ComponentInstance: ComponentInstance{ComponentID: ComponentPublish, Instance: 1},
	})
	if err != nil {
		return state, err
	}
	state, err = c.writeReport(ctx, state, RunSucceeded)
	if err != nil {
		return state, err
	}
	return c.runs.Append(ctx, state.ProjectID, state.RunID, &RunFinished{
		Status: RunSucceeded, Reason: "published_skipped", Message: PublishSkippedMessage,
	})
}

func (c *Controller) publishLocked(ctx context.Context, state *RunState) (*RunState, error) {
	if state.Change == nil {
		return c.finishFailure(ctx, state, ComponentPublish, 1, "revision_stale", fmt.Errorf("the run has no current revision"))
	}
	board, err := c.boardForRun(ctx, state)
	if err != nil {
		return state, err
	}
	verify, err := c.latestVerification(ctx, state)
	if err != nil {
		return c.finishFailure(ctx, state, ComponentPublish, 1, "revision_stale", err)
	}
	review, err := c.latestReview(ctx, state)
	if err != nil {
		return c.finishFailure(ctx, state, ComponentPublish, 1, "revision_stale", err)
	}
	key := publication.IdempotencyKey(state.RunID, state.Change.ChangeRevision)
	state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &PublishAttempted{ChangeRevision: state.Change.ChangeRevision, IdempotencyKey: key, Branch: state.Branch})
	if err != nil {
		return state, err
	}
	record, artifact, publishErr := c.runtime.Publish(ctx, PublishRequest{State: state, Board: board, Review: review, Verification: verify, Title: state.Title, Body: "DaGama run " + state.RunID})
	if publishErr != nil {
		return c.finishFailure(ctx, state, ComponentPublish, 1, classifyError(publishErr), publishErr)
	}
	state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &ArtifactPromoted{Artifact: artifact})
	if err != nil {
		return state, err
	}
	state, err = c.runs.Append(ctx, state.ProjectID, state.RunID, &PublishCompleted{Publication: publicationRecord(record)})
	if err != nil {
		return state, err
	}
	state, err = c.writeReport(ctx, state, RunSucceeded)
	if err != nil {
		return state, err
	}
	return c.runs.Append(ctx, state.ProjectID, state.RunID, &RunFinished{Status: RunSucceeded, Message: "the workflow completed successfully"})
}

func (c *Controller) sourceForRun(ctx context.Context, state *RunState) (CapturedSource, error) {
	if state.Source == nil {
		return CapturedSource{}, newError(CodeInvalidState, "the run source is missing")
	}
	artifact, ok := latestArtifact(state, "SOURCE.md")
	if !ok {
		return CapturedSource{}, newError(CodeInvalidState, "the source artifact is missing")
	}
	body, err := c.runtime.ReadArtifact(ctx, state.RunRoot, artifact)
	if err != nil {
		return CapturedSource{}, err
	}
	return CapturedSource{Record: *state.Source, Body: body}, nil
}

func (c *Controller) boardForRun(ctx context.Context, state *RunState) (*Board, error) {
	artifact, ok := latestArtifact(state, "board.snapshot.json")
	if !ok {
		return nil, newError(CodeInvalidState, "the run board snapshot is missing")
	}
	contents, err := c.runtime.ReadArtifact(ctx, state.RunRoot, artifact)
	if err != nil {
		return nil, err
	}
	var board Board
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	if err := decoder.Decode(&board); err != nil {
		return nil, newError(CodeCorruptDocument, "the run board snapshot is invalid").withCause(err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, newError(CodeCorruptDocument, "the run board snapshot is invalid").withCause(err)
	}
	if board.ID != state.BoardID || board.ProjectID != state.ProjectID || board.Revision != state.BoardRevision {
		return nil, newError(CodeCorruptDocument, "the run board snapshot identity does not match the run")
	}
	if err := AssertPolicy(&board); err != nil {
		return nil, err
	}
	return &board, nil
}

type ReviewOutcome struct {
	SchemaVersion  uint64          `json:"schemaVersion"`
	Verdict        ReviewVerdict   `json:"verdict"`
	Effective      ReviewVerdict   `json:"effectiveVerdict"`
	Summary        string          `json:"summary"`
	Findings       []ReviewFinding `json:"findings"`
	ChangeRevision uint64          `json:"changeRevision"`
	PatchSha256    string          `json:"patchSha256"`
	SeatID         string          `json:"seatId"`
	Attempt        int             `json:"attempt"`
}

func NormalizeReviewOutcome(contents []byte, change ChangeRecord, seatID string, attempt int) (ReviewOutcome, error) {
	var wire struct {
		SchemaVersion uint64           `json:"schemaVersion"`
		Verdict       ReviewVerdict    `json:"verdict"`
		Summary       *string          `json:"summary"`
		Findings      *[]ReviewFinding `json:"findings"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return ReviewOutcome{}, fmt.Errorf("review outcome is invalid: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return ReviewOutcome{}, fmt.Errorf("review outcome is invalid: %w", err)
	}
	if wire.SchemaVersion != ReviewSchemaVersion || (wire.Verdict != ReviewApproved && wire.Verdict != ReviewChangesRequested) || wire.Summary == nil || wire.Findings == nil {
		return ReviewOutcome{}, fmt.Errorf("review outcome has an unsupported schema or verdict")
	}
	outcome := ReviewOutcome{
		SchemaVersion: ReviewSchemaVersion, Verdict: wire.Verdict, Effective: wire.Verdict,
		Summary: *wire.Summary, Findings: *wire.Findings, ChangeRevision: change.ChangeRevision,
		PatchSha256: change.PatchSha256, SeatID: seatID, Attempt: attempt,
	}
	for _, finding := range outcome.Findings {
		if finding.Severity == "blocking" {
			outcome.Effective = ReviewChangesRequested
		}
	}
	if err := ValidateReviewOutcome(outcome, change, seatID, attempt); err != nil {
		return ReviewOutcome{}, err
	}
	return outcome, nil
}

func DecodeReviewOutcome(contents []byte) (ReviewOutcome, error) {
	var outcome ReviewOutcome
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&outcome); err != nil {
		return ReviewOutcome{}, fmt.Errorf("normalized review outcome is invalid: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return ReviewOutcome{}, fmt.Errorf("normalized review outcome is invalid: %w", err)
	}
	return outcome, nil
}

func ValidateReviewOutcome(outcome ReviewOutcome, change ChangeRecord, seatID string, attempt int) error {
	if outcome.SchemaVersion != ReviewSchemaVersion || (outcome.Verdict != ReviewApproved && outcome.Verdict != ReviewChangesRequested) {
		return fmt.Errorf("review outcome has an unsupported schema or verdict")
	}
	if outcome.ChangeRevision != change.ChangeRevision || outcome.PatchSha256 != change.PatchSha256 || outcome.SeatID != seatID || outcome.Attempt != attempt {
		return fmt.Errorf("review outcome does not match the controller revision or attempt")
	}
	if len(outcome.Summary) > 4096 || len(outcome.Findings) > 100 {
		return fmt.Errorf("review outcome exceeds its bounds")
	}
	effective := outcome.Verdict
	for index := range outcome.Findings {
		finding := &outcome.Findings[index]
		if finding.Severity != "blocking" && finding.Severity != "advisory" {
			return fmt.Errorf("review finding severity is invalid")
		}
		if strings.TrimSpace(finding.Summary) == "" || len(finding.Summary) > 1000 || len(finding.Detail) > 8192 {
			return fmt.Errorf("review finding text is invalid")
		}
		if finding.Line != nil && *finding.Line < 1 {
			return fmt.Errorf("review finding line is invalid")
		}
		if finding.Severity == "blocking" {
			effective = ReviewChangesRequested
		}
	}
	if outcome.Effective != effective {
		return fmt.Errorf("review outcome effective verdict was not controller-derived")
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("review outcome contains trailing JSON")
		}
		return err
	}
	return nil
}
