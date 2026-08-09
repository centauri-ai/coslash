package hardening

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/sessioncanvas"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/terminal"
)

// Restart integrity at the assembled level.
//
// Per-component restart behavior is owned and covered where it lives: the
// DaGama controller proves that reconciliation never relaunches an ambiguous
// attempt and drains an exited one exactly once, the terminal manager proves
// tracked adoption of a preserved pane, and the persistence store proves a
// document survives a reopen. What none of them can show is the property a
// user actually experiences — that closing the collector and starting it again
// on the same home returns the same workspace through the same route.

func TestWorkspaceStateSurvivesACollectorRestart(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	workingDirectory := t.TempDir()

	open := func() *sessioncanvas.Runtime {
		runtime, err := sessioncanvas.Open(ctx, sessioncanvas.RuntimeOptions{
			CanvasHome:      home,
			VendorHome:      t.TempDir(),
			Sessions:        fakeSessions{workingDirectory: workingDirectory},
			Projector:       fakeProjector{},
			Renamer:         &fakeRenamer{},
			TerminalOptions: terminal.Options{Capacity: 4, Runner: fakeTmux{live: map[string]bool{}}},
		})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		return runtime
	}

	first := newSuiteAround(t, open())
	response, body := first.do(t, call{
		method: http.MethodPut,
		path:   "/api/canvas/workspaces/claude/session-1",
		body:   `{"schemaVersion":1,"expectedRevision":0,"state":{"note":"before restart"}}`,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("write answered %d: %s", response.StatusCode, body)
	}
	first.close(t)

	// A fresh process against the same home.
	second := newSuiteAround(t, open())
	response, body = second.do(t, call{path: "/api/canvas/workspaces/claude/session-1"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("read after restart answered %d: %s", response.StatusCode, body)
	}
	if !strings.Contains(string(body), "before restart") {
		t.Fatalf("the workspace did not survive the restart: %s", body)
	}
	// The revision has to survive too, or the first write after a restart would
	// look like a conflict to a tab that never reloaded.
	if !strings.Contains(string(body), `"revision":1`) {
		t.Fatalf("the revision did not survive the restart: %s", body)
	}
}

func TestARestartDoesNotResurrectATerminalWhosePaneIsGone(t *testing.T) {
	ctx := context.Background()
	live := map[string]bool{}
	// The pane is not live, which is what a restart after a crash looks like:
	// the registry entry is gone and tmux has nothing to adopt. Reattaching
	// anyway would be how a run gets a second, unrelated agent turn.
	manager := terminal.New(terminal.Options{Capacity: 4, Runner: fakeTmux{live: live}})
	t.Cleanup(func() { _ = manager.Close(ctx) })

	if _, err := manager.Adopt(ctx, "attempt-1", "coslash-gone", t.TempDir(), true, true); err == nil {
		t.Fatal("a terminal was adopted for a pane that does not exist")
	}
	if _, err := manager.Status(ctx, "attempt-1"); err == nil {
		t.Fatal("a failed adopt left a registry entry behind")
	}
}

// newSuiteAround wraps an already-open runtime in the guarded test server, so a
// restart test can control the runtime's lifetime itself.
func newSuiteAround(t *testing.T, runtime *sessioncanvas.Runtime) *suite {
	t.Helper()
	return serve(t, runtime)
}

func (s *suite) close(t *testing.T) {
	t.Helper()
	s.server.CloseClientConnections()
	s.server.Close()
	if err := s.runtime.Close(context.Background()); err != nil {
		t.Fatalf("close runtime: %v", err)
	}
}
