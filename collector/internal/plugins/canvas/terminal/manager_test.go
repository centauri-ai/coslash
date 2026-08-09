package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/agentexec"
)

type runnerCall struct {
	name  string
	args  []string
	stdin string
}

type fakeRunner struct {
	mu       sync.Mutex
	sessions map[string]bool
	panes    map[string]*fakePane
	nextPane int
	calls    []runnerCall
}

type fakePane struct {
	id       string
	session  string
	dead     bool
	exitCode int
	finished int64
	capture  string
}

type failingRunner struct{}

func (failingRunner) Run(context.Context, io.Reader, string, []string, string, []string) error {
	return errors.New("secret command output")
}

func (failingRunner) Output(context.Context, string, []string, string, []string, int64) ([]byte, error) {
	return nil, errors.New("secret command output")
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{sessions: map[string]bool{}, panes: map[string]*fakePane{}}
}

func (runner *fakeRunner) Run(_ context.Context, stdin io.Reader, name string, args []string, _ string, _ []string) error {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	input := ""
	if stdin != nil {
		data, _ := io.ReadAll(stdin)
		input = string(data)
	}
	runner.calls = append(runner.calls, runnerCall{name: name, args: slices.Clone(args), stdin: input})
	if name != "tmux" || len(args) == 0 {
		return errors.New("unexpected command")
	}
	switch args[0] {
	case "has-session":
		name := strings.TrimPrefix(args[len(args)-1], "=")
		if !runner.sessions[name] {
			return errors.New("missing")
		}
	case "new-session":
		_, err := runner.createSession(args)
		return err
	case "respawn-pane":
		pane := runner.paneTarget(args)
		if pane == nil {
			return errors.New("missing pane")
		}
		pane.dead = false
		pane.exitCode = 0
		pane.finished = 0
	case "kill-session":
		name := strings.TrimPrefix(args[len(args)-1], "=")
		delete(runner.sessions, name)
		for id, pane := range runner.panes {
			if pane.session == name {
				delete(runner.panes, id)
			}
		}
	}
	return nil
}

func (runner *fakeRunner) Output(_ context.Context, name string, args []string, _ string, _ []string, limit int64) ([]byte, error) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.calls = append(runner.calls, runnerCall{name: name, args: slices.Clone(args)})
	if name != "tmux" || len(args) == 0 || limit <= 0 {
		return nil, errors.New("unexpected command")
	}
	var output string
	switch args[0] {
	case "new-session":
		pane, err := runner.createSession(args)
		if err != nil {
			return nil, err
		}
		output = pane.id + "\n"
	case "list-panes":
		name := strings.TrimPrefix(args[slices.Index(args, "-t")+1], "=")
		for _, pane := range runner.panes {
			if pane.session == name {
				output += pane.id + "\n"
			}
		}
		if output == "" {
			return nil, errors.New("missing")
		}
	case "display-message":
		pane := runner.paneTarget(args)
		if pane == nil {
			return nil, errors.New("missing")
		}
		if pane.dead {
			output = fmt.Sprintf("1|%d|%d\n", pane.exitCode, pane.finished)
		} else {
			output = "0||\n"
		}
	case "capture-pane":
		pane := runner.paneTarget(args)
		if pane == nil {
			return nil, errors.New("missing")
		}
		output = pane.capture
	default:
		return nil, errors.New("unexpected output command")
	}
	if int64(len(output)) > limit {
		return nil, errors.New("limit")
	}
	return []byte(output), nil
}

func (runner *fakeRunner) createSession(args []string) (*fakePane, error) {
	index := slices.Index(args, "-s")
	if index < 0 || index+1 >= len(args) {
		return nil, errors.New("missing name")
	}
	name := args[index+1]
	if runner.sessions[name] {
		return nil, errors.New("duplicate")
	}
	runner.nextPane++
	pane := &fakePane{id: fmt.Sprintf("%%%d", runner.nextPane), session: name}
	runner.sessions[name] = true
	runner.panes[pane.id] = pane
	return pane, nil
}

func (runner *fakeRunner) paneTarget(args []string) *fakePane {
	index := slices.Index(args, "-t")
	if index < 0 || index+1 >= len(args) {
		return nil
	}
	return runner.panes[args[index+1]]
}

func (runner *fakeRunner) finish(session string, exitCode int, finished time.Time) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	for _, pane := range runner.panes {
		if pane.session == session {
			pane.dead = true
			pane.exitCode = exitCode
			pane.finished = finished.Unix()
		}
	}
}

