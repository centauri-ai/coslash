package launch

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

var runOSAScript = func(ctx context.Context, arguments ...string) error {
	return exec.CommandContext(ctx, "osascript", arguments...).Run()
}

func macApplicationAvailable(ctx context.Context, name string) error {
	if name != "Terminal" && name != "iTerm2" {
		return fmt.Errorf("unknown application %q", name)
	}
	return runOSAScript(ctx, "-e", `id of application "`+name+`"`)
}

func openMacTerminal(ctx context.Context, workingDirectory, command string) error {
	script := "cd " + shellQuote(workingDirectory) + " && " + command
	return runOSAScript(ctx,
		"-e", "on run argv",
		"-e", `tell application "Terminal" to do script (item 1 of argv)`,
		"-e", `tell application "Terminal" to activate`,
		"-e", "end run",
		"--", script,
	)
}

func openMacITerm(ctx context.Context, workingDirectory, command string) error {
	script := "cd " + shellQuote(workingDirectory) + " && " + command
	return runOSAScript(ctx,
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

func shellJoin(arguments ...string) string {
	quoted := make([]string, len(arguments))
	for i, argument := range arguments {
		quoted[i] = shellQuote(argument)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
