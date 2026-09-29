package launch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestITermUsesBundleIDForDiscoveryAndLaunch(t *testing.T) {
	original := runOSAScript
	t.Cleanup(func() { runOSAScript = original })
	var calls [][]string
	runOSAScript = func(_ context.Context, arguments ...string) error {
		calls = append(calls, arguments)
		return nil
	}

	if err := macApplicationAvailable(context.Background(), macITermBundleID); err != nil {
		t.Fatal(err)
	}
	if err := openMacITerm(context.Background(), "/tmp", "true"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("osascript calls = %d, want 2", len(calls))
	}
	if !slices.Equal(calls[0], []string{"-e", `id of application id "com.googlecode.iterm2"`}) {
		t.Fatalf("availability lookup = %q", calls[0])
	}
	if !slices.Contains(calls[1], `tell application id "com.googlecode.iterm2"`) {
		t.Fatalf("launch script = %q", calls[1])
	}
}

func TestOpenMacTerminalStagesLongCommand(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("uses the macOS Terminal opener")
	}
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	workingDirectory := t.TempDir()
	command := "printf '%s' '" + strings.Repeat("x", 1200) + "' > result"
	original := runOSAScript
	t.Cleanup(func() { runOSAScript = original })
	runOSAScript = func(_ context.Context, args ...string) error {
		line := args[len(args)-1]
		if len(line) >= 1024 || strings.Contains(line, strings.Repeat("x", 100)) {
			t.Fatalf("Terminal input is too long: %d bytes", len(line))
		}
		if !strings.Contains(line, "'/bin/bash'") {
			t.Fatalf("Terminal command did not preserve the selected shell: %q", line)
		}
		output, err := exec.Command("/bin/sh", "-c", line).CombinedOutput()
		if err != nil {
			t.Fatalf("staged command failed: %v: %s", err, output)
		}
		return nil
	}
	if err := openMacTerminal(context.Background(), workingDirectory, command); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(workingDirectory, "result"))
	if err != nil || string(contents) != strings.Repeat("x", 1200) {
		t.Fatalf("terminal command result = %q, error = %v", contents, err)
	}
	files, err := os.ReadDir(filepath.Join(home, "sys-prompts"))
	if err != nil || len(files) != 0 {
		t.Fatalf("staged commands after exit = %v, error = %v", files, err)
	}
}

func TestOpenMacTerminalRemovesStagedCommandOnLaunchFailure(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("uses the macOS Terminal opener")
	}
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	original := runOSAScript
	t.Cleanup(func() { runOSAScript = original })
	runOSAScript = func(context.Context, ...string) error { return errors.New("Terminal unavailable") }
	if err := openMacTerminal(context.Background(), "/repo", "codex"); err == nil {
		t.Fatal("Terminal failure was ignored")
	}
	files, err := os.ReadDir(filepath.Join(home, "sys-prompts"))
	if err != nil || len(files) != 0 {
		t.Fatalf("staged commands after failure = %v, error = %v", files, err)
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
