package launch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/review"
)

func TestReviewRejectsUnavailableWorkingDirectory(t *testing.T) {
	_, err := Review(context.Background(), review.Launch{
		Reviewer:         "invalid",
		WorkingDirectory: filepath.Join(t.TempDir(), "missing"),
	})
	if !errors.Is(err, ErrWorkingDirectoryUnavailable) {
		t.Fatalf("Review() error = %v", err)
	}
}

func TestReviewCLICommands(t *testing.T) {
	name := "Review — Bob's change (12345678)"
	prompt := "Review Bob's change\nDo not edit."
	tests := map[string]reviewCommandSpec{
		"claude": {
			bin:   "claude",
			args:  []string{"-p", "--name", name, "--permission-mode", "plan"},
			stdin: prompt,
		},
		"codex": {
			bin:   "codex",
			args:  []string{"exec", "--sandbox", "read-only", "--skip-git-repo-check", "-"},
			stdin: prompt,
		},
		"opencode": {
			bin:   "opencode",
			args:  []string{"run", "--title", name},
			env:   []string{`OPENCODE_PERMISSION={"edit":"deny","bash":{"*":"deny","git diff --no-ext-diff --no-textconv*":"allow","git status*":"allow"}}`},
			stdin: prompt,
		},
	}
	for reviewer, want := range tests {
		got, err := reviewCLICommand(reviewer, "/repo", name, prompt)
		if err != nil {
			t.Fatalf("reviewCLICommand(%q): %v", reviewer, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("reviewCLICommand(%q) = %#v, want %#v", reviewer, got, want)
		}
	}
}

func TestBoundedBufferCapsDiagnostics(t *testing.T) {
	buffer := boundedBuffer{limit: 4}
	if written, err := buffer.Write([]byte("secret diagnostic")); err != nil || written != 17 {
		t.Fatalf("Write() = %d, %v", written, err)
	}
	if got := buffer.String(); got != "secr" {
		t.Fatalf("String() = %q", got)
	}
	if !buffer.truncated {
		t.Fatal("buffer did not mark truncated output")
	}
	exact := boundedBuffer{limit: 4}
	_, _ = exact.Write([]byte("four"))
	if exact.truncated {
		t.Fatal("buffer marked exact-size output as truncated")
	}
}

func TestReviewCapturesResult(t *testing.T) {
	t.Setenv("REVIEW_RESULT_OUTPUT", "Found a race")
	original := reviewCommandContext
	t.Cleanup(func() { reviewCommandContext = original })
	reviewCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], "-test.run=TestReviewResultHelper")
	}
	result, err := Review(context.Background(), review.Launch{Reviewer: "codex", WorkingDirectory: t.TempDir()})
	if err != nil || result != "Found a race" {
		t.Fatalf("result=%q err=%v", result, err)
	}
}

func TestReviewRunsRemoteCLIWithoutLocalWorkingDirectory(t *testing.T) {
	t.Setenv("REVIEW_RESULT_OUTPUT", "remote review")
	original := reviewCommandContext
	t.Cleanup(func() { reviewCommandContext = original })
	var binary string
	var arguments []string
	reviewCommandContext = func(ctx context.Context, bin string, args ...string) *exec.Cmd {
		binary, arguments = bin, args
		return exec.CommandContext(ctx, os.Args[0], "-test.run=TestReviewResultHelper")
	}
	result, err := Review(context.Background(), review.Launch{
		Reviewer: "codex", SSHAlias: "agent-box", WorkingDirectory: "/remote/only/worktree",
		Name: "Review — Change (12345678)", Prompt: "review this",
	})
	if err != nil || result != "remote review" {
		t.Fatalf("result=%q err=%v", result, err)
	}
	if binary != "ssh" || !slices.Contains(arguments, "-T") || slices.Contains(arguments, "-tt") ||
		!strings.Contains(arguments[len(arguments)-1], "cd '/remote/only/worktree' || exit 1;") ||
		!strings.Contains(arguments[len(arguments)-1], "'codex' 'exec' '--sandbox' 'read-only'") ||
		!strings.Contains(arguments[len(arguments)-1], `trap 'rm -f "$marker"' EXIT`) {
		t.Fatalf("remote command = %q %q", binary, arguments)
	}
}

