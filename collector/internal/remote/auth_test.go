package remote

import (
	"context"
	"errors"
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
	id, err := CreateAuthAttempt("agent-box")
	if err != nil {
		t.Fatal(err)
	}
	if err := CancelAuthAttempt(id); err != nil {
		t.Fatal(err)
	}
	if state, err := AuthAttemptState(context.Background(), id); err != nil || state != AuthCancelled {
		t.Fatalf("cancelled state = %q, %v", state, err)
	}
	if err := CancelAuthAttempt("not-an-id"); err == nil {
		t.Fatal("invalid ID accepted")
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
			_, err := CreateAuthAttempt("agent-box")
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
	id, err := CreateAuthAttempt("agent-box")
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
	id, err = CreateAuthAttempt("agent-box")
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

func TestRunAuthAttemptClosesMasterAfterCancellation(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	originalRun := runInteractiveSSH
	originalExit := exitAuthControlMaster
	t.Cleanup(func() {
		runInteractiveSSH = originalRun
		exitAuthControlMaster = originalExit
	})
	id, err := CreateAuthAttempt("agent-box")
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan string, 2)
	exitAuthControlMaster = func(destination string) { closed <- destination }
	runInteractiveSSH = func(context.Context, []string) error {
		if err := CancelAuthAttempt(id); err != nil {
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
	id, err := CreateAuthAttempt("agent-box")
	if err != nil {
		t.Fatal(err)
	}
	exited := false
	exitAuthControlMaster = func(string) { exited = true }
	runInteractiveSSH = func(context.Context, []string) error {
		_, err := updateAuthAttemptIfWaiting(id, AuthReady)
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
		_, err := CreateAuthAttempt("agent-box")
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
	if _, err := CreateAuthAttempt("agent-box"); err != nil {
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
