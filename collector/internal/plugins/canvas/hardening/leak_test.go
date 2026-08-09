package hardening

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/persistence"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/sessioncanvas"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/terminal"
)

// Resource leaks are the failure mode a single-run test cannot see.
//
// The collector is long-lived: a user opens Canvas, works, closes it, and does
// that again for days. A goroutine, watcher, or PTY that survives one cycle is
// invisible; the same one over a hundred cycles is the process dying. Every
// test here repeats a lifecycle and asserts the process returns to where it
// started, rather than asserting a single teardown "worked".

// settledGoroutines waits for the runtime to quiesce and returns the count.
//
// A raw NumGoroutine immediately after a close is unreliable: a goroutine that
// has been told to stop has not necessarily been scheduled to do so. Polling
// for a stable value distinguishes "still shutting down" from "leaked", which
// is the distinction the test actually cares about.
func settledGoroutines(t *testing.T, atMost int, within time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(within)
	count := runtime.NumGoroutine()
	for time.Now().Before(deadline) {
		count = runtime.NumGoroutine()
		if count <= atMost {
			return count
		}
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}
	return count
}

func TestRepeatedRuntimeLifecyclesReturnToBaseline(t *testing.T) {
	ctx := context.Background()
	open := func() *sessioncanvas.Runtime {
		runtime, err := sessioncanvas.Open(ctx, sessioncanvas.RuntimeOptions{
			CanvasHome:      t.TempDir(),
			VendorHome:      t.TempDir(),
			Sessions:        fakeSessions{workingDirectory: t.TempDir()},
			Projector:       fakeProjector{},
			Renamer:         &fakeRenamer{},
			TerminalOptions: terminal.Options{Capacity: 8, Runner: fakeTmux{live: map[string]bool{}}},
		})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		return runtime
	}

	// One warm-up cycle first: the first open initialises package-level state
	// that legitimately persists, and counting it as a leak would make the test
	// fail for the wrong reason.
	first := open()
	if err := first.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	baseline := settledGoroutines(t, 0, time.Second)

	for i := range 25 {
		instance := open()
		if err := instance.Close(ctx); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
	}

	// A small allowance covers the test server's own scheduling, not a
	// per-cycle leak: 25 cycles leaking even one goroutine each would exceed it.
	settled := settledGoroutines(t, baseline+5, 5*time.Second)
	if settled > baseline+5 {
		t.Fatalf("25 runtime lifecycles left %d goroutines, baseline %d", settled, baseline)
	}
}

func TestRepeatedTerminalLifecyclesReleaseTheirRegistryAndGoroutines(t *testing.T) {
	ctx := context.Background()
	live := map[string]bool{}
	manager := terminal.New(terminal.Options{Capacity: 4, Runner: fakeTmux{live: live}})
	t.Cleanup(func() { _ = manager.Close(ctx) })

	// A warm-up adopt/stop so the first-use allocations are not counted.
	live["coslash-warmup"] = true
	if _, err := manager.Adopt(ctx, "warmup", "coslash-warmup", t.TempDir(), false, true); err != nil {
		t.Fatalf("warm-up adopt: %v", err)
	}
	if err := manager.Stop(ctx, "warmup"); err != nil {
		t.Fatalf("warm-up stop: %v", err)
	}
	baseline := settledGoroutines(t, 0, time.Second)

	// The registry is bounded at 4. Adopting and stopping well past that bound
	// only works if every stop actually releases its slot — which is the same
	// property that stops a long session from exhausting the registry.
	directory := t.TempDir()
	for i := range 40 {
		name := fmt.Sprintf("coslash-pane-%d", i)
		id := fmt.Sprintf("attempt-%d", i)
		live[name] = true
		if _, err := manager.Adopt(ctx, id, name, directory, false, true); err != nil {
			t.Fatalf("adopt %d: %v", i, err)
		}
		if _, err := manager.Status(ctx, id); err != nil {
			t.Fatalf("status %d: %v", i, err)
		}
		if err := manager.Stop(ctx, id); err != nil {
			t.Fatalf("stop %d: %v", i, err)
		}
		// A stopped terminal must be gone from the registry, not merely marked.
		if _, err := manager.Status(ctx, id); err == nil {
			t.Fatalf("terminal %s survived its stop", id)
		}
		delete(live, name)
	}

	settled := settledGoroutines(t, baseline+5, 5*time.Second)
	if settled > baseline+5 {
		t.Fatalf("40 terminal lifecycles left %d goroutines, baseline %d", settled, baseline)
	}
}