func (runner *fakeRunner) setCapture(session, output string) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	for _, pane := range runner.panes {
		if pane.session == session {
			pane.capture = output
		}
	}
}

func (runner *fakeRunner) snapshot() []runnerCall {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return slices.Clone(runner.calls)
}

type fakePTY struct {
	mu      sync.Mutex
	reads   chan []byte
	writes  chan []byte
	resizes chan [2]uint16
	closed  chan struct{}
	once    sync.Once
}

func newFakePTY() *fakePTY {
	return &fakePTY{reads: make(chan []byte, 8), writes: make(chan []byte, 8), resizes: make(chan [2]uint16, 8), closed: make(chan struct{})}
}

func (pty *fakePTY) Read(target []byte) (int, error) {
	select {
	case data := <-pty.reads:
		return copy(target, data), nil
	case <-pty.closed:
		return 0, io.EOF
	}
}

func (pty *fakePTY) Write(data []byte) (int, error) {
	select {
	case <-pty.closed:
		return 0, io.ErrClosedPipe
	default:
	}
	pty.writes <- slices.Clone(data)
	return len(data), nil
}

func (pty *fakePTY) Resize(cols, rows uint16) error {
	pty.resizes <- [2]uint16{cols, rows}
	return nil
}

func (pty *fakePTY) Close() error {
	pty.once.Do(func() { close(pty.closed) })
	return nil
}

type fakePTYFactory struct {
	created chan *fakePTY
}

func (factory *fakePTYFactory) Start(_ context.Context, name string, args []string, _ string, _ []string, cols, rows uint16) (PTY, error) {
	if name != "tmux" || !reflect.DeepEqual(args[:2], []string{"attach-session", "-t"}) || cols == 0 || rows == 0 {
		return nil, errors.New("bad PTY command")
	}
	created := newFakePTY()
	factory.created <- created
	return created, nil
}

func testCommand(t *testing.T, prompt string) agentexec.Command {
	t.Helper()
	return agentexec.Command{Path: "codex", Args: []string{"--model", "gpt-5.6-sol", prompt}, Dir: t.TempDir(), Env: []string{"PATH=/usr/bin", "TERM=xterm-256color"}}
}

func TestNameIsDeterministicAndCollisionResistant(t *testing.T) {
	first, err := Name("session", "a/b")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := Name("session", "a_b")
	again, _ := Name("session", "a/b")
	if first == second || first != again || !tmuxNamePattern.MatchString(first) {
		t.Fatalf("names = %q %q %q", first, second, again)
	}
	if _, err := Name("../bad", "id"); err == nil {
		t.Fatal("unsafe namespace accepted")
	}
}

func TestCreateUsesDirectTmuxArgvAndBoundsRegistry(t *testing.T) {
	runner := newFakeRunner()
	manager := New(Options{Capacity: 1, Runner: runner, PTYFactory: &fakePTYFactory{created: make(chan *fakePTY, 1)}})
	name, _ := Name("session", "one")
	prompt := "; touch /tmp/not-created"
	status, err := manager.Create(context.Background(), Spec{ID: "term-1", TmuxName: name, Command: testCommand(t, prompt), Writable: true})
	if err != nil {
		t.Fatal(err)
	}
	if status.TerminalID != "term-1" || !status.Writable {
		t.Fatalf("status = %#v", status)
	}
	var creation []string
	for _, call := range runner.snapshot() {
		if len(call.args) > 0 && call.args[0] == "new-session" {
			creation = call.args
		}
	}
	if len(creation) == 0 || !slices.Contains(creation, "--") || !slices.Contains(creation, prompt) {
		t.Fatalf("creation argv = %#v", creation)
	}
	separator := slices.Index(creation, "--")
	if separator < 0 || separator+1 >= len(creation) || creation[separator+1] != "codex" {
		t.Fatalf("shell reached creation argv: %#v", creation)
	}
	name2, _ := Name("session", "two")
	if _, err := manager.Create(context.Background(), Spec{ID: "term-2", TmuxName: name2, Command: testCommand(t, "")}); err == nil {
		t.Fatal("capacity was not enforced")
	}
}

func TestCreateReportsMissingTmuxWithoutLeakingCommandOutput(t *testing.T) {
	manager := New(Options{Runner: failingRunner{}})
	name, _ := Name("session", "missing")
	_, err := manager.Create(context.Background(), Spec{ID: "missing", TmuxName: name, Command: testCommand(t, "")})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe error = %v", err)
	}
}

