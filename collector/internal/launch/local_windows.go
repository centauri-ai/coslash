package launch

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode/utf16"
	"unsafe"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"golang.org/x/sys/windows"
)

var windowsLookPath = exec.LookPath
var windowsCreateProcess = windows.CreateProcess
var windowsCloseHandle = windows.CloseHandle
var windowsStartConsole = startWindowsConsole

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

func openTerminalForAgent(ctx context.Context, terminal, agent, workingDirectory, command string) error {
	if agent != vendors.AgentCursor {
		return openTerminal(ctx, terminal, workingDirectory, command)
	}
	if terminal != settings.TerminalWindows {
		return fmt.Errorf("launch: unsupported terminal %q", terminal)
	}
	powerShell, err := windowsLookPath("powershell.exe")
	if err != nil {
		return fmt.Errorf("launch: Windows PowerShell is not installed or available")
	}
	if err := windowsStartConsole(powerShell, workingDirectory, powerShellCommandArguments(command)...); err != nil {
		return fmt.Errorf("launch: open Windows PowerShell: %w", err)
	}
	return nil
}

func Available(terminal string) bool {
	if terminal != settings.TerminalWindows {
		return false
	}
	_, err := windowsLookPath("powershell.exe")
	return err == nil
}

func openWindowsTerminal(workingDirectory, command string) error {
	arguments := powerShellCommandArguments(command)
	if terminal, err := windowsLookPath("wt.exe"); err == nil {
		process := exec.Command(terminal, append([]string{"-d", workingDirectory, "powershell.exe"}, arguments...)...)
		process.Dir = workingDirectory
		return windowsStart(process)
	}
	powerShell, err := windowsLookPath("powershell.exe")
	if err != nil {
		return fmt.Errorf("Windows Terminal and Windows PowerShell are not installed or available")
	}
	return startWindowsConsole(powerShell, workingDirectory, arguments...)
}

func powerShellCommandArguments(command string) []string {
	command = "$env:TERM = 'xterm-256color'; " + command
	utf16Command := utf16.Encode([]rune(command))
	encodedCommand := make([]byte, len(utf16Command)*2)
	for i, codeUnit := range utf16Command {
		binary.LittleEndian.PutUint16(encodedCommand[i*2:], codeUnit)
	}
	return []string{
		"-NoLogo",
		"-NoProfile",
		"-NonInteractive",
		"-NoExit",
		"-EncodedCommand",
		base64.StdEncoding.EncodeToString(encodedCommand),
	}
}

func startWindowsConsole(executable, workingDirectory string, arguments ...string) error {
	applicationName, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return err
	}
	commandLine, err := windows.UTF16FromString(windows.ComposeCommandLine(append([]string{executable}, arguments...)))
	if err != nil {
		return err
	}
	currentDirectory, err := windows.UTF16PtrFromString(workingDirectory)
	if err != nil {
		return err
	}
	startupInfo := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
	var processInformation windows.ProcessInformation
	err = windowsCreateProcess(
		applicationName,
		&commandLine[0],
		nil,
		nil,
		false,
		windows.CREATE_NEW_CONSOLE,
		nil,
		currentDirectory,
		&startupInfo,
		&processInformation,
	)
	if processInformation.Process != 0 {
		_ = windowsCloseHandle(processInformation.Process)
	}
	if processInformation.Thread != 0 {
		_ = windowsCloseHandle(processInformation.Thread)
	}
	return err
}

func localCommandJoin(arguments ...string) string {
	if len(arguments) > 0 && strings.HasSuffix(strings.ToLower(arguments[0]), ".ps1") {
		arguments = append([]string{"powershell.exe", "-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File"}, arguments...)
	}
	quoted := make([]string, len(arguments))
	for i, argument := range arguments {
		quoted[i] = powerShellQuote(argument)
	}
	return "& " + strings.Join(quoted, " ")
}

func localCLIExecutable(agent, fallback string) string {
	if agent != vendors.AgentCursor {
		return fallback
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fallback
	}
	if path := CursorCLIExecutable(home); path != "" {
		return path
	}
	return fallback
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
