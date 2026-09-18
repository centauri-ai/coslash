package launch

import (
	"fmt"
	"os/exec"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

var runOSAScript = func(arguments ...string) error {
	return exec.Command("osascript", arguments...).Run()
}

func openTerminal(terminal, workingDirectory, command string) error {
	var label, application string
	var open func(string, string) error
	switch terminal {
	case settings.TerminalApple:
		label, application, open = "Apple Terminal", "Terminal", openMacTerminal
	case settings.TerminalITerm:
		label, application, open = "iTerm2", "iTerm2", openMacITerm
	default:
		return fmt.Errorf("launch: unsupported terminal %q", terminal)
	}
	if err := macApplicationAvailable(application); err != nil {
		return fmt.Errorf("launch: %s is not installed or available; choose another terminal in Settings", label)
	}
	if err := open(workingDirectory, command); err != nil {
		return fmt.Errorf("launch: open %s: %w", label, err)
	}
	return nil
}

func Available(terminal string) bool {
	switch terminal {
	case settings.TerminalApple:
		return macApplicationAvailable("Terminal") == nil
	case settings.TerminalITerm:
		return macApplicationAvailable("iTerm2") == nil
	default:
		return false
	}
}

func macApplicationAvailable(name string) error {
	if name != "Terminal" && name != "iTerm2" {
		return fmt.Errorf("unknown application %q", name)
	}
	return runOSAScript("-e", `id of application "`+name+`"`)
}

func openMacTerminal(workingDirectory, command string) error {
	script := "cd " + shellQuote(workingDirectory) + " && " + command
	return runOSAScript(
		"-e", "on run argv",
		"-e", `tell application "Terminal" to do script (item 1 of argv)`,
		"-e", `tell application "Terminal" to activate`,
		"-e", "end run",
		"--", script,
	)
}

func openMacITerm(workingDirectory, command string) error {
	script := "cd " + shellQuote(workingDirectory) + " && " + command
	return runOSAScript(
		"-e", "on run argv",
		"-e", `tell application "iTerm2"`,
		"-e", `set newWindow to (create window with default profile)`,
		"-e", `tell current session of newWindow to write text (item 1 of argv)`,
		"-e", `activate`,
		"-e", `end tell`,
		"-e", "end run",
		"--", script,
	)
}
