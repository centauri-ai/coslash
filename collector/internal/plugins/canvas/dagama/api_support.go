package dagama

import (
	"context"
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/publication"
)

// Read-side controller operations the HTTP layer needs.
//
// They live here rather than in the handler because each one needs the run's
// board, source, or artifact rules, and duplicating those in a second place is
// how a client and a controller start disagreeing about what a run contains.

// RunRootsDirectory is the parent every run root is created under. The run
// dialog shows it before a run exists, so it must come from the controller
// rather than being re-derived beside it.
func (c *Controller) RunRootsDirectory() string {
	return filepath.Join(c.rootsDirectory, "roots")
}

// ReadArtifact returns the contents of a promoted artifact by name.
//
// Only a promoted artifact is readable: the name is resolved against the run's
// own artifact records, never joined onto the run root, so a crafted name
// cannot address a file the run never produced. The newest record wins, because
// a repair round republishes the same names.
func (c *Controller) ReadArtifact(ctx context.Context, projectID, runID, name string) ([]byte, error) {
	state, err := c.runs.Read(ctx, projectID, runID)
	if err != nil {
		return nil, err
	}
	var found *ArtifactRecord
	for index := range state.Artifacts {
		if state.Artifacts[index].Name == name {
			found = &state.Artifacts[index]
		}
	}
	if found == nil {
		return nil, newError(CodeNotFound, "the run has no artifact with that name")
	}
	if err := AssertArtifactReference(*found); err != nil {
		return nil, err
	}
	return c.runtime.ReadArtifact(ctx, state.RunRoot, *found)
}

// AssembledPrompt recomposes the prompt a seat attempt was launched with.
//
// The prompt is not stored: it is derived from the board, the captured source,
// and the artifacts promoted so far, and storing a second copy would let the
// two drift. Recomposing from the same inputs is what makes "show me what this
// seat was told" answerable and honest.
func (c *Controller) AssembledPrompt(ctx context.Context, projectID, runID string, component ComponentID) (string, error) {
	if !HasSeat(component) {
		return "", newError(CodeInvalidState, "only an agent component has an assembled prompt")
	}
	unlock := c.lock(projectID, runID)
	defer unlock()
	state, err := c.runs.Read(ctx, projectID, runID)
	if err != nil {
		return "", err
	}
	current := state.Components[component]
	if current == nil {
		return "", newError(CodeInvalidState, "the component is not part of this run")
	}
	board, err := c.boardForRun(ctx, state)
	if err != nil {
		return "", err
	}
	source, err := c.sourceForRun(ctx, state)
	if err != nil {
		return "", err
	}
	artifacts, err := c.promptArtifacts(ctx, state)
	if err != nil {
		return "", err
	}
	instance := current.Instance
	if instance < 1 {
		instance = 1
	}
	attempt := 1
	if current.Attempt != nil && current.Attempt.Attempt > 0 {
		attempt = current.Attempt.Attempt
	}
	return ComposePrompt(PromptInput{
		Component: component, Instance: instance, Attempt: attempt,
		Source: source, Artifacts: artifacts, Repair: instance > 1,
		Instructions: board.Instructions, Steering: seatPrompt(board, component),
	})
}

// PublishRequest assembles the publication facts for a run so a caller can run
// preflight against exactly what publication would use.
func (c *Controller) PublishRequest(ctx context.Context, projectID, runID string) (publication.Request, error) {
	unlock := c.lock(projectID, runID)
	defer unlock()
	state, err := c.runs.Read(ctx, projectID, runID)
	if err != nil {
		return publication.Request{}, err
	}
	if state.Change == nil {
		return publication.Request{}, newError(CodeInvalidState, "the run has no current revision")
	}
	board, err := c.boardForRun(ctx, state)
	if err != nil {
		return publication.Request{}, err
	}
	verify, err := c.latestVerification(ctx, state)
	if err != nil {
		return publication.Request{}, err
	}
	review, err := c.latestReview(ctx, state)
	if err != nil {
		return publication.Request{}, err
	}
	return publishRequestFor(PublishRequest{
		State: state, Board: board, Review: review, Verification: verify,
	}), nil
}

// ReadRun returns one run's materialized state.
func (c *Controller) ReadRun(ctx context.Context, projectID, runID string) (*RunState, error) {
	return c.runs.Read(ctx, projectID, runID)
}

// ListRuns returns the run summaries for a project, newest first.
func (c *Controller) ListRuns(ctx context.Context, projectID string) ([]RunSummary, error) {
	return c.runs.List(ctx, projectID)
}
