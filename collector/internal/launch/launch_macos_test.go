package launch

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func TestITermUsesBundleIDForDiscoveryAndLaunch(t *testing.T) {
	original := runOSAScript
	t.Cleanup(func() { runOSAScript = original })
	var calls [][]string
	runOSAScript = func(_ context.Context, arguments ...string) error {
		calls = append(calls, arguments)
		return nil
	}

	if !Available(settings.TerminalITerm) {
		t.Fatal("iTerm2 unavailable")
	}
	if err := openTerminal(context.Background(), settings.TerminalITerm, "/tmp", "true"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("osascript calls = %d, want 3", len(calls))
	}
	for _, call := range calls[:2] {
		if !slices.Equal(call, []string{"-e", `id of application id "com.googlecode.iterm2"`}) {
			t.Fatalf("availability lookup = %q", call)
		}
	}
	if !slices.Contains(calls[2], `tell application id "com.googlecode.iterm2"`) {
		t.Fatalf("launch script = %q", calls[2])
	}
}

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
