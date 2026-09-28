//go:build !windows

package agentexec

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestCommandContextOmitsSessionMarkersFromChildOnly(t *testing.T) {
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "1")
	t.Setenv("CODEX_SESSION_ID", "parent")
	t.Setenv("CODEX_HOME", "/codex-home")
	t.Setenv("CLAUDE_CODE_USE_FOUNDRY", "1")

	output, err := Output(CommandContext(context.Background(), "env"))
	if err != nil {
		t.Fatal(err)
	}
	child := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, marker := range []string{"CLAUDE_CODE_CHILD_SESSION=1", "CODEX_SESSION_ID=parent"} {
		if slices.Contains(child, marker) {
			t.Errorf("child environment contains %s", marker)
		}
	}
	for _, kept := range []string{"CODEX_HOME=/codex-home", "CLAUDE_CODE_USE_FOUNDRY=1", "PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")} {
		if !slices.Contains(child, kept) {
			t.Errorf("child environment lacks %s", kept)
		}
	}
	if os.Getenv("CLAUDE_CODE_CHILD_SESSION") != "1" {
		t.Error("CommandContext changed the environment of its own process")
	}
}
