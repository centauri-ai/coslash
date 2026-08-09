package atlas

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/terminal"
)

// The review verdict and the failure taxonomy.
//
// A review artifact is agent-authored, which makes it untrusted input to the
// gate that decides whether a change publishes. It is therefore read
// fail-closed: anything the controller cannot positively read as an approval is
// not an approval.

// ReviewOutcome is the verdict a review turn wrote.
type ReviewOutcome struct {
	SchemaVersion uint64          `json:"schemaVersion"`
	Verdict       string          `json:"verdict"`
	Summary       string          `json:"summary"`
	Findings      []ReviewFinding `json:"findings"`
}

// ReviewFinding is one issue a review raised.
type ReviewFinding struct {
	Severity string  `json:"severity"`
	File     *string `json:"file"`
	Line     *int    `json:"line"`
	Summary  string  `json:"summary"`
	Detail   string  `json:"detail"`
}

// Review verdict values, as written by an agent.
const (
	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes_requested"
	SeverityBlocking       = "blocking"
)

// Approved reports the effective verdict.
//
// A stated approval carrying a blocking finding is not an approval. The two
// halves are written by the same turn and can disagree; when they do, the
// stricter one wins, because publishing on a self-contradictory review is the
// failure that cannot be undone.
func (o ReviewOutcome) Approved() bool {
	if !strings.EqualFold(strings.TrimSpace(o.Verdict), ReviewApproved) {
		return false
	}
	for _, finding := range o.Findings {
		if strings.EqualFold(strings.TrimSpace(finding.Severity), SeverityBlocking) {
			return false
		}
	}
	return true
}

// reviewApproved reads the latest review verdict for the run.
func (c *Controller) reviewApproved(ctx context.Context, state *RunState) (bool, error) {
	outcome, err := c.latestReview(ctx, state)
	if err != nil {
		return false, err
	}
	return outcome.Approved(), nil
}

// latestReview returns the newest review.json the run promoted.
func (c *Controller) latestReview(ctx context.Context, state *RunState) (ReviewOutcome, error) {
	for index := len(state.Artifacts) - 1; index >= 0; index-- {
		record := state.Artifacts[index]
		if record.Name != "review.json" {
			continue
		}
		contents, err := c.runtime.ReadArtifact(ctx, state.RunRoot, record)
		if err != nil {
			return ReviewOutcome{}, err
		}
		var outcome ReviewOutcome
		if err := json.Unmarshal(contents, &outcome); err != nil {
			// An unreadable verdict is a changes_requested, not a crash: the
			// run still has a bounded repair round to spend on it.
			return ReviewOutcome{Verdict: ReviewChangesRequested}, nil
		}
		return outcome, nil
	}
	return ReviewOutcome{}, newError(CodeNotFound, "the run has no review verdict")
}

// classify maps a runtime failure onto the stable taxonomy the run cards show.
//
// An unrecognised failure becomes launch_failed rather than a new string: the
// taxonomy is what an operator filters and reports on, and silently growing it
// makes past runs unqueryable.
func classify(err error) string {
	if err == nil {
		return "launch_failed"
	}
	var atlasError *Error
	if errors.As(err, &atlasError) {
		switch atlasError.Code {
		case CodeUnsafePath, CodePolicyViolation:
			return "invalid_output"
		case CodeStorageFailed, CodeLogFull:
			return "storage_failed"
		}
	}
	return "launch_failed"
}

// terminalName derives the tmux session name for an attempt.
func terminalName(attemptID string) (string, error) {
	return terminal.Name("atlas", attemptID)
}