func TestAttachEnforcesWritableResizeAndCleanup(t *testing.T) {
	runner := newFakeRunner()
	factory := &fakePTYFactory{created: make(chan *fakePTY, 2)}
	manager := New(Options{Runner: runner, PTYFactory: factory})
	name, _ := Name("session", "read-only")
	if _, err := manager.Create(context.Background(), Spec{ID: "readonly", TmuxName: name, Command: testCommand(t, ""), Writable: false}); err != nil {
		t.Fatal(err)
	}
	client, err := manager.Attach(context.Background(), "readonly", 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	pty := <-factory.created
	if _, err := client.Write([]byte("no")); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("write error = %v", err)
	}
	if err := client.Resize(10, 40); err == nil {
		t.Fatal("small resize accepted")
	}
	if err := client.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	if got := <-pty.resizes; got != [2]uint16{100, 30} {
		t.Fatalf("resize = %#v", got)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-pty.closed:
	default:
		t.Fatal("PTY was not closed")
	}
}

func TestPasteUsesBracketedPasteWithoutShell(t *testing.T) {
	runner := newFakeRunner()
	manager := New(Options{Runner: runner})
	name, _ := Name("session", "paste")
	if _, err := manager.Create(context.Background(), Spec{ID: "paste", TmuxName: name, Command: testCommand(t, ""), Writable: true}); err != nil {
		t.Fatal(err)
	}
	note := "line one\n$(touch /tmp/nope)"
	if err := manager.Paste(context.Background(), "paste", note); err != nil {
		t.Fatal(err)
	}
	calls := runner.snapshot()
	load := calls[len(calls)-3]
	paste := calls[len(calls)-2]
	if load.stdin != note || load.args[0] != "load-buffer" || !slices.Contains(paste.args, "-p") {
		t.Fatalf("load=%#v paste=%#v", load, paste)
	}
}

func TestCreateDeliversHeadlessPromptThroughTmuxBuffer(t *testing.T) {
	runner := newFakeRunner()
	manager := New(Options{Runner: runner})
	name, _ := Name("headless", "prompt")
	command := testCommand(t, "")
	command.Stdin = "first line\nsecond line; $(touch /tmp/nope)"
	if _, err := manager.Create(context.Background(), Spec{ID: "headless", TmuxName: name, Command: command}); err != nil {
		t.Fatal(err)
	}
	var load runnerCall
	var pasted, eof bool
	for _, call := range runner.snapshot() {
		if len(call.args) == 0 {
			continue
		}
		switch call.args[0] {
		case "load-buffer":
			load = call
		case "paste-buffer":
			pasted = !slices.Contains(call.args, "-p")
		case "send-keys":
			eof = call.args[len(call.args)-1] == "C-d"
		}
	}
	if load.stdin != command.Stdin || !pasted || !eof {
		t.Fatalf("load=%#v pasted=%v eof=%v", load, pasted, eof)
	}
}

