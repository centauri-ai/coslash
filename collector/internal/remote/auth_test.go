//go:build !windows

package remote

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func TestInteractiveMasterArgsUseStructuredDestination(t *testing.T) {
	args, err := interactiveMasterArgs("jane@devvm1872.cln0")
	if err != nil {
		t.Fatal(err)
	}
	for index := range args {
		if args[index] == "BatchMode=yes" {
			t.Fatal("interactive authentication must not set BatchMode")
		}
	}
	foundBatchModeOverride := false
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "-o" && args[index+1] == "BatchMode=no" {
			foundBatchModeOverride = true
		}
	}
	if !foundBatchModeOverride {
		t.Fatal("interactive authentication must override a BatchMode=yes SSH config")
	}
	found := false
	for index := 0; index+2 < len(args); index++ {
		if args[index] == "-l" && args[index+1] == "jane" && args[index+2] == "devvm1872.cln0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("args = %#v, want structured -l user host", args)
	}
}

func TestAuthAttemptCancelAndInvalidID(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	id, err := CreateAuthAttempt(context.Background(), "agent-box")
	if err != nil {
		t.Fatal(err)
	}
	if state, err := CancelAuthAttempt(context.Background(), id); err != nil || state != AuthCancelled {
		t.Fatalf("CancelAuthAttempt state/error = %q/%v", state, err)
	}
	if state, err := AuthAttemptState(context.Background(), id); err != nil || state != AuthCancelled {
		t.Fatalf("cancelled state = %q, %v", state, err)
	}
	if _, err := CancelAuthAttempt(context.Background(), "not-an-id"); err == nil {
		t.Fatal("invalid ID accepted")
	}
}

func TestCancelAuthAttemptCoordinatesMasterTeardown(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	originalExit := exitAuthControlMaster
	t.Cleanup(func() { exitAuthControlMaster = originalExit })

	id, err := CreateAuthAttempt(context.Background(), "agent-box")
	if err != nil {
		t.Fatal(err)
	}
	exitAuthControlMaster = func(destination string) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		if err := withDestinationCoordinator(ctx, destination, func() error { return nil }); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("teardown coordinator error = %v, want deadline exceeded", err)
		}
	}
	if state, err := CancelAuthAttempt(context.Background(), id); err != nil || state != AuthCancelled {
		t.Fatalf("CancelAuthAttempt state/error = %q/%v", state, err)
	}
}

func TestCancelReadyAuthAttemptKeepsMaster(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	originalExit := exitAuthControlMaster
	t.Cleanup(func() { exitAuthControlMaster = originalExit })

	id, err := CreateAuthAttempt(context.Background(), "agent-box")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updateAuthAttemptIfWaiting(context.Background(), id, AuthReady); err != nil {
		t.Fatal(err)
	}
	exited := false
	exitAuthControlMaster = func(string) { exited = true }
	if state, err := CancelAuthAttempt(context.Background(), id); err != nil || state != AuthReady {
		t.Fatalf("CancelAuthAttempt state/error = %q/%v", state, err)
	}
	if exited {
		t.Fatal("ready authentication master was closed")
	}
}

func TestCreateAuthAttemptAllowsOnlyOneWaitingAttempt(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	const requests = 12
	var group sync.WaitGroup
	results := make(chan error, requests)
	for range requests {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := CreateAuthAttempt(context.Background(), "agent-box")
			results <- err
		}()
	}
	group.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
			continue
		}
		if !errors.Is(err, ErrAuthAttemptActive) {
			t.Fatalf("CreateAuthAttempt error = %v, want ErrAuthAttemptActive", err)
		}
	}
	if winners != 1 {
		t.Fatalf("successful attempts = %d, want 1", winners)
	}
	if !AuthAttemptActive("agent-box") {
		t.Fatal("waiting attempt was not reported active")
	}
}

