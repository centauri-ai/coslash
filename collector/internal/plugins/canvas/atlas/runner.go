package atlas

import (
	"context"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/revision"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/verification"
)

// The controller's boundary to everything that leaves the process.
//
// The interface is deliberately the same shape as DaGama's. The two products
// differ in what they orchestrate — Atlas fans a stage out across a committee,
// DaGama runs one seat — but they need identical things from the outside world,
// and a second, subtly different boundary would mean two places to get process
// cleanup and exit capture wrong.

// PrepareRequest asks for an isolated run root.
type PrepareRequest struct {
	ProjectPath string
	BaseBranch  string
	RunID       string
	RunRoot     string
	Branch      string
	// AllowPlainFolder permits a project that is not a git repository. Atlas
	// supports plain folders, where the run root is a copy rather than a clone
	// and publication is unavailable.
	AllowPlainFolder bool
}

// PreparedRun is the isolated root a run executes in.
type PreparedRun struct {
	Root      revision.RunRoot
	RemoteURL string
}

// AttemptRequest is one agent turn.
//
// SeatID distinguishes a committee worker from the refine turn that follows it,
// which is what keeps sibling outputs attributable after a fan-out.
type AttemptRequest struct {
	ProjectID string
	RunID     string
	RunRoot   string
	BaseSha   string
	Component ComponentID
	Instance  uint64
	Attempt   uint64
	AttemptID string
	SeatID    string
	Seat      Seat
	Prompt    string
	// OutputDirectory is the attempt's own directory beneath the run root. Two
	// committee siblings never share one, so neither can overwrite the other.
	OutputDirectory string
	// Resume carries the provider session a takeover or handback continues.
	Resume *contracts.SessionIdentity
}

// AttemptResult is the exact record of a finished turn.
type AttemptResult struct {
	ExitCode   int
	FinishedAt time.Time
	Session    contracts.SessionIdentity
	// Outputs are the artifact names the attempt actually wrote, discovered by
	// the runtime rather than assumed by the controller.
	Outputs []string
}

// LaunchRecorder binds a provider session to an attempt as soon as the runtime
// learns it, so a crash between launch and exit still leaves a resumable
// identity in the log.
type LaunchRecorder func(contracts.SessionIdentity) error

// VerifyRequest runs the board's checks against a frozen revision.
type VerifyRequest struct {
	RunRoot        string
	Checks         []Check
	ChangeRevision uint64
}

// PublishRequest is everything publication needs about a run.
type PublishRequest struct {
	State  *RunState
	Board  *Board
	Review ReviewFact
	// Verification is the document the publish gate attests.
	Verification verification.Document
	Title        string
	Body         string
}

// ReviewFact is the review outcome reduced to what publication gates on.
type ReviewFact struct {
	Approved       bool
	ChangeRevision uint64
}

// ProbeState is what a restart found of an attempt.
type ProbeState string

const (
	ProbeRunning ProbeState = "running"
	ProbeExited  ProbeState = "exited"
	ProbeMissing ProbeState = "missing"
)

// ProbeResult reports an attempt's fate across a collector restart.
type ProbeResult struct {
	State ProbeState
	// Completion is present only when the exit was recorded exactly.
	Completion *AttemptResult
}

// Runtime is everything the controller cannot do itself.
type Runtime interface {
	Prepare(context.Context, PrepareRequest) (PreparedRun, error)
	RecordControllerArtifact(ctx context.Context, runRoot, name string, contents []byte, producer ArtifactProducer) (ArtifactRecord, error)
	ReadArtifact(ctx context.Context, runRoot string, record ArtifactRecord) ([]byte, error)
	Execute(context.Context, AttemptRequest, LaunchRecorder) (AttemptResult, error)
	Release(context.Context, AttemptState) error
	Verify(context.Context, VerifyRequest) (verification.Document, ArtifactRecord, error)
	Publish(context.Context, PublishRequest) (PublicationRecord, ArtifactRecord, error)
	CaptureChange(ctx context.Context, runRoot string, revisionNumber uint64) (ChangeRecord, error)
	Cancel(context.Context, *RunState) (*ArtifactRecord, error)
	Takeover(context.Context, AttemptRequest, AttemptState) (AttemptResult, error)
	Handback(context.Context, AttemptRequest, AttemptState) (AttemptResult, error)
	Probe(context.Context, *RunState, AttemptState) (ProbeResult, error)
	Rearm(context.Context, *RunState, AttemptState) error
	Cleanup(context.Context, *RunState) error
}
