package remote

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestRemoteReviewCommandIsReadOnlyAndQuotesDirectory(t *testing.T) {
	for _, reviewer := range []string{"claude", "codex"} {
		command, err := remoteReviewCommand(reviewer, "/work/O'Brien")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(command, "cd '/work/O'\\''Brien' || exit 1") {
			t.Fatalf("directory is not quoted: %q", command)
		}
		if reviewer == "claude" && !strings.Contains(command, "'--permission-mode' 'plan'") {
			t.Fatalf("Claude is not read-only: %q", command)
		}
		if reviewer == "claude" && !strings.Contains(command, "'--strict-mcp-config'") {
			t.Fatalf("Claude MCP config is not isolated: %q", command)
		}
		if reviewer == "codex" && !strings.Contains(command, "'--sandbox' 'read-only'") {
			t.Fatalf("Codex is not read-only: %q", command)
		}
		if reviewer == "codex" && !strings.Contains(command, "'--ignore-user-config'") {
			t.Fatalf("Codex user config is not isolated: %q", command)
		}
	}
	if _, err := remoteReviewCommand("opencode", "/work"); err == nil {
		t.Fatal("unsupported remote reviewer accepted")
	}
}

func TestRemoteReviewSendsPromptOnStdin(t *testing.T) {
	t.Setenv("REMOTE_REVIEW_HELPER", "1")
	var args []string
	options := OpenOptions{command: func(ctx context.Context, _ string, commandArgs ...string) *exec.Cmd {
		args = commandArgs
		return exec.CommandContext(ctx, os.Args[0], "-test.run=TestRemoteReviewHelper")
	}}
	result, err := remoteReview(context.Background(), "agent-box", "codex", "/work", "Review", "private prompt", options)
	if err != nil || result != "private prompt" {
		t.Fatalf("result = %q, err = %v", result, err)
	}
	if strings.Contains(strings.Join(args, " "), "private prompt") {
		t.Fatalf("prompt exposed in SSH arguments: %q", args)
	}
}

func TestAvailableReviewersOnlyReportsInstalledSupportedCLIs(t *testing.T) {
	t.Setenv("REMOTE_REVIEW_PROBE_HELPER", "1")
	options := OpenOptions{command: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], "-test.run=TestRemoteReviewProbeHelper")
	}}
	available, err := availableReviewers(context.Background(), "agent-box", options)
	if err != nil || !available["claude"] || available["codex"] || available["opencode"] {
		t.Fatalf("available = %v, err = %v", available, err)
	}
}

func TestRemoteReviewProbeHelper(t *testing.T) {
	if os.Getenv("REMOTE_REVIEW_PROBE_HELPER") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString("claude\nopencode\n")
	os.Exit(0)
}

func TestRemoteReviewHelper(t *testing.T) {
	if os.Getenv("REMOTE_REVIEW_HELPER") != "1" {
		return
	}
	data, _ := io.ReadAll(os.Stdin)
	_, _ = os.Stdout.Write(data)
	os.Exit(0)
}
