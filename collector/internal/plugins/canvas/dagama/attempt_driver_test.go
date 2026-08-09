package dagama

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/revision"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/terminal"
)

type nativeDriverRunner struct {
	mu          sync.Mutex
	session     string
	pane        string
	dead        bool
	exitCode    int
	finished    time.Time
	statusCalls int
	capture     string
	onRespawn   func() error
	calls       [][]string
}

func (runner *nativeDriverRunner) Run(_ context.Context, _ io.Reader, name string, args []string, _ string, _ []string) error {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.calls = append(runner.calls, slices.Clone(args))
	if name != "tmux" || len(args) == 0 {
		return errors.New("unexpected command")
	}
	switch args[0] {
	case "has-session":
		if runner.session == "" {
			return errors.New("missing")
		}
	case "respawn-pane":
		if runner.onRespawn != nil {
			if err := runner.onRespawn(); err != nil {
				return err
			}
		}
	case "kill-session":
		runner.session = ""
		runner.pane = ""
	}
	return nil
}

func (runner *nativeDriverRunner) Output(_ context.Context, name string, args []string, _ string, _ []string, limit int64) ([]byte, error) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.calls = append(runner.calls, slices.Clone(args))
	if name != "tmux" || len(args) == 0 || limit <= 0 {
		return nil, errors.New("unexpected command")
	}
	var output string
	switch args[0] {
	case "new-session":
		index := slices.Index(args, "-s")
		runner.session = args[index+1]
		runner.pane = "%42"
		output = runner.pane + "\n"
	case "display-message":
		runner.statusCalls++
		if runner.dead || runner.statusCalls > 1 {
			runner.dead = true
			output = fmt.Sprintf("1|%d|%d\n", runner.exitCode, runner.finished.Unix())
		} else {
			output = "0||\n"
		}
	case "capture-pane":
		output = runner.capture
	case "list-panes":
		if runner.session == "" {
			return nil, errors.New("missing")
		}
		output = runner.pane + "\n"
	default:
		return nil, errors.New("unexpected output command")
	}
	if int64(len(output)) > limit {
		return nil, errors.New("limit")
	}
	return []byte(output), nil
}

