package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"golang.org/x/sys/windows"
)

var windowsLookPath = exec.LookPath

var windowsStart = func(command *exec.Cmd) error {
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

func openTerminal(_ context.Context, terminal, workingDirectory, command string) error {
	if terminal != settings.TerminalWindows {
		return fmt.Errorf("launch: unsupported terminal %q", terminal)
	}
	if err := openWindowsTerminal(workingDirectory, command); err != nil {
		return fmt.Errorf("launch: open Windows Terminal: %w", err)
	}
	return nil
}

func Available(terminal string) bool {
	if terminal != settings.TerminalWindows {
		return false
	}
	if _, err := windowsLookPath("wt.exe"); err == nil {
		return true
	}
	_, err := windowsLookPath("powershell.exe")
	return err == nil
}

func openWindowsTerminal(workingDirectory, command string) error {
	if terminal, err := windowsLookPath("wt.exe"); err == nil {
		process := exec.Command(terminal, "-d", workingDirectory, "powershell.exe", "-NoExit", "-Command", command)
		process.Dir = workingDirectory
		return windowsStart(process)
	}
	powerShell, err := windowsLookPath("powershell.exe")
	if err != nil {
		return fmt.Errorf("Windows Terminal and Windows PowerShell are not installed or available")
	}
	process := exec.Command(powerShell, "-NoExit", "-Command", command)
	configureWindowsConsole(process, workingDirectory)
	return windowsStart(process)
}

func configureWindowsConsole(command *exec.Cmd, workingDirectory string) {
	command.Dir = workingDirectory
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE}
}

func localCommandJoin(arguments ...string) string {
	quoted := make([]string, len(arguments))
	for i, argument := range arguments {
		quoted[i] = powerShellQuote(argument)
	}
	return "& " + strings.Join(quoted, " ")
}

func powerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func handoffCommand(agent, cli, handoff, prompt string) (string, string, error) {
	context := handoffPreamble + handoff
	switch agent {
	case vendors.AgentClaude:
		path, err := writeHandoffFile(context)
		if err != nil {
			return "", "", err
		}
		arguments := []string{cli, "--append-system-prompt-file", path}
		if prompt != "" {
			arguments = append(arguments, "--", prompt)
		}
		command := localCommandJoin(arguments...)
		return withPowerShellCleanup(command, path), path, nil
	case vendors.AgentCodex:
		encoded, err := json.Marshal(context)
		if err != nil {
			return "", "", fmt.Errorf("launch: encoding handoff context: %w", err)
		}
		path, err := writeHandoffFile(string(encoded))
		if err != nil {
			return "", "", err
		}
		command := "$handoff = Get-Content -Raw -Encoding UTF8 -LiteralPath " + powerShellQuote(path) + " -ErrorAction Stop; " +
			localCommandJoin(cli, "-c") + " ('developer_instructions=' + $handoff)"
		if prompt != "" {
			command += " " + powerShellQuote("--") + " " + powerShellQuote(prompt)
		}
		return withPowerShellCleanup(command, path), path, nil
	case vendors.AgentOpenCode:
		path, err := writeHandoffFile(context)
		if err != nil {
			return "", "", err
		}
		config, err := json.Marshal(map[string][]string{"instructions": {path}})
		if err != nil {
			os.Remove(path)
			return "", "", fmt.Errorf("launch: encoding OpenCode handoff config: %w", err)
		}
		setup := "$hadOpenCodeConfigContent = Test-Path Env:OPENCODE_CONFIG_CONTENT; " +
			"$previousOpenCodeConfigContent = $env:OPENCODE_CONFIG_CONTENT; "
		command := "$env:OPENCODE_CONFIG_CONTENT = " + powerShellQuote(string(config)) + "; " + localCommandJoin(cli)
		if prompt != "" {
			command += " " + powerShellQuote(prompt)
		}
		restore := "if ($hadOpenCodeConfigContent) { $env:OPENCODE_CONFIG_CONTENT = $previousOpenCodeConfigContent } else { " +
			"Remove-Item Env:OPENCODE_CONFIG_CONTENT -ErrorAction SilentlyContinue }; " + powerShellRemove(path)
		return setup + "try { " + command + " } finally { " + restore + " }", path, nil
	case vendors.AgentCursor:
		return localCommandJoin(cli), "", nil
	}
	return "", "", fmt.Errorf("launch: unknown agent %q", agent)
}

func withPowerShellCleanup(command, path string) string {
	return "try { " + command + " } finally { " + powerShellRemove(path) + " }"
}

func powerShellRemove(path string) string {
	return "Remove-Item -LiteralPath " + powerShellQuote(path) + " -Force -ErrorAction SilentlyContinue"
}