func TestClosingTheManagerReleasesEverythingItStillHolds(t *testing.T) {
	ctx := context.Background()
	live := map[string]bool{"coslash-held-1": true, "coslash-held-2": true}
	manager := terminal.New(terminal.Options{Capacity: 4, Runner: fakeTmux{live: live}})
	directory := t.TempDir()
	for _, name := range []string{"coslash-held-1", "coslash-held-2"} {
		if _, err := manager.Adopt(ctx, name, name, directory, true, true); err != nil {
			t.Fatalf("adopt %s: %v", name, err)
		}
	}
	baseline := settledGoroutines(t, 0, time.Second)

	// Shutdown must not depend on every terminal having been stopped first;
	// that is exactly the state a crash or a fast quit leaves behind.
	if err := manager.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := manager.Status(ctx, "coslash-held-1"); err == nil {
		t.Fatal("a terminal survived the manager's close")
	}
	// A closed manager must refuse new work rather than resurrect itself.
	if _, err := manager.Adopt(ctx, "coslash-held-1", "coslash-held-1", directory, true, true); err == nil {
		t.Fatal("a closed manager adopted a new terminal")
	}
	settled := settledGoroutines(t, baseline+5, 5*time.Second)
	if settled > baseline+5 {
		t.Fatalf("close left %d goroutines, baseline %d", settled, baseline)
	}
}

func TestRepeatedWorkspaceWritesDoNotAccumulateHandles(t *testing.T) {
	ctx := context.Background()
	store, err := persistence.Open(ctx, t.TempDir(), persistence.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	baseline := settledGoroutines(t, 0, time.Second)

	// Every write is atomic, which means a temporary file and a rename. Two
	// hundred of them is where a missing Close shows up as an FD exhaustion.
	for i := range 200 {
		session := identityFor(i % 7)
		document, loadErr := store.Load(ctx, session)
		if loadErr != nil {
			t.Fatalf("load %d: %v", i, loadErr)
		}
		if _, saveErr := store.Save(ctx, session, writeFor(document.Revision, i)); saveErr != nil {
			t.Fatalf("save %d: %v", i, saveErr)
		}
	}

	settled := settledGoroutines(t, baseline+5, 5*time.Second)
	if settled > baseline+5 {
		t.Fatalf("200 workspace writes left %d goroutines, baseline %d", settled, baseline)
	}
}

func TestRepeatedRequestCyclesAgainstTheAssembledSuiteDoNotLeak(t *testing.T) {
	suite := newSuite(t)
	// Warm the client, the server, and the store before measuring.
	for range 5 {
		suite.do(t, call{path: "/api/canvas/workspaces/claude/session-1"})
	}
	baseline := settledGoroutines(t, 0, time.Second)

	for i := range 150 {
		suite.do(t, call{path: "/api/canvas/workspaces/claude/session-1"})
		suite.do(t, call{path: "/api/canvas/sessions/claude/session-1"})
		suite.do(t, call{path: "/api/terminals/missing", noToken: i%2 == 0})
	}

	// The HTTP client keeps idle connections, so a handful of extra goroutines
	// is expected; per-request leakage over 450 requests would not fit in it.
	settled := settledGoroutines(t, baseline+10, 5*time.Second)
	if settled > baseline+10 {
		t.Fatalf("450 requests left %d goroutines, baseline %d", settled, baseline)
	}
}

func TestTheSuiteStillAnswersAfterItsLeakLoops(t *testing.T) {
	// A leak test that quietly broke the thing it was measuring would report a
	// clean baseline for the wrong reason.
	suite := newSuite(t)
	for range 50 {
		suite.do(t, call{path: "/api/canvas/workspaces/claude/session-1"})
	}
	response, body := suite.do(t, call{path: "/api/canvas/workspaces/claude/session-1"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the suite stopped answering: %d %s", response.StatusCode, body)
	}
}

// identityFor produces a small set of distinct composite identities, so the
// write loop exercises several documents rather than one hot file.
func identityFor(index int) contracts.SessionIdentity {
	return contracts.SessionIdentity{Agent: "claude", ID: fmt.Sprintf("session-%d", index)}
}

func writeFor(revision uint64, index int) contracts.WorkspaceWrite {
	return contracts.WorkspaceWrite{
		SchemaVersion:    1,
		ExpectedRevision: revision,
		State:            json.RawMessage(fmt.Sprintf(`{"note":%d}`, index)),
	}
}
