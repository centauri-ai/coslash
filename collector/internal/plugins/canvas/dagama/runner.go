package dagama

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/artifacts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/publication"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/revision"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/terminal"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/verification"
)

type PrepareRequest struct {
	ProjectPath string
	BaseBranch  string
	RunID       string
	RunRoot     string
	Branch      string
}

type PreparedRun struct {
	Root      revision.RunRoot
	RemoteURL string
}

type AttemptRequest struct {
	ProjectID string
	RunID     string
	RunRoot   string
	BaseSha   string
	Component ComponentID
	Instance  int
	Attempt   int
	AttemptID string
	SeatID    string
	Seat      Seat
	Prompt    string
	Resume    *contracts.SessionIdentity
}

type AttemptResult struct {
	ExitCode        int
	Session         contracts.SessionIdentity
	Artifacts       []ArtifactRecord
	Change          *revision.CapturedRevision
	ReviewerMutated bool
	FinishedAt      time.Time
}

type VerifyRequest struct {
	ProjectID string
	RunID     string
	RunRoot   string
	Instance  int
	Change    ChangeRecord
	Checks    []Check
}

type PublishRequest struct {
	State        *RunState
	Board        *Board
	Review       ReviewOutcome
	Verification verification.Document
	Title        string
	Body         string
}

type ProbeState string

const (
	ProbeRunning   ProbeState = "running"
	ProbeExited    ProbeState = "exited"
	ProbeMissing   ProbeState = "missing"
	ProbeAmbiguous ProbeState = "ambiguous"
)

type ProbeResult struct {
	State      ProbeState
	Completion *AttemptResult
}

// LaunchRecorder persists the live tmux/session identity before Execute waits
// for completion. Returning an error aborts the attempt so a launch is never
// left running without its durable event.
type LaunchRecorder func(contracts.SessionIdentity) error

type Runtime interface {
	Prepare(context.Context, PrepareRequest) (PreparedRun, error)
	RecordControllerArtifact(context.Context, string, string, []byte, ProducerRef) (ArtifactRecord, error)
	ReadArtifact(context.Context, string, ArtifactRecord) ([]byte, error)
	Execute(context.Context, AttemptRequest, LaunchRecorder) (AttemptResult, error)
	Release(context.Context, AttemptState) error
	Verify(context.Context, VerifyRequest) (verification.Document, ArtifactRecord, error)
	Publish(context.Context, PublishRequest) (publication.Record, ArtifactRecord, error)
	Cancel(context.Context, *RunState) (*ArtifactRecord, error)
	Takeover(context.Context, AttemptRequest, AttemptState) (AttemptResult, error)
	Handback(context.Context, AttemptRequest, AttemptState) (AttemptResult, error)
	Probe(context.Context, *RunState, AttemptState) (ProbeResult, error)
	Rearm(context.Context, *RunState, AttemptState) error
	Cleanup(context.Context, *RunState) error
}

type ProducerRef struct {
	Component ComponentID
	Instance  int
	SeatID    string
	Attempt   int
}

// AttemptDriver is the narrow Task 04 boundary. Implementations own the native
// tmux/PTY-backed agent attempt and its exact exit record; the controller owns
// durable ordering and never infers completion from terminal text.
type AttemptDriver interface {
	Execute(context.Context, AttemptRequest, LaunchRecorder) (AttemptResult, error)
	Release(context.Context, AttemptState) error
	Cancel(context.Context, *RunState) ([]byte, error)
	Takeover(context.Context, AttemptRequest, AttemptState) (AttemptResult, error)
	Handback(context.Context, AttemptRequest, AttemptState) (AttemptResult, error)
	Probe(context.Context, *RunState, AttemptState) (ProbeResult, error)
	Rearm(context.Context, *RunState, AttemptState) error
	Cleanup(context.Context, *RunState) error
}

type ProductionRuntimeOptions struct {
	Git                *revision.Git
	Publisher          *publication.Publisher
	Attempts           AttemptDriver
	Terminals          *terminal.Manager
	VerificationRunner verification.Runner
	Now                func() time.Time
}

// ProductionRuntime composes the approved Task 04/05 services. Tests normally
// use a fake Runtime; production supplies an AttemptDriver backed by Task 04's
// terminal manager and exit-record adapter.
type ProductionRuntime struct {
	git                *revision.Git
	publisher          *publication.Publisher
	attempts           AttemptDriver
	verificationRunner verification.Runner
	now                func() time.Time
}

