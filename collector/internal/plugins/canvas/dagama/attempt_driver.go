package dagama

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/agentexec"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/artifacts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/revision"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/terminal"
)

const (
	defaultAttemptPoll        = 100 * time.Millisecond
	defaultSessionBindTimeout = 30 * time.Second
)

type NativeAttemptOptions struct {
	Terminals          *terminal.Manager
	Git                *revision.Git
	PollInterval       time.Duration
	SessionBindTimeout time.Duration
	Now                func() time.Time
}

// NativeAttemptDriver is the production Task 04/05 adapter. The agent is the
// direct child of a retained tmux pane; tmux, not terminal text, owns the exact
// completion fact. Pane capture is used only to discover Codex's thread id.
type NativeAttemptDriver struct {
	terminals          *terminal.Manager
	git                *revision.Git
	pollInterval       time.Duration
	sessionBindTimeout time.Duration
	now                func() time.Time
}

func NewNativeAttemptDriver(options NativeAttemptOptions) *NativeAttemptDriver {
	poll := options.PollInterval
	if poll <= 0 {
		poll = defaultAttemptPoll
	}
	bindTimeout := options.SessionBindTimeout
	if bindTimeout <= 0 {
		bindTimeout = defaultSessionBindTimeout
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &NativeAttemptDriver{terminals: options.Terminals, git: options.Git, pollInterval: poll, sessionBindTimeout: bindTimeout, now: now}
}

func (driver *NativeAttemptDriver) Execute(ctx context.Context, request AttemptRequest, launched LaunchRecorder) (AttemptResult, error) {
	if driver.terminals == nil || driver.git == nil {
		return AttemptResult{}, newError(CodeInvalidState, "the native attempt dependencies are incomplete")
	}
	if err := driver.prepare(ctx, request); err != nil {
		return AttemptResult{}, err
	}
	command, session, err := driver.command(request, true)
	if err != nil {
		return AttemptResult{}, err
	}
	tmuxName, err := terminal.Name("dagama", request.AttemptID)
	if err != nil {
		return AttemptResult{}, err
	}
	if _, err = driver.terminals.CreateTracked(ctx, terminal.Spec{ID: request.AttemptID, TmuxName: tmuxName, Command: command, Writable: false, PreserveOnClose: true}); err != nil {
		return AttemptResult{}, err
	}
	session, err = driver.bindSession(ctx, request, session)
	if err != nil {
		_ = driver.terminals.Stop(context.WithoutCancel(ctx), request.AttemptID)
		return AttemptResult{}, err
	}
	if err := launched(session); err != nil {
		_ = driver.terminals.Stop(context.WithoutCancel(ctx), request.AttemptID)
		return AttemptResult{}, err
	}
	completion, err := driver.wait(ctx, request, session)
	if err != nil {
		return AttemptResult{}, err
	}
	return driver.collect(ctx, request, completion)
}

func (driver *NativeAttemptDriver) command(request AttemptRequest, headless bool) (agentexec.Command, contracts.SessionIdentity, error) {
	vendor := agentexec.Vendor(request.Seat.Vendor)
	mode := agentexec.Start
	sessionID := ""
	parentVendor := agentexec.Vendor("")
	parentID := ""
	if request.Resume != nil {
		if request.Resume.Agent != string(request.Seat.Vendor) {
			return agentexec.Command{}, contracts.SessionIdentity{}, newError(CodeInvalidState, "the resume session belongs to another provider")
		}
		mode = agentexec.Resume
		parentVendor = vendor
		parentID = request.Resume.ID
	}
	if vendor == agentexec.Claude && mode == agentexec.Start {
		generated, err := uuidV4()
		if err != nil {
			return agentexec.Command{}, contracts.SessionIdentity{}, err
		}
		sessionID = generated
	}
	command, err := agentexec.Build(agentexec.Request{
		Vendor: vendor, Mode: mode, CWD: request.RunRoot, SessionID: sessionID,
		ParentVendor: parentVendor, ParentSessionID: parentID,
		Model: request.Seat.Model, Effort: request.Seat.Effort, Permission: request.Seat.Permission,
		Prompt: request.Prompt, Headless: headless,
	})
	if err != nil {
		return agentexec.Command{}, contracts.SessionIdentity{}, err
	}
	return command, contracts.SessionIdentity{Agent: string(request.Seat.Vendor), ID: command.ExpectedSessionID}, nil
}

func (driver *NativeAttemptDriver) bindSession(ctx context.Context, request AttemptRequest, session contracts.SessionIdentity) (contracts.SessionIdentity, error) {
	if session.ID != "" {
		return session, nil
	}
	timer := time.NewTimer(driver.sessionBindTimeout)
	defer timer.Stop()
	for {
		captured, captureErr := driver.terminals.Capture(ctx, request.AttemptID)
		if captureErr == nil {
			if id := agentexec.CaptureSessionID(agentexec.Vendor(request.Seat.Vendor), captured); id != "" {
				session.ID = id
				return session, nil
			}
		}
		status, statusErr := driver.terminals.TrackedStatus(ctx, request.AttemptID)
		if statusErr != nil {
			return contracts.SessionIdentity{}, statusErr
		}
		if status.State == "exited" {
			return contracts.SessionIdentity{}, newError(CodeInvalidState, "the provider exited before reporting a session identity")
		}
		select {
		case <-ctx.Done():
			return contracts.SessionIdentity{}, ctx.Err()
		case <-timer.C:
			return contracts.SessionIdentity{}, newError(CodeInvalidState, "the provider did not report a session identity in time")
		case <-time.After(driver.pollInterval):
		}
	}
}

func (driver *NativeAttemptDriver) wait(ctx context.Context, request AttemptRequest, session contracts.SessionIdentity) (AttemptResult, error) {
	for {
		status, err := driver.terminals.TrackedStatus(ctx, request.AttemptID)
		if err != nil {
			return AttemptResult{}, err
		}
		if status.State == "exited" {
			if status.ExitCode == nil || status.FinishedAt == nil {
				return AttemptResult{}, newError(CodeInvalidState, "the provider exit record is incomplete")
			}
			return AttemptResult{ExitCode: *status.ExitCode, Session: session, FinishedAt: *status.FinishedAt}, nil
		}
		select {
		case <-ctx.Done():
			return AttemptResult{}, ctx.Err()
		case <-time.After(driver.pollInterval):
		}
	}
}

func (driver *NativeAttemptDriver) prepare(ctx context.Context, request AttemptRequest) error {
	scope, err := runfs.OpenScope(request.RunRoot, runfs.ScopeOptions{})
	if err != nil {
		return err
	}
	if err := scope.MkdirAll(ctx, attemptOutputDirectory(request)); err != nil {
		return err
	}
	metadata := attemptMetadataDirectory(request)
	if err := scope.MkdirAll(ctx, metadata); err != nil {
		return err
	}
	if err := scope.AtomicWrite(ctx, path.Join(metadata, "prompt.md"), []byte(request.Prompt)); err != nil {
		return err
	}
	if request.Component == ComponentReview {
		oid, captureErr := driver.git.CaptureTreeOID(ctx, revision.CaptureOptions{RunRoot: request.RunRoot, IndexFile: captureIndexPath(request, "review-before"), ExcludeExchangePaths: true})
		if captureErr != nil {
			return captureErr
		}
		if err := scope.AtomicWrite(ctx, path.Join(metadata, "before-tree-oid.txt"), []byte(oid+"\n")); err != nil {
			return err
		}
	}
	return nil
}

func (driver *NativeAttemptDriver) collect(ctx context.Context, request AttemptRequest, result AttemptResult) (AttemptResult, error) {
	if result.ExitCode != 0 {
		return result, nil
	}
	scope, err := runfs.OpenScope(request.RunRoot, runfs.ScopeOptions{})
	if err != nil {
		return AttemptResult{}, err
	}
	store, err := artifacts.NewStore(ctx, scope, artifacts.Options{Now: driver.now})
	if err != nil {
		return AttemptResult{}, err
	}
	for _, name := range requiredOutputs(request.Component) {
		maxBytes := artifacts.MaxMarkdownBytes
		if filepath.Ext(name) == ".json" {
			maxBytes = artifacts.MaxJSONBytes
		}
		record, promoteErr := store.PromoteCandidate(ctx,
			artifacts.CandidateOptions{Directory: attemptOutputDirectory(request), Name: name, MaxBytes: maxBytes, RequireUTF8: true},
			artifacts.PromoteOptions{Name: name, Kind: string(request.Component), Extension: filepath.Ext(name), Producer: artifactProducer(request)},
		)
		if promoteErr != nil {
			return AttemptResult{}, promoteErr
		}
		result.Artifacts = append(result.Artifacts, fromArtifactRecord(record))
	}
	if request.Component == ComponentBuild && result.ExitCode == 0 {
		captured, captureErr := driver.git.CaptureRevision(ctx, revision.CaptureRevisionOptions{
			CaptureOptions: revision.CaptureOptions{RunRoot: request.RunRoot, IndexFile: captureIndexPath(request, "build"), ExcludeExchangePaths: true},
			RunID:          request.RunID, ChangeRevision: uint64(request.Instance), BaseSha: request.BaseSha, RefNamespace: "dagama",
		})
		if captureErr != nil {
			return AttemptResult{}, captureErr
		}
		result.Change = &captured
		patchRecord, promoteErr := store.Promote(ctx, captured.Patch, artifacts.PromoteOptions{Name: "CHANGESET.patch", Kind: "patch", Extension: ".patch", Producer: artifactProducer(request)})
		if promoteErr != nil {
			return AttemptResult{}, promoteErr
		}
		result.Artifacts = append(result.Artifacts, fromArtifactRecord(patchRecord))
	}
	if request.Component == ComponentReview {
		before, readErr := scope.ReadFile(ctx, path.Join(attemptMetadataDirectory(request), "before-tree-oid.txt"))
		if readErr != nil {
			return AttemptResult{}, readErr
		}
		after, captureErr := driver.git.CaptureTreeOID(ctx, revision.CaptureOptions{RunRoot: request.RunRoot, IndexFile: captureIndexPath(request, "review-after"), ExcludeExchangePaths: true})
		if captureErr != nil {
			return AttemptResult{}, captureErr
		}
		result.ReviewerMutated = string(before) != after+"\n"
	}
	return result, nil
}

func (driver *NativeAttemptDriver) Cancel(ctx context.Context, state *RunState) ([]byte, error) {
	attempt := liveAttempt(state)
	if attempt == nil {
		return nil, nil
	}
	if stopErr := driver.terminals.Stop(context.WithoutCancel(ctx), attempt.AttemptID); stopErr != nil && !errors.Is(stopErr, terminal.ErrNotFound) {
		return nil, stopErr
	}
	captured, err := driver.git.CaptureRevision(ctx, revision.CaptureRevisionOptions{
		CaptureOptions: revision.CaptureOptions{RunRoot: state.RunRoot, IndexFile: filepath.Join(filepath.Dir(state.RunRoot), "."+state.RunID+"-cancel.index"), ExcludeExchangePaths: true},
		RunID:          state.RunID, ChangeRevision: nextChangeRevision(state), BaseSha: state.BaseSha, RefNamespace: "dagama",
	})
	if err != nil {
		return nil, err
	}
	return captured.Patch, nil
}

func (driver *NativeAttemptDriver) Release(ctx context.Context, attempt AttemptState) error {
	err := driver.terminals.Stop(ctx, attempt.AttemptID)
	if errors.Is(err, terminal.ErrNotFound) {
		return nil
	}
	return err
}

func (driver *NativeAttemptDriver) Takeover(ctx context.Context, request AttemptRequest, prior AttemptState) (AttemptResult, error) {
	if err := driver.terminals.Stop(ctx, prior.AttemptID); err != nil && !errors.Is(err, terminal.ErrNotFound) {
		return AttemptResult{}, err
	}
	command, session, err := driver.command(request, false)
	if err != nil {
		return AttemptResult{}, err
	}
	tmuxName, err := terminal.Name("dagama", request.AttemptID)
	if err != nil {
		return AttemptResult{}, err
	}
	if _, err = driver.terminals.CreateTracked(ctx, terminal.Spec{ID: request.AttemptID, TmuxName: tmuxName, Command: command, Writable: true, PreserveOnClose: true}); err != nil {
		return AttemptResult{}, err
	}
	return AttemptResult{Session: session}, nil
}

func (driver *NativeAttemptDriver) Handback(ctx context.Context, request AttemptRequest, attempt AttemptState) (AttemptResult, error) {
	if err := driver.terminals.Stop(ctx, attempt.AttemptID); err != nil && !errors.Is(err, terminal.ErrNotFound) {
		return AttemptResult{}, err
	}
	if request.Prompt == "" {
		request.Prompt = "Continue the assigned DaGama seat and write every required output to the documented attempt output directory."
	}
	return driver.Execute(ctx, request, func(contracts.SessionIdentity) error { return nil })
}

func (driver *NativeAttemptDriver) Probe(ctx context.Context, state *RunState, attempt AttemptState) (ProbeResult, error) {
	status, err := driver.terminals.TrackedStatus(ctx, attempt.AttemptID)
	if errors.Is(err, terminal.ErrNotFound) {
		status, err = driver.terminals.AdoptTracked(ctx, attempt.AttemptID, attempt.TmuxName, state.RunRoot, attempt.Ownership == OwnershipHumanControlled, true)
	}
	if errors.Is(err, terminal.ErrNotFound) {
		return ProbeResult{State: ProbeMissing}, nil
	}
	if err != nil {
		return ProbeResult{State: ProbeAmbiguous}, err
	}
	if status.State == "running" {
		return ProbeResult{State: ProbeRunning}, nil
	}
	if status.ExitCode == nil || status.FinishedAt == nil {
		return ProbeResult{State: ProbeAmbiguous}, nil
	}
	request := AttemptRequest{ProjectID: state.ProjectID, RunID: state.RunID, RunRoot: state.RunRoot, BaseSha: state.BaseSha, Component: attempt.ComponentID, Instance: attempt.Instance, Attempt: attempt.Attempt, AttemptID: attempt.AttemptID, SeatID: attempt.SeatID}
	result, collectErr := driver.collect(ctx, request, AttemptResult{ExitCode: *status.ExitCode, Session: contracts.SessionIdentity{ID: attempt.SessionID}, FinishedAt: *status.FinishedAt})
	if collectErr != nil {
		return ProbeResult{}, collectErr
	}
	return ProbeResult{State: ProbeExited, Completion: &result}, nil
}

func (driver *NativeAttemptDriver) Rearm(ctx context.Context, state *RunState, attempt AttemptState) error {
	_, err := driver.terminals.AdoptTracked(ctx, attempt.AttemptID, attempt.TmuxName, state.RunRoot, attempt.Ownership == OwnershipHumanControlled, true)
	if err != nil && !errors.Is(err, terminal.ErrNotFound) {
		return err
	}
	return err
}

func (driver *NativeAttemptDriver) Cleanup(ctx context.Context, state *RunState) error {
	for _, component := range state.Components {
		if component == nil || component.Attempt == nil {
			continue
		}
		if err := driver.terminals.Stop(ctx, component.Attempt.AttemptID); err != nil && !errors.Is(err, terminal.ErrNotFound) {
			return err
		}
	}
	return nil
}

func attemptOutputDirectory(request AttemptRequest) string {
	return path.Join(".fleetlog/run/out", string(request.Component), request.SeatID, strconv.Itoa(request.Attempt))
}

func attemptMetadataDirectory(request AttemptRequest) string {
	return path.Join(".fleetlog/run/attempts", string(request.Component), strconv.Itoa(request.Instance), request.SeatID, strconv.Itoa(request.Attempt))
}

func captureIndexPath(request AttemptRequest, phase string) string {
	return filepath.Join(filepath.Dir(request.RunRoot), "."+request.AttemptID+"-"+phase+".index")
}

func artifactProducer(request AttemptRequest) artifacts.Producer {
	return artifacts.Producer{ComponentID: string(request.Component), Instance: request.Instance, SeatID: request.SeatID, Attempt: request.Attempt}
}

func nextChangeRevision(state *RunState) uint64 {
	if state.Change == nil {
		return 1
	}
	return state.Change.ChangeRevision + 1
}

func uuidV4() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("allocate provider session: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
