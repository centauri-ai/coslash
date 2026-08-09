package sessioncanvas

import (
	"context"
	"errors"
	"net/http"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/sessiondetail"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/terminal"
	"github.com/centauri-ai/coslash/collector/internal/session"
)

var (
	ErrSessionNotFound   = errors.New("session canvas: session not found")
	ErrSessionAmbiguous  = errors.New("session canvas: session identity is ambiguous")
	ErrRenameUnsupported = errors.New("session canvas: rename is unsupported")
	ErrAnalysisDisabled  = errors.New("session canvas: turn analysis is disabled")
	ErrAnalysisFailed    = errors.New("session canvas: turn analysis failed")
)

type ResolvedSession struct {
	Session        session.Session
	TranscriptPath string
}

type SessionResolver interface {
	Resolve(context.Context, contracts.SessionIdentity) (ResolvedSession, error)
}

type DetailProjector interface {
	ProjectKnown(context.Context, session.Session, string) (*sessiondetail.Detail, error)
}

type Renamer interface {
	Rename(context.Context, contracts.SessionIdentity, string) error
}

type WorkspaceRegistrar interface {
	Register(*http.ServeMux)
}

type TerminalService interface {
	Create(context.Context, terminal.Spec) (contracts.TerminalStatus, error)
	Adopt(context.Context, string, string, string, bool, bool) (contracts.TerminalStatus, error)
	Status(context.Context, string) (contracts.TerminalStatus, error)
	Stop(context.Context, string) error
}

type TerminalAPI interface {
	Status(http.ResponseWriter, *http.Request, string)
	Input(http.ResponseWriter, *http.Request, string)
	Stop(http.ResponseWriter, *http.Request, string)
	WebSocket(http.ResponseWriter, *http.Request, string)
}

type TurnAnalyzer interface {
	Analyze(context.Context, TurnAnalysisInput) (TurnAnalysis, error)
}

type TurnAnalysisInput struct {
	Session contracts.SessionIdentity
	Turn    sessiondetail.Turn
	Detail  *sessiondetail.Detail
}

type TurnAnalysis struct {
	Intention   string   `json:"intention"`
	PlanSummary string   `json:"planSummary"`
	Status      string   `json:"status"`
	Findings    []string `json:"findings"`
	Issues      []string `json:"issues"`
}

type Options struct {
	Sessions       SessionResolver
	Projector      DetailProjector
	Renamer        Renamer
	Workspaces     WorkspaceRegistrar
	Terminals      TerminalService
	TerminalAPI    TerminalAPI
	Analyzer       TurnAnalyzer
	NewUUID        func() (string, error)
	AnalysisCache  int
	MaxFileBytes   int64
	MaxPromptBytes int
}

type actionRequest struct {
	Prompt     string `json:"prompt,omitempty"`
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
	Permission string `json:"permission,omitempty"`
	Writable   *bool  `json:"writable,omitempty"`
}

type terminalResponse struct {
	OK             bool                     `json:"ok"`
	Reused         bool                     `json:"reused,omitempty"`
	Terminal       contracts.TerminalStatus `json:"terminal"`
	ChildSessionID *string                  `json:"childSessionId,omitempty"`
}

type analysisResponse struct {
	OK       bool         `json:"ok"`
	CacheKey string       `json:"cacheKey"`
	Cached   bool         `json:"cached"`
	Analysis TurnAnalysis `json:"analysis"`
}
