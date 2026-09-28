package launch

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestTerminalScriptClearsSessionMarkersInPOSIXShells(t *testing.T) {
	command := "'claude' '--resume' 'id'"
	original := "cd '/repo' && " + command
	for _, shell := range []string{"/bin/zsh", "/bin/bash", "/bin/sh", "/opt/homebrew/bin/bash"} {
		script := terminalScript(shell, "/repo", command)
		prefix, rest, found := strings.Cut(script, "; ")
		if !found || rest != original {
			t.Fatalf("%s script = %q, want unset prefix then %q", shell, script, original)
		}
		names := strings.Fields(strings.TrimPrefix(prefix, "unset "))
		if !slices.Contains(names, "CLAUDE_CODE_CHILD_SESSION") || !slices.Contains(names, "CODEX_SESSION_ID") || slices.Contains(names, "CODEX_HOME") {
			t.Fatalf("%s unset names = %q", shell, names)
		}
	}
	for _, shell := range []string{"/opt/homebrew/bin/fish", ""} {
		if script := terminalScript(shell, "/repo", command); script != original {
			t.Fatalf("%q script = %q, want %q", shell, script, original)
		}
	}
}

func TestOSAScriptOmitsSessionMarkers(t *testing.T) {
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "1")
	t.Setenv("CODEX_HOME", "/codex-home")
	env := osascriptCommand(context.Background(), "-e", "return").Env
	if slices.Contains(env, "CLAUDE_CODE_CHILD_SESSION=1") || !slices.Contains(env, "CODEX_HOME=/codex-home") {
		t.Fatalf("osascript environment = %q", env)
	}
}
