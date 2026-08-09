package dagama

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/terminal"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/verification"
)

type RunReport struct {
	SchemaVersion uint64                             `json:"schemaVersion"`
	RunID         string                             `json:"runId"`
	ProjectID     string                             `json:"projectId"`
	BoardID       string                             `json:"boardId"`
	BoardRevision uint64                             `json:"boardRevision"`
	Status        RunStatus                          `json:"status"`
	Source        *SourceRecord                      `json:"source"`
	BaseSha       string                             `json:"baseSha"`
	Branch        string                             `json:"branch"`
	Checks        []Check                            `json:"checks"`
	Components    map[ComponentID]*ComponentRunState `json:"components"`
	Artifacts     []ArtifactRecord                   `json:"artifacts"`
	Change        *ChangeRecord                      `json:"change"`
	Verification  *verification.Document             `json:"verification"`
	Review        *ReviewOutcome                     `json:"review"`
	Gate          *GateRecord                        `json:"gate"`
	Publication   *PublicationRecord                 `json:"publication"`
	Failure       *RunFailure                        `json:"failure"`
}

type ControllerOptions struct {
	Boards         *BoardStore
	Runs           *RunStore
	Runtime        Runtime
	RootsDirectory string
	Now            func() time.Time
	Suffix         func() (string, error)
}

type Controller struct {
	boards         *BoardStore
	runs           *RunStore
	runtime        Runtime
	rootsDirectory string
	now            func() time.Time
	suffix         func() (string, error)
	locks          sync.Map
}

type StartRequest struct {
	ProjectID  string
	BoardID    string
	Source     SourceInput
	BaseBranch string
}

