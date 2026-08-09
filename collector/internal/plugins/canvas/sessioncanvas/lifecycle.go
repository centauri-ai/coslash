package sessioncanvas

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/persistence"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/sessiondetail"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/terminal"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

// RuntimeOptions allows tests and the eventual master-owned plugin wiring to
// replace infrastructure without changing Session Canvas route semantics.
type RuntimeOptions struct {
	CanvasHome         string
	VendorHome         string
	Settings           func() settings.SynthesisSettings
	Sessions           SessionResolver
	Projector          DetailProjector
	Renamer            Renamer
	PersistenceOptions persistence.Options
	TerminalOptions    terminal.Options
	HandlerOptions     Options
}

// Runtime owns the stores and terminal manager consumed by Handler.
type Runtime struct {
	Handler    *Handler
	workspaces *persistence.Store
	terminals  *terminal.Manager
}

func Open(ctx context.Context, options RuntimeOptions) (*Runtime, error) {
	canvasHome := options.CanvasHome
	if canvasHome == "" {
		canvasHome = settings.Home()
	}
	workspaces, err := persistence.Open(ctx, filepath.Join(canvasHome, "canvas"), options.PersistenceOptions)
	if err != nil {
		return nil, err
	}
	manager := terminal.New(options.TerminalOptions)
	sessions := options.Sessions
	if sessions == nil {
		sessions = CollectorResolver{}
	}
	projector := options.Projector
	if projector == nil {
		projector = sessiondetail.New(sessiondetail.Options{})
	}
	renamer := options.Renamer
	if renamer == nil {
		vendorHome := options.VendorHome
		if vendorHome == "" {
			vendorHome, err = os.UserHomeDir()
			if err != nil {
				workspaces.Close()
				return nil, err
			}
		}
		renamer = MetadataRenamer{Home: vendorHome}
	}
	settingsProvider := options.Settings
	if settingsProvider == nil {
		store := settings.Open()
		settingsProvider = func() settings.SynthesisSettings {
			state := store.State()
			if !state.Valid {
				return settings.SynthesisSettings{}
			}
			return state.Config.Synthesis
		}
	}
	handlerOptions := options.HandlerOptions
	handlerOptions.Sessions = sessions
	handlerOptions.Projector = projector
	handlerOptions.Renamer = renamer
	handlerOptions.Workspaces = workspaces
	handlerOptions.Terminals = manager
	handlerOptions.TerminalAPI = terminal.Handler{Manager: manager}
	if handlerOptions.Analyzer == nil {
		handlerOptions.Analyzer = CLIAnalyzer{Config: settingsProvider}
	}
	handler, err := New(handlerOptions)
	if err != nil {
		_ = manager.Close(context.Background())
		_ = workspaces.Close()
		return nil, err
	}
	return &Runtime{Handler: handler, workspaces: workspaces, terminals: manager}, nil
}

func (runtime *Runtime) Register(mux *http.ServeMux) { runtime.Handler.Register(mux) }

func (*Runtime) Start(context.Context) error { return nil }

func (runtime *Runtime) Close(ctx context.Context) error {
	return errors.Join(runtime.terminals.Close(ctx), runtime.workspaces.Close())
}
