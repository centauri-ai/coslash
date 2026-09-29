package launch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/agentexec"
)

var runOSAScript = func(ctx context.Context, arguments ...string) error {
	return osascriptCommand(ctx, arguments...).Run()
}

// A terminal application that osascript starts inherits this environment.
func osascriptCommand(ctx context.Context, arguments ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, "osascript", arguments...)
	command.Env = agentexec.WithoutSessionMarkers(os.Environ())
	return command
}

func macApplicationAvailable(ctx context.Context, name string) error {
	if name != "Terminal" && name != "iTerm2" {
		return fmt.Errorf("unknown application %q", name)
	}
	return runOSAScript(ctx, "-e", `id of application "`+name+`"`)
}

func openMacTerminal(ctx context.Context, workingDirectory, command string) error {
	return runOSAScript(ctx,
		"-e", "on run argv",
		"-e", `tell application "Terminal" to do script (item 1 of argv)`,
		"-e", `tell application "Terminal" to activate`,
		"-e", "end run",
		"--", terminalScript(os.Getenv("SHELL"), workingDirectory, command),
	)
}

func openMacITerm(ctx context.Context, workingDirectory, command string) error {
	return runOSAScript(ctx,
		"-e", "on run argv",
		"-e", `tell application "iTerm2"`,
		"-e", `set newWindow to (create window with default profile)`,
		"-e", `tell current session of newWindow to write text (item 1 of argv)`,
		"-e", `activate`,
		"-e", `end tell`,
		"-e", "end run",
		"--", terminalScript(os.Getenv("SHELL"), workingDirectory, command),
	)
}

// terminalScript also clears the session markers in the terminal shell, which
// keeps its own environment when the terminal was already running. The script
// of any other shell is unchanged.
func terminalScript(shell, workingDirectory, command string) string {
	script := "cd " + shellQuote(workingDirectory) + " && " + command
	markers := strings.Join(agentexec.SessionMarkers(), " ")
	switch name := filepath.Base(shell); {
	case slices.Contains([]string{"sh", "bash", "zsh", "ksh", "dash"}, name):
		return "unset " + markers + "; " + script
	case name == "fish":
		return "set --erase " + markers + "; " + script
	default:
		return script
	}
}
