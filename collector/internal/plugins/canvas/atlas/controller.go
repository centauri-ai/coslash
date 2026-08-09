package atlas

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// The Atlas run controller.
//
// It owns durable ordering and nothing else: every fact it records comes from
// the runtime, and it never infers that a turn finished from anything an agent
// wrote in a terminal. The shape mirrors DaGama's controller deliberately, with
// one structural difference — a stage here is a committee, so a single stage
// launches several attempts and then a refine turn over their drafts.

// BoardLoader reads the board a run is started from. It is an interface rather
// than the concrete store because a board store is scoped to one project, and
// the controller serves many.
type BoardLoader interface {
	Load(ctx context.Context, projectID, boardID string) (*BoardDocument, error)
}

// ControllerOptions are the controller's collaborators.
type ControllerOptions struct {
	Boards  BoardLoader
	Runs    *RunStore
	Runtime Runtime
	// RootsDirectory is the parent every isolated run root is created under.
	RootsDirectory string
	Now            func() time.Time
	Suffix         func() (string, error)
}

// Controller advances Atlas runs.
type Controller struct {
	boards         BoardLoader
	runs           *RunStore
	runtime        Runtime
	rootsDirectory string
	now            func() time.Time
	suffix         func() (string, error)
	locks          sync.Map
}

