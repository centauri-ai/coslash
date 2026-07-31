package launch

import (
	"os/exec"
	"strings"
)

func openMacTerminal(workingDirectory, command string) error {
	script := "cd " + shellQuote(workingDirectory) + " && " + command
	return exec.Command("osascript",
		"-e", "on run argv",
		"-e", `tell application "Terminal" to do script (item 1 of argv)`,
		"-e", `tell application "Terminal" to activate`,
		"-e", "end run",
		"--", script,
	).Run()
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
