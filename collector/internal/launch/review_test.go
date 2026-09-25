package launch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

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

func TestCleanupReviewScratchRemovesOnlyStaleCursorData(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	cutoff := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	for name, modified := range map[string]time.Time{
		"cursor-stale": cutoff.Add(-time.Hour),
		"cursor-fresh": cutoff.Add(time.Hour),
		"unrelated":    cutoff.Add(-time.Hour),
	} {
		path := filepath.Join(reviewScratchDir(), name)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	if err := cleanupReviewScratch(cutoff); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"cursor-stale": false, "cursor-fresh": true, "unrelated": true} {
		_, err := os.Stat(filepath.Join(reviewScratchDir(), name))
		if (err == nil) != want {
			t.Fatalf("review scratch %q: exists=%t, want %t (err=%v)", name, err == nil, want, err)
		}
	}
}

func TestReviewCLICommands(t *testing.T) {
	name := "Review — Bob's change (12345678)"
	prompt := "Review Bob's change\nDo not edit."
	sandbox := "enabled"
	if runtime.GOOS == "windows" {
		sandbox = "disabled"
	}
	cursorBin := cursorReviewerExecutable()
	cursorArgs := []string{"--print", "--mode=ask", "--sandbox", sandbox, "--trust", "--add-dir", "/repo", "--output-format", "text"}
	if strings.EqualFold(filepath.Ext(cursorBin), ".ps1") {
		cursorArgs = append([]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", cursorBin}, cursorArgs...)
		cursorBin = "powershell.exe"
	}
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
			env:   []string{`OPENCODE_PERMISSION={"edit":"deny","bash":"deny"}`},
			stdin: prompt + "\nUse the supplied worktree snapshot for the review. Read the contents of untracked files named in git status with the file reader. Do not run shell commands.\n",
		},
		"cursor": {
			bin:   cursorBin,
			args:  cursorArgs,
			stdin: prompt + "\nUse the supplied worktree snapshot for the review. Read the contents of untracked files named in git status with the file reader. Do not run shell commands.\n",
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

func TestReviewGitSnapshotListsFilesInUntrackedDirectories(t *testing.T) {
	repo := t.TempDir()
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	path := filepath.Join(repo, "new-package", "main.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package newpackage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := reviewGitSnapshot(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot, "?? new-package/main.go") || strings.Contains(snapshot, "?? new-package/\n") {
		t.Fatalf("untracked file is not named in snapshot: %s", snapshot)
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
		{ID: "claude", Label: "Claude Code CLI", Executable: "claude"},
		{ID: "codex", Label: "Codex CLI", Executable: "codex"},
		{ID: "opencode", Label: "OpenCode CLI", Executable: "opencode"},
		{ID: "cursor", Label: "Cursor CLI", Executable: cursorReviewerExecutable()},
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
