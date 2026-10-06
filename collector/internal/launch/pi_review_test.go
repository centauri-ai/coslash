package launch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/review"
)

func TestPiReviewReadOnlyCommandAndAvailability(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("local Pi reviews require macOS or Windows")
	}
	bin := t.TempDir()
	cli := filepath.Join(bin, "pi")
	if runtime.GOOS == "windows" {
		cli += ".exe"
	}
	if err := os.WriteFile(cli, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	flags := []string{"--print", "--no-session", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--tools", "--system-prompt", "--append-system-prompt"}
	original := reviewCommandContext
	t.Cleanup(func() { reviewCommandContext = original })
	reviewCommandContext = func(ctx context.Context, binary string, args ...string) *exec.Cmd {
		if binary != cli {
			t.Fatalf("Pi command = %q, want %q", binary, cli)
		}
		if slices.Contains(args, "--help") && slices.Contains(args, "--print") {
			t.Fatal("Pi print mode redirects help away from stdout")
		}
		for _, flag := range flags[1:] {
			if !slices.Contains(args, flag) {
				t.Fatalf("Pi probe/review missing isolation flag %s", flag)
			}
		}
		appendPrompt := slices.Index(args, "--append-system-prompt")
		if args[appendPrompt+1] != " " {
			t.Fatal("Pi append prompt must survive Windows native argument transport")
		}
		return exec.CommandContext(ctx, os.Args[0], "-test.run=TestReviewResultHelper")
	}
	for _, missing := range append([]string{""}, flags...) {
		help := slices.Clone(flags)
		if missing != "" {
			help = slices.DeleteFunc(help, func(flag string) bool { return flag == missing })
		}
		t.Setenv("REVIEW_RESULT_OUTPUT", strings.Join(help, " "))
		if got := ReviewCLIAvailable(context.Background(), "pi"); got != (missing == "") {
			t.Fatalf("missing %q: Pi availability = %v", missing, got)
		}
	}
	spec, err := reviewCLICommand("pi", t.TempDir(), "Review", "private session context")
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range flags {
		if !slices.Contains(spec.args, flag) {
			t.Errorf("Pi review missing %s", flag)
		}
	}
	tools := slices.Index(spec.args, "--tools")
	if tools < 0 || spec.args[tools+1] != "read,grep,find,ls" || spec.stdin != "private session context\nUse the supplied worktree snapshot for the review. Read the contents of untracked files named in git status with the file reader. Do not run shell commands.\n" || slices.Contains(spec.args, "private session context") {
		t.Fatalf("unsafe Pi review command: %#v", spec)
	}
	t.Setenv("REVIEW_RESULT_OUTPUT", "Found an untracked bug")
	if result, err := Review(context.Background(), review.Launch{Reviewer: "pi", WorkingDirectory: t.TempDir(), Prompt: "private session context"}); err != nil || result != "Found an untracked bug" {
		t.Fatalf("Pi review result = %q, %v", result, err)
	}
	if _, err := Review(context.Background(), review.Launch{Reviewer: "pi", SSHAlias: "host", WorkingDirectory: "/remote"}); err == nil {
		t.Fatal("remote Pi review accepted")
	}
}
