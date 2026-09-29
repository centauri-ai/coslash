package launch

import (
	"context"
	"errors"
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

const macITermBundleID = "com.googlecode.iterm2"

// A terminal application that osascript starts inherits this environment.
func osascriptCommand(ctx context.Context, arguments ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, "osascript", arguments...)
	command.Env = agentexec.WithoutSessionMarkers(os.Environ())
	return command
}

func macApplicationAvailable(ctx context.Context, name string) error {
	switch name {
	case "Terminal":
		return runOSAScript(ctx, "-e", `id of application "Terminal"`)
	case macITermBundleID:
		return runOSAScript(ctx, "-e", `id of application id "`+macITermBundleID+`"`)
	default:
		return fmt.Errorf("unknown application %q", name)
	}
}

func openMacTerminal(ctx context.Context, workingDirectory, command string) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	path, err := writeHandoffFile(terminalScript(shell, workingDirectory, command) + "\n")
	if err != nil {
		return err
	}
	if err := runOSAScript(ctx,
		"-e", "on run argv",
		"-e", `tell application "Terminal" to do script (item 1 of argv)`,
		"-e", `tell application "Terminal" to activate`,
		"-e", "end run",
		"--", localCommandJoin("/bin/sh", "-c", `trap 'rm -f "$2"' EXIT; "$1" "$2"`, "sh", shell, path),
	); err != nil {
		return errors.Join(err, removeHandoffFile(path))
	}
	return nil
}

func openMacITerm(ctx context.Context, workingDirectory, command string) error {
	return runOSAScript(ctx,
		"-e", "on run argv",
		"-e", `tell application id "`+macITermBundleID+`"`,
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