// NewController builds a controller.
func NewController(options ControllerOptions) (*Controller, error) {
	if options.Boards == nil || options.Runs == nil || options.Runtime == nil {
		return nil, newError(CodeInvalidState, "the Atlas controller dependencies are incomplete")
	}
	if options.RootsDirectory == "" || !filepath.IsAbs(options.RootsDirectory) {
		return nil, newError(CodeUnsafePath, "the Atlas run roots directory must be absolute")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	suffix := options.Suffix
	if suffix == nil {
		suffix = randomSuffix
	}
	return &Controller{
		boards: options.Boards, runs: options.Runs, runtime: options.Runtime,
		rootsDirectory: filepath.Clean(options.RootsDirectory), now: now, suffix: suffix,
	}, nil
}

func randomSuffix() (string, error) {
	var value [4]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

// RunRootsDirectory is the parent a run root is created under. The run dialog
// shows it before a run exists, so it comes from the controller rather than
// being re-derived beside it.
func (c *Controller) RunRootsDirectory() string {
	return filepath.Join(c.rootsDirectory, RootsDirectory)
}

func (c *Controller) lock(projectID, runID string) func() {
	value, _ := c.locks.LoadOrStore(projectID+"/"+runID, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

// StartRequest is one run's inputs.
type StartRequest struct {
	ProjectID   string
	BoardID     string
	ProjectPath string
	Source      SourceInput
	BaseBranch  string
}

// SourceInput is the problem statement a run is started from.
type SourceInput struct {
	Kind  string
	Title string
	Text  string
	Path  string
}

// Start creates the run and drives it to completion or to its first gate. It
// blocks for as long as the run takes, so a caller that must answer sooner —
// an HTTP request — wants StartAsync.
func (c *Controller) Start(ctx context.Context, request StartRequest) (*RunState, error) {
	_, advance, err := c.StartAsync(ctx, request)
	if err != nil {
		return nil, err
	}
	return advance(ctx)
}

// StartAsync creates the run and returns its initial state together with the
// function that advances it.
//
// Everything refusable is refused here, before the run exists: an invalid
// identity, an unreadable source, a board that is not runnable, and a project
// that already has a live run. A caller never has to discover those
// asynchronously.
func (c *Controller) StartAsync(
	ctx context.Context,
	request StartRequest,
) (*RunState, func(context.Context) (*RunState, error), error) {
	if !ValidProjectID(request.ProjectID) || !ValidBoardID(request.BoardID) {
		return nil, nil, newError(CodeInvalidState, "the run project or board identity is invalid")
	}
	source, err := CaptureSource(request.Source)
	if err != nil {
		return nil, nil, err
	}
	document, err := c.boards.Load(ctx, request.ProjectID, request.BoardID)
	if err != nil {
		return nil, nil, err
	}
	if document.ProjectID != request.ProjectID {
		return nil, nil, newError(CodeInvalidState, "the board belongs to another project")
	}
	// A graph that is not runnable is refused with the board's own explanation
	// rather than a generic one, because the operator fixes it on the board.
	if reason := document.Board.RunnableBlockedReason(); reason != "" {
		return nil, nil, policyError("graph", reason)
	}

	summaries, _, err := c.runs.List(ctx, request.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	if err := CanStartRun(summaries); err != nil {
		return nil, nil, err
	}

	suffix, err := c.suffix()
	if err != nil {
		return nil, nil, newError(CodeStorageFailed, "a run identifier could not be allocated").withCause(err)
	}
	runID, err := NewRunID(c.now().UTC(), suffix)
	if err != nil {
		return nil, nil, err
	}

	// The snapshots land before the first event, so a crash between them leaves
	// an empty directory rather than a log referencing a snapshot that is not
	// there.
	if err := c.runs.Allocate(ctx, request.ProjectID, runID, document, source.Record); err != nil {
		return nil, nil, err
	}
	state, err := c.runs.Append(ctx, request.ProjectID, runID, &RunCreated{
		ProjectID: document.ProjectID, BoardID: document.ID, BoardRevision: document.Revision,
		Title: source.Record.Title, Source: source.Record,
	})
	if err != nil {
		return nil, nil, err
	}

	projectPath := request.ProjectPath
	baseBranch := request.BaseBranch
	advance := func(background context.Context) (*RunState, error) {
		return c.advanceCreatedRun(background, document.Board, request.ProjectID, runID, projectPath, baseBranch, source)
	}
	return state, advance, nil
}

// advanceCreatedRun prepares the run root and drives the pipeline for a run
// that has already been created. It re-reads state rather than closing over it,
// so it is correct whether it runs inline or on a background goroutine.
func (c *Controller) advanceCreatedRun(
	ctx context.Context,
	board *Board,
	projectID, runID, projectPath, baseBranch string,
	source CapturedSource,
) (*RunState, error) {
	state, err := c.runs.Read(ctx, projectID, runID)
	if err != nil {
		return nil, err
	}
	runRoot := filepath.Join(c.RunRootsDirectory(), runID)
	branch := "atlas/" + runID

	prepared, err := c.runtime.Prepare(ctx, PrepareRequest{
		ProjectPath: projectPath,
		BaseBranch:  chooseBase(baseBranch, boardPublishBase(board)),
		RunID:       runID, RunRoot: runRoot, Branch: branch,
		// Atlas supports a plain folder, where the run root is a copy and
		// publication is simply unavailable rather than an error at start.
		AllowPlainFolder: true,
	})
	if err != nil {
		return c.terminate(ctx, state, ComponentIntake, 1, "preflight_failed", err)
	}
	state, err = c.runs.Append(ctx, projectID, runID, &RunRootCreated{
		RunRoot: prepared.Root.Path, Branch: prepared.Root.Branch,
		BaseBranch: prepared.Root.PublishBaseBranch, BaseSha: prepared.Root.BaseSha,
		RemoteURL:         prepared.RemoteURL,
		PublishBaseBranch: prepared.Root.PublishBaseBranch,
		PublishBaseSha:    prepared.Root.PublishBaseSha,
	})
	if err != nil {
		return nil, err
	}
	state, err = c.runIntake(ctx, state, board, source)
	if err != nil {
		return state, err
	}
	return settle(c.runPipeline(ctx, board, state, source, 1))
}

func chooseBase(requested, configured string) string {
	if trimmed := strings.TrimSpace(requested); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(configured)
}

// runIntake records the problem statement as the run's first artifact. Intake
// runs no model: it renders what the operator supplied.
func (c *Controller) runIntake(
	ctx context.Context,
	state *RunState,
	board *Board,
	source CapturedSource,
) (*RunState, error) {
	projectID, runID := state.ProjectID, state.RunID
	state, err := c.runs.Append(ctx, projectID, runID, &ComponentReadyEvent{ComponentID: ComponentIntake, Instance: 1})
	if err != nil {
		return state, err
	}
	state, err = c.runs.Append(ctx, projectID, runID, &ComponentStartedEvent{ComponentID: ComponentIntake, Instance: 1})
	if err != nil {
		return state, err
	}

	producer := ArtifactProducer{ComponentID: ComponentIntake, Instance: 1}
	outputs := make([]string, 0, 2)
	for name, contents := range map[string][]byte{
		"SOURCE.md":  source.Body,
		"PROBLEM.md": problemStatement(source, board),
	} {
		record, recordErr := c.runtime.RecordControllerArtifact(ctx, state.RunRoot, name, contents, producer)
		if recordErr != nil {
			return c.fail(ctx, state, ComponentIntake, 1, "invalid_output", recordErr)
		}
		state, err = c.runs.Append(ctx, projectID, runID, &ArtifactPromoted{Artifact: record})
		if err != nil {
			return state, err
		}
		outputs = append(outputs, name)
	}
	slices.Sort(outputs)
	return c.runs.Append(ctx, projectID, runID, &ComponentSucceededEvent{
		ComponentID: ComponentIntake, Instance: 1, Outputs: outputs,
	})
}

// problemStatement renders the operator's input into the artifact every seat
// reads. The source body is delivered verbatim and fenced: it is data the run
// was started from, not instructions the pipeline obeys.
func problemStatement(source CapturedSource, board *Board) []byte {
	var builder strings.Builder
	builder.WriteString("# Problem\n\n")
	builder.WriteString("Title: " + source.Record.Title + "\n\n")
	if instructions := strings.TrimSpace(board.Instructions); instructions != "" {
		builder.WriteString("## Project conventions\n\n")
		builder.WriteString(instructions + "\n\n")
	}
	builder.WriteString("## Source\n\n")
	appendFence(&builder, "source", source.Body)
	return []byte(builder.String())
}

// fail records a stage failure without ending the run, for the cases a retry or
// a repair round can still recover from.
func (c *Controller) fail(
	ctx context.Context,
	state *RunState,
	component ComponentID,
	instance uint64,
	reason string,
	cause error,
) (*RunState, error) {
	next, err := c.runs.Append(ctx, state.ProjectID, state.RunID, &ComponentFailedEvent{
		ComponentID: component, Instance: instance,
		Reason: reason, Message: safeMessage(cause),
	})
	if err != nil {
		return next, err
	}
	return next, nil
}

// terminate records a stage failure that ends the run.
func (c *Controller) terminate(
	ctx context.Context,
	state *RunState,
	component ComponentID,
	instance uint64,
	reason string,
	cause error,
) (*RunState, error) {
	state, err := c.fail(ctx, state, component, instance, reason, cause)
	if err != nil {
		return state, err
	}
	finished, appendErr := c.runs.Append(ctx, state.ProjectID, state.RunID, &RunFinished{
		Status: RunFailed, Reason: reason, Message: safeMessage(cause),
	})
	if appendErr != nil {
		return finished, appendErr
	}
	// Cleanup is best effort by design: a run that has already failed must not
	// be reported as a different failure because its temporary files lingered.
	_ = c.runtime.Cleanup(ctx, finished)
	return finished, nil
}

// ReadRun returns one run's materialized state.
func (c *Controller) ReadRun(ctx context.Context, projectID, runID string) (*RunState, error) {
	return c.runs.Read(ctx, projectID, runID)
}

// ListRuns returns a project's run summaries.
func (c *Controller) ListRuns(ctx context.Context, projectID string) ([]RunSummary, error) {
	summaries, _, err := c.runs.List(ctx, projectID)
	return summaries, err
}

// safeMessage keeps a client-facing message free of anything the error wrapped.
func safeMessage(err error) string {
	if err == nil {
		return "the operation failed"
	}
	var atlasError *Error
	if errors.As(err, &atlasError) {
		return atlasError.Message
	}
	return "the operation could not be completed; inspect server diagnostics"
}

func attemptIDFor(runID string, component ComponentID, instance uint64, seatID string, attempt uint64) string {
	return fmt.Sprintf("%s-%s-%d-%s-%d", runID, component, instance, seatID, attempt)
}

// A board omits its run policy entirely when it configures neither checks nor a
// publish target, so every read of it goes through a nil-safe accessor rather
// than through a nil check at each call site.

func boardPublishBase(board *Board) string {
	if board == nil || board.RunPolicy == nil {
		return ""
	}
	return board.RunPolicy.Publish.Base
}

func boardChecks(board *Board) []Check {
	if board == nil || board.RunPolicy == nil {
		return nil
	}
	return board.RunPolicy.Checks
}

func boardPublishDraft(board *Board) bool {
	if board == nil || board.RunPolicy == nil {
		// A board that never configured publication opens a draft, which is the
		// same default the publish config carries.
		return true
	}
	return board.RunPolicy.Publish.Draft
}