func TestClosePreservesOnlyAllowedTmuxSessions(t *testing.T) {
	runner := newFakeRunner()
	factory := &fakePTYFactory{created: make(chan *fakePTY, 2)}
	manager := New(Options{Runner: runner, PTYFactory: factory})
	for index, preserve := range []bool{true, false} {
		id := []string{"keep", "kill"}[index]
		name, _ := Name("session", id)
		if _, err := manager.Create(context.Background(), Spec{ID: id, TmuxName: name, Command: testCommand(t, ""), Writable: true, PreserveOnClose: preserve}); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Attach(context.Background(), id, 120, 40); err != nil {
			t.Fatal(err)
		}
		<-factory.created
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	kills := 0
	for _, call := range runner.snapshot() {
		if len(call.args) > 0 && call.args[0] == "kill-session" {
			kills++
			if strings.Contains(strings.Join(call.args, " "), "keep") {
				t.Fatal("preserved session was killed")
			}
		}
	}
	if kills != 1 {
		t.Fatalf("kill count = %d", kills)
	}
}

func TestRepeatedLifecycleReturnsRegistryAndClientsToBaseline(t *testing.T) {
	runner := newFakeRunner()
	factory := &fakePTYFactory{created: make(chan *fakePTY, 32)}
	manager := New(Options{Runner: runner, PTYFactory: factory})
	for index := range 20 {
		id := "cycle-" + strings.Repeat("x", index%3) + string(rune('a'+index))
		name, _ := Name("cycle", id)
		if _, err := manager.Create(context.Background(), Spec{ID: id, TmuxName: name, Command: testCommand(t, ""), Writable: true}); err != nil {
			t.Fatal(err)
		}
		client, err := manager.Attach(context.Background(), id, 120, 40)
		if err != nil {
			t.Fatal(err)
		}
		<-factory.created
		_ = client.Close()
		if err := manager.Stop(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.sessions) != 0 {
		t.Fatalf("registry leaked %d sessions", len(manager.sessions))
	}
}

func TestLimitedBufferReportsOverflow(t *testing.T) {
	client := &Client{pty: newFakePTY(), writable: true}
	if _, err := client.Write(make([]byte, MaxInputBytes+1)); err == nil {
		t.Fatal("large input accepted")
	}
}

func TestStopAndDisconnectRaceLeavesNoClientOrSession(t *testing.T) {
	runner := newFakeRunner()
	factory := &fakePTYFactory{created: make(chan *fakePTY, 1)}
	manager := New(Options{Runner: runner, PTYFactory: factory})
	name, _ := Name("race", "stop")
	if _, err := manager.Create(context.Background(), Spec{ID: "stop-race", TmuxName: name, Command: testCommand(t, ""), Writable: true}); err != nil {
		t.Fatal(err)
	}
	client, err := manager.Attach(context.Background(), "stop-race", 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	<-factory.created
	start := make(chan struct{})
	done := make(chan struct{}, 2)
	go func() { <-start; _ = client.Close(); done <- struct{}{} }()
	go func() { <-start; _ = manager.Stop(context.Background(), "stop-race"); done <- struct{}{} }()
	close(start)
	<-done
	<-done
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.sessions) != 0 {
		t.Fatalf("registry leaked after race: %#v", manager.sessions)
	}
}

func TestTrackedLifecycleReportsExactExitAndSupportsRestartAdoption(t *testing.T) {
	runner := newFakeRunner()
	name, _ := Name("dagama", "attempt-one")
	dir := t.TempDir()
	command := testCommand(t, "")
	command.Dir = dir
	first := New(Options{Runner: runner})
	status, err := first.CreateTracked(context.Background(), Spec{ID: "attempt-one", TmuxName: name, Command: command, Writable: true, PreserveOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "running" || status.ExitCode != nil || status.FinishedAt != nil {
		t.Fatalf("running status = %#v", status)
	}
	runner.setCapture(name, "{\"type\":\"thread.started\",\"thread_id\":\"thread-123\"}\n")
	captured, err := first.Capture(context.Background(), "attempt-one")
	if err != nil || !strings.Contains(string(captured), "thread-123") {
		t.Fatalf("capture = %q, err = %v", captured, err)
	}
	if err := first.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	finished := time.Unix(1_786_250_000, 0).UTC()
	runner.finish(name, 17, finished)
	second := New(Options{Runner: runner})
	status, err = second.AdoptTracked(context.Background(), "attempt-one", name, dir, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "exited" || status.ExitCode == nil || *status.ExitCode != 17 || status.FinishedAt == nil || !status.FinishedAt.Equal(finished) {
		t.Fatalf("completion status = %#v", status)
	}
	if err := second.Stop(context.Background(), "attempt-one"); err != nil {
		t.Fatal(err)
	}
	if runner.sessions[name] {
		t.Fatal("tracked tmux session leaked")
	}
}

func TestTrackedLaunchArmsRemainOnExitBeforeDirectRespawn(t *testing.T) {
	runner := newFakeRunner()
	manager := New(Options{Runner: runner})
	name, _ := Name("dagama", "launch-order")
	command := testCommand(t, "")
	command.Stdin = "prompt\n"
	if _, err := manager.CreateTracked(context.Background(), Spec{ID: "launch-order", TmuxName: name, Command: command}); err != nil {
		t.Fatal(err)
	}
	calls := runner.snapshot()
	indices := map[string]int{}
	for index, call := range calls {
		if len(call.args) > 0 {
			if _, exists := indices[call.args[0]]; !exists {
				indices[call.args[0]] = index
			}
		}
	}
	if !(indices["new-session"] < indices["set-option"] && indices["set-option"] < indices["respawn-pane"] && indices["respawn-pane"] < indices["load-buffer"]) {
		t.Fatalf("unsafe launch order: %#v", calls)
	}
	respawn := calls[indices["respawn-pane"]].args
	separator := slices.Index(respawn, "--")
	if separator < 0 || separator+1 >= len(respawn) || respawn[separator+1] != "codex" {
		t.Fatalf("tracked command was not direct argv: %#v", respawn)
	}
}
