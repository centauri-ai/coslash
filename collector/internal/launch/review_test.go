package launch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
			args:  []string{"-p", "--name", name, "--permission-mode", "plan", "--safe-mode", "--strict-mcp-config", "--disable-slash-commands", "--tools", "Read,Glob,Grep"},
			stdin: prompt,
		},
		"codex": {
			bin:   "codex",
			args:  []string{"exec", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--sandbox", "read-only", "--skip-git-repo-check", "-"},
			stdin: prompt,
		},
		"opencode": {
			bin:   "opencode",
			args:  []string{"run", "--pure", "--title", name},
			env:   []string{`OPENCODE_PERMISSION={"edit":"deny","bash":{"*":"deny","git diff --no-ext-diff --no-textconv*":"allow","git status*":"allow"}}`},
			stdin: prompt,
		},
		"cursor": func() reviewCommandSpec {
			spec := cursorReviewCommand(prompt)
			spec.args = append(spec.args, "--sandbox", "enabled", "--trust")
			return spec
		}(),
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

func TestReviewResultHelper(t *testing.T) {
	if output := os.Getenv("REVIEW_RESULT_OUTPUT"); output != "" {
		_, _ = os.Stdout.WriteString(output)
		os.Exit(0)
	}
}

func TestCursorReviewUsesPrivateReadOnlyConfig(t *testing.T) {
	t.Setenv("REVIEW_CURSOR_HELPER", "1")
	original := reviewCommandContext
	t.Cleanup(func() { reviewCommandContext = original })
	reviewCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], "-test.run=TestCursorReviewHelper")
	}
	result, err := Review(context.Background(), review.Launch{Reviewer: "cursor", WorkingDirectory: t.TempDir(), Prompt: "review"})
	if err != nil || result != "secure" {
		t.Fatalf("result=%q err=%v", result, err)
	}
}

func TestCursorReviewHelper(t *testing.T) {
	if os.Getenv("REVIEW_CURSOR_HELPER") != "1" {
		return
	}
	path := filepath.Join(os.Getenv("CURSOR_DATA_DIR"), ".cursor", "cli.json")
	contents, err := os.ReadFile(path)
	if err == nil && strings.Contains(string(contents), "Mcp(*)") && strings.Contains(string(contents), "Write(*)") {
		_, _ = os.Stdout.WriteString("secure")
		os.Exit(0)
	}
	os.Exit(1)
}

func TestReviewerOptionsAreCollectedAgents(t *testing.T) {
	got := ReviewerOptions()
	want := []ReviewerOption{
		{ID: "claude", Label: "Claude Code", Executable: "claude"},
		{ID: "codex", Label: "Codex", Executable: "codex"},
		{ID: "opencode", Label: "OpenCode", Executable: "opencode"},
		{ID: "cursor", Label: "Cursor CLI", Executable: "agent"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReviewerOptions() = %#v, want %#v", got, want)
	}
}

func TestReviewCLICommandRejectsUnknownReviewer(t *testing.T) {
	if _, err := reviewCLICommand("invalid", "/repo", "name", "prompt"); err == nil {
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
