//go:build !windows

package synthesis

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestExecuteCommandAddsEnvWithoutSessionMarkers(t *testing.T) {
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "1")
	t.Setenv("CODEX_HOME", "/codex-home")
	output, err := executeCommand(context.Background(), commandSpec{bin: "env", env: []string{"XDG_DATA_HOME=/scratch"}})
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Split(strings.TrimSpace(string(output)), "\n")
	if slices.Contains(env, "CLAUDE_CODE_CHILD_SESSION=1") {
		t.Fatal("synthesis agent inherited a session marker")
	}
	if !slices.Contains(env, "CODEX_HOME=/codex-home") || !slices.Contains(env, "XDG_DATA_HOME=/scratch") {
		t.Fatalf("synthesis environment = %q", env)
	}
}
