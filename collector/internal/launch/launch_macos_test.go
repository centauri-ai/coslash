package launch

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestTerminalScriptClearsSessionMarkersInKnownShells(t *testing.T) {
	command := "'claude' '--resume' 'id'"
	original := "cd '/repo' && " + command
	for shell, clear := range map[string]string{
		"/bin/zsh": "unset ", "/bin/bash": "unset ", "/bin/sh": "unset ", "/opt/homebrew/bin/bash": "unset ",
		"/opt/homebrew/bin/fish": "set --erase ",
	} {
		script := terminalScript(shell, "/repo", command)
		prefix, rest, found := strings.Cut(script, "; ")
		if !found || rest != original || !strings.HasPrefix(prefix, clear) {
			t.Fatalf("%s script = %q, want %q prefix then %q", shell, script, clear, original)
		}
		names := strings.Fields(strings.TrimPrefix(prefix, clear))
		if !slices.Contains(names, "CLAUDE_CODE_CHILD_SESSION") || !slices.Contains(names, "CODEX_SESSION_ID") || slices.Contains(names, "CODEX_HOME") {
			t.Fatalf("%s cleared names = %q", shell, names)
		}
	}
	for _, shell := range []string{"/usr/local/bin/nu", ""} {
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
