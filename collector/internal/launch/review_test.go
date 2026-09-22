package launch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/review"
)

func TestReviewRejectsUnavailableWorkingDirectory(t *testing.T) {
	err := Review(context.Background(), review.Launch{
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
}

func TestReviewerOptionsAreCollectedAgents(t *testing.T) {
	got := ReviewerOptions()
	want := []ReviewerOption{
		{ID: "claude", Label: "Claude Code", Executable: "claude"},
		{ID: "codex", Label: "Codex", Executable: "codex"},
		{ID: "opencode", Label: "OpenCode", Executable: "opencode"},
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

	if err := Review(context.Background(), review.Launch{Reviewer: "opencode", WorkingDirectory: workingDirectory}); err != nil {
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