func TestNativeAttemptDriverBindsCodexSessionBeforeExactExitAndPromotesOutput(t *testing.T) {
	runRoot := t.TempDir()
	hooks := t.TempDir()
	git, err := revision.NewGit(revision.NewExecRunner(), hooks)
	if err != nil {
		t.Fatal(err)
	}
	finished := time.Unix(1_786_250_123, 0).UTC()
	runner := &nativeDriverRunner{finished: finished, capture: "{\"type\":\"thread.started\",\"thread_id\":\"thread-123\"}\n"}
	request := AttemptRequest{
		ProjectID: "project-1", RunID: "run-20260809t010203-deadbeef", RunRoot: runRoot,
		Component: ComponentPlan, Instance: 1, Attempt: 1, AttemptID: "attempt-plan-1", SeatID: "plan-1",
		Seat: Seat{Vendor: VendorCodex, Model: "gpt-5.6-sol", Effort: "high", Permission: "workspace-write"}, Prompt: "plan the work",
	}
	outputPath := filepath.Join(runRoot, ".fleetlog", "run", "out", "plan", "plan-1", "1", "PLAN.md")
	runner.onRespawn = func() error {
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
			return err
		}
		return os.WriteFile(outputPath, []byte("safe plan\n"), 0o600)
	}
	driver := NewNativeAttemptDriver(NativeAttemptOptions{
		Terminals: terminal.New(terminal.Options{Runner: runner}), Git: git,
		PollInterval: time.Millisecond, SessionBindTimeout: time.Second,
	})
	launched := false
	result, err := driver.Execute(context.Background(), request, func(session contracts.SessionIdentity) error {
		launched = true
		if session != (contracts.SessionIdentity{Agent: "codex", ID: "thread-123"}) {
			t.Fatalf("launch session = %#v", session)
		}
		if runner.dead {
			t.Fatal("attempt exited before its launch was recorded")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !launched || result.ExitCode != 0 || !result.FinishedAt.Equal(finished) || result.Session.ID != "thread-123" {
		t.Fatalf("result = %#v, launched = %v", result, launched)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].Name != "PLAN.md" || result.Artifacts[0].Producer.SeatID != "plan-1" {
		t.Fatalf("artifacts = %#v", result.Artifacts)
	}
	metadataPrompt := filepath.Join(runRoot, ".fleetlog", "run", "attempts", "plan", "1", "plan-1", "1", "prompt.md")
	contents, err := os.ReadFile(metadataPrompt)
	if err != nil || string(contents) != request.Prompt {
		t.Fatalf("prompt snapshot = %q, err = %v", contents, err)
	}
	for _, call := range runner.calls {
		if len(call) > 0 && call[0] == "respawn-pane" {
			separator := slices.Index(call, "--")
			if separator < 0 || call[separator+1] != "codex" || strings.Contains(strings.Join(call, " "), "sh -c") {
				t.Fatalf("unsafe respawn argv = %#v", call)
			}
		}
	}
}

func TestNativeAttemptDriverKeepsNonzeroExitAsTheAuthoritativeFailure(t *testing.T) {
	runRoot := t.TempDir()
	git, err := revision.NewGit(revision.NewExecRunner(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runner := &nativeDriverRunner{exitCode: 23, finished: time.Unix(1_786_250_123, 0).UTC(), capture: "{\"type\":\"thread.started\",\"thread_id\":\"thread-err\"}\n"}
	driver := NewNativeAttemptDriver(NativeAttemptOptions{Terminals: terminal.New(terminal.Options{Runner: runner}), Git: git, PollInterval: time.Millisecond, SessionBindTimeout: time.Second})
	request := AttemptRequest{RunID: "run-20260809t010203-deadbeef", RunRoot: runRoot, Component: ComponentPlan, Instance: 1, Attempt: 1, AttemptID: "attempt-plan-error", SeatID: "plan-1", Seat: Seat{Vendor: VendorCodex, Model: "gpt-5.6-sol", Effort: "high", Permission: "workspace-write"}}
	result, err := driver.Execute(context.Background(), request, func(contracts.SessionIdentity) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 23 || len(result.Artifacts) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestNativeAttemptDriverCapturesBuildRevisionAndPromotesPatch(t *testing.T) {
	runRoot := t.TempDir()
	runGit(t, runRoot, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(runRoot, "README.md"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, runRoot, "add", "README.md")
	runGit(t, runRoot, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "base")
	baseSha := strings.TrimSpace(runGitOutput(t, runRoot, "rev-parse", "HEAD"))
	git, err := revision.NewGit(revision.NewExecRunner(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	finished := time.Unix(1_786_250_456, 0).UTC()
	runner := &nativeDriverRunner{finished: finished, capture: "{\"type\":\"thread.started\",\"thread_id\":\"thread-build\"}\n"}
	request := AttemptRequest{
		ProjectID: "project-1", RunID: "run-20260809t010203-cafebabe", RunRoot: runRoot, BaseSha: baseSha,
		Component: ComponentBuild, Instance: 1, Attempt: 1, AttemptID: "attempt-build-1", SeatID: "build-1",
		Seat: Seat{Vendor: VendorCodex, Model: "gpt-5.6-sol", Effort: "high", Permission: "workspace-write"}, Prompt: "implement",
	}
	outputPath := filepath.Join(runRoot, ".fleetlog", "run", "out", "build", "build-1", "1", "IMPLEMENTATION.md")
	runner.onRespawn = func() error {
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(outputPath, []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(runRoot, "README.md"), []byte("after\n"), 0o600)
	}
	driver := NewNativeAttemptDriver(NativeAttemptOptions{Terminals: terminal.New(terminal.Options{Runner: runner}), Git: git, PollInterval: time.Millisecond, SessionBindTimeout: time.Second})
	result, err := driver.Execute(context.Background(), request, func(contracts.SessionIdentity) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.Change == nil || result.Change.PatchBytes == 0 || len(result.Change.ChangedFiles) != 1 || result.Change.ChangedFiles[0].Path != "README.md" {
		t.Fatalf("captured revision = %#v", result.Change)
	}
	if len(result.Artifacts) != 2 || result.Artifacts[0].Name != "IMPLEMENTATION.md" || result.Artifacts[1].Name != "CHANGESET.patch" {
		t.Fatalf("artifacts = %#v", result.Artifacts)
	}
	if status := runGitOutput(t, runRoot, "status", "--porcelain"); status != " M README.md\n?? .fleetlog/\n" {
		t.Fatalf("capture mutated the run index: %q", status)
	}
	if staged := runGitOutput(t, runRoot, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("capture staged user files: %q", staged)
	}
}