func NewProductionRuntime(options ProductionRuntimeOptions) (*ProductionRuntime, error) {
	if options.Attempts == nil && options.Terminals != nil && options.Git != nil {
		options.Attempts = NewNativeAttemptDriver(NativeAttemptOptions{Terminals: options.Terminals, Git: options.Git, Now: options.Now})
	}
	if options.Git == nil || options.Publisher == nil || options.Attempts == nil {
		return nil, newError(CodeInvalidState, "the production runtime dependencies are incomplete")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &ProductionRuntime{git: options.Git, publisher: options.Publisher, attempts: options.Attempts, verificationRunner: options.VerificationRunner, now: now}, nil
}

func (r *ProductionRuntime) Prepare(ctx context.Context, request PrepareRequest) (PreparedRun, error) {
	ready, err := r.git.RunPreflight(ctx, revision.PreflightOptions{ProjectPath: request.ProjectPath, BaseBranch: request.BaseBranch})
	if err != nil {
		return PreparedRun{}, err
	}
	root, err := r.git.CreateRunRoot(ctx, revision.CreateRunRootOptions{Preflight: ready, Path: request.RunRoot, Branch: request.Branch})
	if err != nil {
		return PreparedRun{}, err
	}
	return PreparedRun{Root: root, RemoteURL: ready.RemoteURL}, nil
}

func (r *ProductionRuntime) artifactStore(ctx context.Context, runRoot string) (*artifacts.Store, error) {
	scope, err := runfs.OpenScope(runRoot, runfs.ScopeOptions{})
	if err != nil {
		return nil, err
	}
	return artifacts.NewStore(ctx, scope, artifacts.Options{Now: r.now})
}

func (r *ProductionRuntime) RecordControllerArtifact(ctx context.Context, runRoot, name string, contents []byte, producer ProducerRef) (ArtifactRecord, error) {
	store, err := r.artifactStore(ctx, runRoot)
	if err != nil {
		return ArtifactRecord{}, err
	}
	record, err := store.Promote(ctx, contents, artifacts.PromoteOptions{Name: name, Kind: string(producer.Component), Extension: extensionFor(name), Producer: artifacts.Producer{ComponentID: string(producer.Component), Instance: producer.Instance, SeatID: producer.SeatID, Attempt: producer.Attempt}})
	if err != nil {
		return ArtifactRecord{}, err
	}
	return fromArtifactRecord(record), nil
}

func (r *ProductionRuntime) ReadArtifact(ctx context.Context, runRoot string, record ArtifactRecord) ([]byte, error) {
	store, err := r.artifactStore(ctx, runRoot)
	if err != nil {
		return nil, err
	}
	return store.ReadPromoted(ctx, record.Path)
}

func (r *ProductionRuntime) Execute(ctx context.Context, request AttemptRequest, launched LaunchRecorder) (AttemptResult, error) {
	return r.attempts.Execute(ctx, request, launched)
}
func (r *ProductionRuntime) Release(ctx context.Context, attempt AttemptState) error {
	return r.attempts.Release(ctx, attempt)
}

func (r *ProductionRuntime) Verify(ctx context.Context, request VerifyRequest) (verification.Document, ArtifactRecord, error) {
	scope, err := runfs.OpenScope(request.RunRoot, runfs.ScopeOptions{})
	if err != nil {
		return verification.Document{}, ArtifactRecord{}, err
	}
	checks := make([]verification.Check, len(request.Checks))
	for i, check := range request.Checks {
		checks[i] = verification.Check{Name: check.Name, Argv: append([]string(nil), check.Argv...)}
	}
	document, err := verification.Run(ctx, verification.RunOptions{Scope: scope, RunRoot: request.RunRoot, Instance: request.Instance, ChangeRevision: request.Change.ChangeRevision, PatchSha256: request.Change.PatchSha256, Checks: checks, Runner: r.verificationRunner, Now: r.now})
	if err != nil {
		return verification.Document{}, ArtifactRecord{}, err
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return verification.Document{}, ArtifactRecord{}, err
	}
	record, err := r.RecordControllerArtifact(ctx, request.RunRoot, "verification.json", encoded, ProducerRef{Component: ComponentVerify, Instance: request.Instance})
	return document, record, err
}

func (r *ProductionRuntime) Publish(ctx context.Context, request PublishRequest) (publication.Record, ArtifactRecord, error) {
	state := request.State
	if state == nil || state.Change == nil {
		return publication.Record{}, ArtifactRecord{}, newError(CodeInvalidState, "publication requires a current run revision")
	}
	files := make([]revision.ChangedFile, len(state.Change.ChangedFiles))
	for i, file := range state.Change.ChangedFiles {
		files[i] = revision.ChangedFile{Path: file.Path, Status: revision.FileStatus(file.Status)}
	}
	revisionRecord := revision.Revision{ChangeRevision: state.Change.ChangeRevision, TreeOID: state.Change.TreeOID, PatchSha256: state.Change.PatchSha256, PatchBytes: state.Change.PatchBytes, ChangedFiles: files, Insertions: state.Change.Insertions, Deletions: state.Change.Deletions}
	record, err := r.publisher.Execute(ctx, publication.Request{RunID: state.RunID, RunRoot: state.RunRoot, Branch: state.Branch, BaseBranch: state.BaseBranch, BaseSha: state.BaseSha, RemoteURL: state.RemoteURL, Revision: revisionRecord, Review: publication.ReviewFact{Approved: request.Review.Effective == ReviewApproved, ChangeRevision: request.Review.ChangeRevision}, Verification: &request.Verification, Draft: request.Board.Components.Publish.Publish.Draft, CaptureIndexFile: filepath.Join(filepath.Dir(state.RunRoot), "."+state.RunID+"-publish.index")}, request.Title, request.Body)
	if err != nil {
		return publication.Record{}, ArtifactRecord{}, err
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return publication.Record{}, ArtifactRecord{}, err
	}
	artifact, err := r.RecordControllerArtifact(ctx, state.RunRoot, "publication.json", encoded, ProducerRef{Component: ComponentPublish, Instance: 1})
	return record, artifact, err
}

func (r *ProductionRuntime) Cancel(ctx context.Context, state *RunState) (*ArtifactRecord, error) {
	patch, err := r.attempts.Cancel(ctx, state)
	if err != nil {
		return nil, err
	}
	if len(patch) == 0 {
		return nil, nil
	}
	record, err := r.RecordControllerArtifact(ctx, state.RunRoot, "cancel-snapshot.patch", patch, ProducerRef{Component: ComponentBuild, Instance: state.Components[ComponentBuild].Instance})
	if err != nil {
		return nil, err
	}
	return &record, nil
}
func (r *ProductionRuntime) Takeover(ctx context.Context, request AttemptRequest, prior AttemptState) (AttemptResult, error) {
	return r.attempts.Takeover(ctx, request, prior)
}
func (r *ProductionRuntime) Handback(ctx context.Context, request AttemptRequest, attempt AttemptState) (AttemptResult, error) {
	return r.attempts.Handback(ctx, request, attempt)
}
func (r *ProductionRuntime) Probe(ctx context.Context, state *RunState, attempt AttemptState) (ProbeResult, error) {
	return r.attempts.Probe(ctx, state, attempt)
}
func (r *ProductionRuntime) Rearm(ctx context.Context, state *RunState, attempt AttemptState) error {
	return r.attempts.Rearm(ctx, state, attempt)
}
func (r *ProductionRuntime) Cleanup(ctx context.Context, state *RunState) error {
	if err := r.attempts.Cleanup(ctx, state); err != nil {
		return err
	}
	if state.RunRoot == "" {
		return nil
	}
	if err := revision.RemoveRunRoot(state.RunRoot); err != nil {
		return fmt.Errorf("cleanup run root: %w", err)
	}
	return nil
}

func extensionFor(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[i:]
		}
	}
	return ".bin"
}
func fromArtifactRecord(record artifacts.Record) ArtifactRecord {
	return ArtifactRecord{ArtifactID: record.ArtifactID, Kind: record.Kind, Name: record.Name, Path: record.Path, Sha256: record.Sha256, Bytes: record.Bytes, CreatedAt: record.CreatedAt, Producer: ArtifactProducer{ComponentID: ComponentID(record.Producer.ComponentID), Instance: record.Producer.Instance, SeatID: record.Producer.SeatID, Attempt: record.Producer.Attempt}}
}

func publicationRecord(record publication.Record) PublicationRecord {
	return PublicationRecord{
		ChangeRevision: record.ChangeRevision, CommitSha: record.CommitSha, Branch: record.Branch,
		Remote: record.Remote, PRURL: record.PRURL, PRNumber: record.PRNumber, Action: record.Action,
		IdempotencyKey: record.IdempotencyKey, PublishedAt: record.PublishedAt,
	}
}

func changeRecord(captured revision.CapturedRevision, baseSha string) ChangeRecord {
	files := make([]ChangedFileRecord, len(captured.ChangedFiles))
	for index, file := range captured.ChangedFiles {
		files[index] = ChangedFileRecord{Path: file.Path, Status: string(file.Status)}
	}
	return ChangeRecord{
		ChangeRevision: captured.ChangeRevision, TreeOID: captured.TreeOID, PatchSha256: captured.PatchSha256,
		PatchBytes: captured.PatchBytes, Insertions: captured.Insertions, Deletions: captured.Deletions,
		ChangedFiles: files, BaseSha: baseSha,
	}
}