func TestRunAuthAttemptRecordsTerminalOutcomes(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	original := runInteractiveSSH
	t.Cleanup(func() { runInteractiveSSH = original })

	runInteractiveSSH = func(context.Context, []string) error { return errors.New("bad password") }
	id, err := CreateAuthAttempt(context.Background(), "agent-box")
	if err != nil {
		t.Fatal(err)
	}
	if err := RunAuthAttempt(context.Background(), id); err == nil {
		t.Fatal("RunAuthAttempt succeeded")
	}
	if state, err := AuthAttemptState(context.Background(), id); err != nil || state != AuthFailed {
		t.Fatalf("failure state = %q, %v", state, err)
	}

	runInteractiveSSH = func(ctx context.Context, _ []string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	id, err = CreateAuthAttempt(context.Background(), "agent-box")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RunAuthAttempt(ctx, id); err == nil {
		t.Fatal("cancelled RunAuthAttempt succeeded")
	}
	if state, err := AuthAttemptState(context.Background(), id); err != nil || state != AuthCancelled {
		t.Fatalf("cancelled state = %q, %v", state, err)
	}
}

func TestRunAuthAttemptRejectsCancelledAttemptBeforeSSH(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	originalRun := runInteractiveSSH
	originalExit := exitAuthControlMaster
	t.Cleanup(func() {
		runInteractiveSSH = originalRun
		exitAuthControlMaster = originalExit
	})

	id, err := CreateAuthAttempt(context.Background(), "agent-box")
	if err != nil {
		t.Fatal(err)
	}
	exitAuthControlMaster = func(string) {}
	if _, err := CancelAuthAttempt(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	called := false
	runInteractiveSSH = func(context.Context, []string) error {
		called = true
		return nil
	}
	if err := RunAuthAttempt(context.Background(), id); err == nil {
		t.Fatal("cancelled attempt succeeded")
	}
	if called {
		t.Fatal("interactive SSH ran for a cancelled attempt")
	}
}

func TestRunAuthAttemptStopsSSHWhenAttemptIsCancelled(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	originalRun := runInteractiveSSH
	originalExit := exitAuthControlMaster
	t.Cleanup(func() {
		runInteractiveSSH = originalRun
		exitAuthControlMaster = originalExit
	})

	id, err := CreateAuthAttempt(context.Background(), "agent-box")
	if err != nil {
		t.Fatal(err)
	}
	exitAuthControlMaster = func(string) {}
	started := make(chan struct{})
	runInteractiveSSH = func(ctx context.Context, _ []string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	done := make(chan error, 1)
	go func() { done <- RunAuthAttempt(context.Background(), id) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("interactive SSH did not start")
	}
	if state, err := CancelAuthAttempt(context.Background(), id); err != nil || state != AuthCancelled {
		t.Fatalf("CancelAuthAttempt state/error = %q/%v", state, err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrAuthAttemptCancelled) {
			t.Fatalf("RunAuthAttempt error = %v, want ErrAuthAttemptCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("interactive SSH was not stopped after cancellation")
	}
}

// shortSSHHome stays under the Unix socket path limit. t.TempDir includes the test name.
func shortSSHHome(t *testing.T) {
	t.Helper()
	home, err := os.MkdirTemp("", "csl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("COSLASH_HOME", home)
}

func TestRunAuthAttemptRepairsStaleSocketAndChecksReplacement(t *testing.T) {
	shortSSHHome(t)
	originalRun := runInteractiveSSH
	originalCheck := checkAuthControlMaster
	originalResolve := resolveAuthControlSocketPath
	t.Cleanup(func() {
		runInteractiveSSH = originalRun
		checkAuthControlMaster = originalCheck
		resolveAuthControlSocketPath = originalResolve
	})

	socketPath := filepath.Join(settings.Home(), "ssh", "cm-test")
	resolveAuthControlSocketPath = func(context.Context, string) (string, error) { return socketPath, nil }
	id, err := CreateAuthAttempt(context.Background(), "agent-box")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	// Close unlinks a socket that package net created. A stale control socket remains on disk.
	stale := listener.(*net.UnixListener)
	stale.SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	checks := 0
	checkAuthControlMaster = func(context.Context, string) error {
		checks++
		if checks == 1 {
			return exec.Command("false").Run()
		}
		return nil
	}
	runInteractiveSSH = func(context.Context, []string) error {
		if _, err := os.Lstat(socketPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale socket still exists: %v", err)
		}
		master, err := net.Listen("unix", socketPath)
		if err != nil {
			return err
		}
		return master.Close()
	}
	if err := RunAuthAttempt(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if state, err := AuthAttemptState(context.Background(), id); err != nil || state != AuthReady {
		t.Fatalf("authentication state/error = %q/%v", state, err)
	}
	if checks != 2 {
		t.Fatalf("control checks = %d, want 2", checks)
	}
}

func TestPrepareAuthControlSocketKeepsSocketAfterInconclusiveCheck(t *testing.T) {
	shortSSHHome(t)
	originalCheck := checkAuthControlMaster
	originalResolve := resolveAuthControlSocketPath
	t.Cleanup(func() {
		checkAuthControlMaster = originalCheck
		resolveAuthControlSocketPath = originalResolve
	})

	if err := ensureSSHControlDir(); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(settings.Home(), "ssh", "cm-live")
	master, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = master.Close() }()
	resolveAuthControlSocketPath = func(context.Context, string) (string, error) { return socketPath, nil }
	checkAuthControlMaster = func(context.Context, string) error { return context.DeadlineExceeded }

	ready, err := prepareAuthControlSocket(context.Background(), "agent-box")
	if ready || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("prepareAuthControlSocket ready/error = %v/%v, want false/deadline exceeded", ready, err)
	}
	if _, err := os.Lstat(socketPath); err != nil {
		t.Fatalf("live socket was removed: %v", err)
	}
}

func TestRunAuthAttemptRecordsPreflightFailure(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	originalResolve := resolveAuthControlSocketPath
	t.Cleanup(func() { resolveAuthControlSocketPath = originalResolve })

	resolveAuthControlSocketPath = func(context.Context, string) (string, error) {
		return "", errors.New("SSH configuration failed")
	}
	id, err := CreateAuthAttempt(context.Background(), "agent-box")
	if err != nil {
		t.Fatal(err)
	}
	if err := RunAuthAttempt(context.Background(), id); err == nil {
		t.Fatal("RunAuthAttempt succeeded after preflight failure")
	}
	if state, err := AuthAttemptState(context.Background(), id); err != nil || state != AuthFailed {
		t.Fatalf("preflight failure state/error = %q/%v, want failed", state, err)
	}
}

func TestRunAuthAttemptRejectsMissingControlMaster(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	originalRun := runInteractiveSSH
	originalCheck := checkAuthControlMaster
	originalResolve := resolveAuthControlSocketPath
	t.Cleanup(func() {
		runInteractiveSSH = originalRun
		checkAuthControlMaster = originalCheck
		resolveAuthControlSocketPath = originalResolve
	})

	resolveAuthControlSocketPath = func(context.Context, string) (string, error) {
		return filepath.Join(settings.Home(), "ssh", "cm-missing"), nil
	}
	runInteractiveSSH = func(context.Context, []string) error { return nil }
	checkAuthControlMaster = func(context.Context, string) error { return errors.New("no socket") }
	id, err := CreateAuthAttempt(context.Background(), "agent-box")
	if err != nil {
		t.Fatal(err)
	}
	if err := RunAuthAttempt(context.Background(), id); err == nil {
		t.Fatal("authentication succeeded without a control master")
	}
	if state, err := AuthAttemptState(context.Background(), id); err != nil || state != AuthFailed {
		t.Fatalf("authentication state/error = %q/%v", state, err)
	}
}

func TestRunAuthAttemptClosesMasterAfterCancellation(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	originalRun := runInteractiveSSH
	originalExit := exitAuthControlMaster
	t.Cleanup(func() {
		runInteractiveSSH = originalRun
		exitAuthControlMaster = originalExit
	})
	id, err := CreateAuthAttempt(context.Background(), "agent-box")
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan string, 2)
	exitAuthControlMaster = func(destination string) { closed <- destination }
	runInteractiveSSH = func(context.Context, []string) error {
		if _, err := CancelAuthAttempt(context.Background(), id); err != nil {
			t.Errorf("CancelAuthAttempt: %v", err)
		}
		return nil
	}
	if err := RunAuthAttempt(context.Background(), id); err == nil {
		t.Fatal("cancelled attempt was reported ready")
	}
	for range 2 {
		select {
		case destination := <-closed:
			if destination != "agent-box" {
				t.Fatalf("closed %q, want agent-box", destination)
			}
		default:
			t.Fatal("late master was not closed")
		}
	}
}

func TestRunAuthAttemptKeepsMasterWhenStatusAlreadyMarkedReady(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	originalRun := runInteractiveSSH
	originalExit := exitAuthControlMaster
	t.Cleanup(func() {
		runInteractiveSSH = originalRun
		exitAuthControlMaster = originalExit
	})
	id, err := CreateAuthAttempt(context.Background(), "agent-box")
	if err != nil {
		t.Fatal(err)
	}
	exited := false
	exitAuthControlMaster = func(string) { exited = true }
	runInteractiveSSH = func(context.Context, []string) error {
		_, err := updateAuthAttemptIfWaiting(context.Background(), id, AuthReady)
		return err
	}
	if err := RunAuthAttempt(context.Background(), id); err != nil {
		t.Fatalf("RunAuthAttempt: %v", err)
	}
	if exited {
		t.Fatal("successful master was closed after status marked the attempt ready")
	}
}

func TestDestinationCoordinatorCoversControlMasterStart(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	startedMaster := make(chan struct{})
	var once sync.Once
	command := func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		for _, arg := range args {
			if arg == "ControlMaster=yes" {
				once.Do(func() { close(startedMaster) })
				return exec.CommandContext(ctx, "sleep", "0.12")
			}
		}
		return exec.CommandContext(ctx, "false")
	}
	ensureDone := make(chan error, 1)
	go func() {
		ensureDone <- ensureControlMaster(context.Background(), "agent-box", OpenOptions{command: command})
	}()
	select {
	case <-startedMaster:
	case <-time.After(time.Second):
		t.Fatal("control master did not start")
	}
	created := make(chan error, 1)
	go func() {
		_, err := CreateAuthAttempt(context.Background(), "agent-box")
		created <- err
	}()
	select {
	case err := <-created:
		t.Fatalf("attempt creation raced control-master startup: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := <-ensureDone; err != nil {
		t.Fatalf("ensureControlMaster: %v", err)
	}
	if err := <-created; err != nil {
		t.Fatalf("CreateAuthAttempt: %v", err)
	}
	locks, err := filepath.Glob(filepath.Join(settings.Home(), "ssh", "auth-*.json.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 0 {
		t.Fatalf("per-attempt lock files remain: %v", locks)
	}
}

func TestDestinationCoordinatorHonorsContextWhileWaiting(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	acquired := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- withDestinationCoordinator(context.Background(), "agent-box", func() error {
			close(acquired)
			<-release
			return nil
		})
	}()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("coordinator was not acquired")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	called := false
	err := withDestinationCoordinator(ctx, "agent-box", func() error {
		called = true
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("withDestinationCoordinator error = %v, want deadline exceeded", err)
	}
	if called {
		t.Fatal("callback ran without acquiring the coordinator")
	}
	close(release)
	if err := <-holderDone; err != nil {
		t.Fatalf("holder: %v", err)
	}
}

func TestAuthenticationStatusKeepsChangedHostKeyOutOfTerminalFlow(t *testing.T) {
	changed := WithAuthenticationStatus(Health{Label: "agent-box", Reason: reasonPtr(ReasonHostKeyChanged)})
	if changed.ActionRequired != ActionVerifyHostKey || changed.AuthState != AuthNotRequired {
		t.Fatalf("changed key action/state = %q/%q", changed.ActionRequired, changed.AuthState)
	}
	required := WithAuthenticationStatus(Health{Label: "agent-box", Reason: reasonPtr(ReasonHostKeyConfirmation)})
	if required.ActionRequired != ActionAuthenticate || required.AuthState != AuthRequired {
		t.Fatalf("confirmation action/state = %q/%q", required.ActionRequired, required.AuthState)
	}
}

func TestWaitingAuthenticationSuppressesRefreshAndControlMaster(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	if _, err := CreateAuthAttempt(context.Background(), "agent-box"); err != nil {
		t.Fatal(err)
	}
	if err := ensureControlMaster(context.Background(), "agent-box", OpenOptions{}); !errors.Is(err, ErrAuthAttemptActive) {
		t.Fatalf("ensureControlMaster error = %v, want ErrAuthAttemptActive", err)
	}
	var started int
	manager := NewManager(Options{
		Cache: NewCache(t.TempDir()),
		Refresh: func(context.Context, string, int64, time.Time, CachedSnapshotV2) (refreshOutcome, error) {
			started++
			return refreshOutcome{}, nil
		},
	})
	if err := manager.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "agent-box", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	manager.ListView(0)
	time.Sleep(30 * time.Millisecond)
	if started != 0 {
		t.Fatalf("background refreshes = %d, want 0 while authentication waits", started)
	}
	if _, started := manager.Retry(); started {
		t.Fatal("manual retry reported a refresh while authentication waits")
	}
}
