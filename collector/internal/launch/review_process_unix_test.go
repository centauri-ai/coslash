//go:build unix

package launch

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/agentexec"
	"github.com/centauri-ai/coslash/collector/internal/review"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestReviewerDoesNotInheritSessionMarkers(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "parent")
	t.Setenv("CODEX_HOME", "/codex-home")
	original := reviewCommandContext
	t.Cleanup(func() { reviewCommandContext = original })
	reviewCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return agentexec.CommandContext(ctx, "env")
	}

	output, err := Review(context.Background(), review.Launch{Reviewer: vendors.AgentOpenCode, WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Split(output, "\n")
	if slices.Contains(env, "CLAUDECODE=1") || slices.Contains(env, "CLAUDE_CODE_SESSION_ID=parent") {
		t.Fatalf("reviewer inherited session markers: %q", output)
	}
	if !slices.Contains(env, "CODEX_HOME=/codex-home") || !strings.Contains(output, "\nOPENCODE_PERMISSION=") {
		t.Fatalf("reviewer lost its environment: %q", output)
	}
}

func TestConfigureReviewProcessCreatesProcessGroup(t *testing.T) {
	command := exec.Command("true")
	configureReviewProcess(command)
	if command.SysProcAttr == nil || !command.SysProcAttr.Setpgid {
		t.Fatal("reviewer was not placed in its own process group")
	}
}

func TestReviewCLIAvailabilityProbeUsesProcessGroup(t *testing.T) {
	t.Setenv("REVIEW_RESULT_OUTPUT", "--safe-mode --restricted --strict-mcp-config --tools")
	original := reviewCommandContext
	t.Cleanup(func() { reviewCommandContext = original })
	var command *exec.Cmd
	reviewCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		command = exec.CommandContext(ctx, os.Args[0], "-test.run=TestReviewResultHelper")
		return command
	}
	if !ReviewCLIAvailable(context.Background(), "claude") || command == nil || command.Cancel == nil ||
		command.SysProcAttr == nil || !command.SysProcAttr.Setpgid {
		t.Fatal("CLI availability probe lacks process group cleanup")
	}
}