func NewController(options ControllerOptions) (*Controller, error) {
	if options.Boards == nil || options.Runs == nil || options.Runtime == nil {
		return nil, newError(CodeInvalidState, "the DaGama controller dependencies are incomplete")
	}
	if options.RootsDirectory == "" || !filepath.IsAbs(options.RootsDirectory) {
		return nil, newError(CodeUnsafePath, "the DaGama run roots directory must be absolute")
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

func (c *Controller) lock(projectID, runID string) func() {
	value, _ := c.locks.LoadOrStore(projectID+"/"+runID, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

func (c *Controller) Start(ctx context.Context, request StartRequest) (*RunState, error) {
	if !ValidProjectID(request.ProjectID) || !ValidBoardID(request.BoardID) {
		return nil, newError(CodeInvalidState, "the run project or board identity is invalid")
	}
	source, err := CaptureSource(request.Source)
	if err != nil {
		return nil, err
	}
	board, err := c.boards.Load(ctx, request.ProjectID, request.BoardID)
	if err != nil {
		return nil, err
	}
	if board.ProjectID != request.ProjectID {
		return nil, newError(CodeInvalidState, "the board belongs to another project")
	}
	suffix, err := c.suffix()
	if err != nil {
		return nil, newError(CodeStorageFailed, "a run identifier could not be allocated").withCause(err)
	}
	runID, err := NewRunID(c.now().UTC(), suffix)
	if err != nil {
		return nil, err
	}
	unlock := c.lock(request.ProjectID, runID)
	defer unlock()
	return c.startLocked(ctx, board, runID, source, request.BaseBranch)
}

func (c *Controller) startLocked(ctx context.Context, board *Board, runID string, source CapturedSource, baseBranch string) (*RunState, error) {
	state, err := c.runs.Append(ctx, board.ProjectID, runID, &RunCreated{
		ProjectID: board.ProjectID, BoardID: board.ID, BoardRevision: board.Revision,
		Title: source.Record.Title, Source: source.Record,
	})
	if err != nil {
		return nil, err
	}
	runRoot := filepath.Join(c.rootsDirectory, "roots", runID)
	branch := "dagama/" + runID
	prepared, err := c.runtime.Prepare(ctx, PrepareRequest{
		ProjectPath: board.ProjectPath, BaseBranch: chooseBase(baseBranch, board.Components.Publish.Publish.Base),
		RunID: runID, RunRoot: runRoot, Branch: branch,
	})
	if err != nil {
		return c.terminateFailure(ctx, state, ComponentIntake, 1, "preflight_failed", err)
	}
	state, err = c.runs.Append(ctx, board.ProjectID, runID, &RunRootCreated{
		RunRoot: prepared.Root.Path, Branch: prepared.Root.Branch, BaseBranch: prepared.Root.PublishBaseBranch,
		BaseSha: prepared.Root.BaseSha, RemoteURL: prepared.RemoteURL,
	})
	if err != nil {
		return nil, err
	}
	state, err = c.runIntake(ctx, state, source, board)
	if err != nil {
		return state, err
	}
	return c.runPipeline(ctx, board, state, source, 1)
}

func chooseBase(requested, configured string) string {
	if strings.TrimSpace(requested) != "" {
		return strings.TrimSpace(requested)
	}
	return strings.TrimSpace(configured)
}

func (c *Controller) runIntake(ctx context.Context, state *RunState, source CapturedSource, board *Board) (*RunState, error) {
	projectID, runID := state.ProjectID, state.RunID
	var err error
	state, err = c.runs.Append(ctx, projectID, runID, &ComponentReady{ComponentInstance{ComponentID: ComponentIntake, Instance: 1}})
	if err != nil {
		return state, err
	}
	state, err = c.runs.Append(ctx, projectID, runID, &ComponentStarted{ComponentInstance{ComponentID: ComponentIntake, Instance: 1}})
	if err != nil {
		return state, err
	}
	sourceJSON, err := json.Marshal(struct {
		SchemaVersion uint64 `json:"schemaVersion"`
		SourceRecord
		CapturedAt time.Time `json:"capturedAt"`
	}{1, source.Record, c.now().UTC()})
	if err != nil {
		return c.terminateFailure(ctx, state, ComponentIntake, 1, "invalid_output", err)
	}
	boardJSON, err := json.Marshal(board)
	if err != nil {
		return c.terminateFailure(ctx, state, ComponentIntake, 1, "invalid_output", err)
	}
	items := []struct {
		name, kind string
		contents   []byte
	}{
		{"SOURCE.md", "source", source.Body},
		{"source.json", "source_provenance", sourceJSON},
		{"PROBLEM.md", "problem", RenderProblem(source)},
		{"board.snapshot.json", "board_snapshot", boardJSON},
	}
	outputs := make([]string, 0, len(items))
	for _, item := range items {
		record, promoteErr := c.runtime.RecordControllerArtifact(ctx, state.RunRoot, item.name, item.contents, ProducerRef{Component: ComponentIntake, Instance: 1})
		if promoteErr != nil {
			return c.terminateFailure(ctx, state, ComponentIntake, 1, classifyError(promoteErr), promoteErr)
		}
		state, err = c.runs.Append(ctx, projectID, runID, &ArtifactPromoted{Artifact: record})
		if err != nil {
			return state, err
		}
		outputs = append(outputs, record.ArtifactID)
	}
	return c.runs.Append(ctx, projectID, runID, &ComponentSucceeded{ComponentInstance: ComponentInstance{ComponentID: ComponentIntake, Instance: 1}, Outputs: outputs})
}

func (c *Controller) finishFailure(ctx context.Context, state *RunState, component ComponentID, instance int, reason string, cause error) (*RunState, error) {
	message := safeMessage(cause)
	failed, appendErr := c.runs.Append(ctx, state.ProjectID, state.RunID, &ComponentFailed{
		ComponentInstance: ComponentInstance{ComponentID: component, Instance: instance}, Reason: reason, Message: message,
	})
	if appendErr != nil {
		return state, appendErr
	}
	return failed, safeFailure(cause)
}

func (c *Controller) terminateFailure(ctx context.Context, state *RunState, component ComponentID, instance int, reason string, cause error) (*RunState, error) {
	failed, err := c.finishFailure(ctx, state, component, instance, reason, cause)
	if err != nil && failed == state {
		return state, err
	}
	finished, appendErr := c.runs.Append(ctx, state.ProjectID, state.RunID, &RunFinished{Status: RunFailed, Reason: reason, Message: safeMessage(cause)})
	if appendErr != nil {
		return failed, appendErr
	}
	return finished, safeFailure(cause)
}

func safeMessage(err error) string {
	if err == nil {
		return "the operation failed"
	}
	var dagamaError *Error
	if errors.As(err, &dagamaError) {
		return dagamaError.Message
	}
	return "the operation could not be completed; inspect server diagnostics"
}

func safeFailure(err error) error {
	if err == nil {
		return newError(CodeInvalidState, "the component failed")
	}
	var dagamaError *Error
	if errors.As(err, &dagamaError) {
		return dagamaError
	}
	return newError(CodeInvalidState, "the component failed; inspect the persisted run state").withCause(err)
}

func classifyError(err error) string {
	if err == nil {
		return "invalid_output"
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "missing") || errors.Is(err, terminal.ErrNotFound):
		return "missing_output"
	case strings.Contains(text, "timeout") || strings.Contains(text, "timed out"):
		return "timed_out"
	case strings.Contains(text, "not installed") || strings.Contains(text, "not found"):
		return "provider_missing"
	case strings.Contains(text, "launch") || strings.Contains(text, "tmux"):
		return "launch_failed"
	default:
		return "invalid_output"
	}
}

func (c *Controller) writeReport(ctx context.Context, state *RunState, finalStatus ...RunStatus) (*RunState, error) {
	status := state.Status
	if len(finalStatus) > 0 {
		status = finalStatus[0]
	}
	board, err := c.boardForRun(ctx, state)
	if err != nil {
		return state, err
	}
	report := RunReport{SchemaVersion: 1, RunID: state.RunID, ProjectID: state.ProjectID, BoardID: state.BoardID, BoardRevision: state.BoardRevision, Status: status, Source: state.Source, BaseSha: state.BaseSha, Branch: state.Branch, Checks: board.Components.Verify.Checks, Components: state.Components, Artifacts: state.Artifacts, Change: state.Change, Gate: state.Gate, Publication: state.Publication, Failure: state.Failure}
	if _, ok := latestArtifact(state, "verification.json"); ok {
		document, readErr := c.latestVerification(ctx, state)
		if readErr != nil {
			return state, readErr
		}
		report.Verification = &document
	}
	if _, ok := latestArtifact(state, "review.json"); ok {
		outcome, readErr := c.latestReview(ctx, state)
		if readErr != nil {
			return state, readErr
		}
		report.Review = &outcome
	}
	contents, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return state, err
	}
	record, err := c.runtime.RecordControllerArtifact(ctx, state.RunRoot, "run-report.json", contents, ProducerRef{Component: ComponentPublish, Instance: 1})
	if err != nil {
		return state, err
	}
	return c.runs.Append(ctx, state.ProjectID, state.RunID, &ArtifactPromoted{Artifact: record})
}

func (c *Controller) Retry(ctx context.Context, projectID, runID string, componentID ComponentID) (*RunState, error) {
	unlock := c.lock(projectID, runID)
	defer unlock()
	state, err := c.runs.Read(ctx, projectID, runID)
	if err != nil {
		return nil, err
	}
	if isTerminal(state.Status) {
		return nil, newError(CodeInvalidState, "a terminal run cannot be retried")
	}
	component := state.Components[componentID]
	if component == nil || component.Status != ComponentFailedStatus || !HasSeat(componentID) {
		return nil, newError(CodeInvalidState, "only a failed agent component can be retried")
	}
	board, err := c.boardForRun(ctx, state)
	if err != nil {
		return nil, err
	}
	source, err := c.sourceForRun(ctx, state)
	if err != nil {
		return nil, err
	}
	attempt := 1
	if component.Attempt != nil {
		attempt = component.Attempt.Attempt + 1
	}
	state, err = c.runSeat(ctx, board, state, source, componentID, component.Instance, attempt, resumeIdentity(board, state, componentID))
	if err != nil {
		return state, err
	}
	return c.continueAfterAgent(ctx, board, state, source, componentID, component.Instance)
}