func TestRemoteReviewFailureAttemptsRemoteCleanup(t *testing.T) {
	t.Setenv("REVIEW_FAILURE_HELPER", "1")
	t.Setenv("REVIEW_RESULT_OUTPUT", "cleanup")
	original := reviewCommandContext
	t.Cleanup(func() { reviewCommandContext = original })
	var commands []string
	reviewCommandContext = func(ctx context.Context, bin string, args ...string) *exec.Cmd {
		if bin != "ssh" {
			t.Fatalf("binary = %q", bin)
		}
		commands = append(commands, args[len(args)-1])
		if len(commands) == 1 {
			return exec.CommandContext(ctx, os.Args[0], "-test.run=TestReviewFailureHelper")
		}
		return exec.CommandContext(ctx, os.Args[0], "-test.run=TestReviewResultHelper")
	}
	_, err := Review(context.Background(), review.Launch{
		Reviewer: "codex", SSHAlias: "agent-box", WorkingDirectory: "/remote/repo",
	})
	if err == nil || len(commands) != 2 || !strings.Contains(commands[1], `kill -TERM "$pid"`) {
		t.Fatalf("review error = %v, commands = %q", err, commands)
	}
}

func TestReviewFailureHelper(t *testing.T) {
	if os.Getenv("REVIEW_FAILURE_HELPER") == "1" {
		os.Exit(1)
	}
}

func TestRemoteReviewerOptionsExcludeOpenCode(t *testing.T) {
	t.Setenv("REVIEW_RESULT_OUTPUT", "claude\ncodex\nopencode\n")
	original := reviewCommandContext
	t.Cleanup(func() { reviewCommandContext = original })
	reviewCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], "-test.run=TestReviewResultHelper")
	}
	options, err := RemoteReviewerOptions(context.Background(), "agent-box")
	if err != nil || !reflect.DeepEqual(options, []ReviewerOption{
		{ID: "claude", Label: "Claude Code CLI", Executable: "claude"},
		{ID: "codex", Label: "Codex CLI", Executable: "codex"},
	}) {
		t.Fatalf("options = %#v, %v", options, err)
	}
}

func TestReviewResultHelper(t *testing.T) {
	if output := os.Getenv("REVIEW_RESULT_OUTPUT"); output != "" {
		_, _ = os.Stdout.WriteString(output)
		os.Exit(0)
	}
}

func TestReviewerOptionsAreCollectedAgents(t *testing.T) {
	got := ReviewerOptions()
	want := []ReviewerOption{
		{ID: "claude", Label: "Claude Code CLI", Executable: "claude"},
		{ID: "codex", Label: "Codex CLI", Executable: "codex"},
		{ID: "opencode", Label: "OpenCode CLI", Executable: "opencode"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReviewerOptions() = %#v, want %#v", got, want)
	}
}

func TestReviewCLICommandRejectsUnknownReviewer(t *testing.T) {
	if _, err := reviewCLICommand("cursor", "/repo", "name", "prompt"); err == nil {
		t.Fatal("reviewCLICommand() accepted an unsupported reviewer")
	}
}

func TestReviewSetsPWDToWorkingDirectory(t *testing.T) {
	workingDirectory := t.TempDir()
	output := filepath.Join(t.TempDir(), "pwd")
	t.Setenv("REVIEW_PWD_OUTPUT", output)
	original := reviewCommandContext
	t.Cleanup(func() { reviewCommandContext = original })
	reviewCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], "-test.run=TestReviewWorkingDirectoryHelper")
	}

	if _, err := Review(context.Background(), review.Launch{Reviewer: "opencode", WorkingDirectory: workingDirectory}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != workingDirectory {
		t.Fatalf("PWD = %q, want %q", got, workingDirectory)
	}
}

func TestReviewWorkingDirectoryHelper(t *testing.T) {
	output := os.Getenv("REVIEW_PWD_OUTPUT")
	if output == "" {
		return
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(output, []byte(workingDirectory), 0o600); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
