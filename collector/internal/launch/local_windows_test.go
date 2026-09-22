package launch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"golang.org/x/sys/windows"
)

func TestPowerShellQuote(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "spaces", value: `C:\Users\Calvin Smith`, want: `'C:\Users\Calvin Smith'`},
		{name: "apostrophes", value: `C:\Users\Bob's Project`, want: `'C:\Users\Bob''s Project'`},
		{name: "Unicode", value: `C:\Users\卡尔文\🦖`, want: `'C:\Users\卡尔文\🦖'`},
		{name: "dollar signs", value: `$HOME\project`, want: `'$HOME\project'`},
		{name: "semicolons", value: `C:\work;Remove-Item`, want: `'C:\work;Remove-Item'`},
		{name: "ampersands", value: `C:\work&more`, want: `'C:\work&more'`},
		{name: "pipes", value: `C:\work|more`, want: `'C:\work|more'`},
		{name: "parentheses", value: `C:\work (copy)`, want: `'C:\work (copy)'`},
		{name: "backticks", value: "C:\\work`more", want: "'C:\\work`more'"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := powerShellQuote(test.value); got != test.want {
				t.Fatalf("powerShellQuote(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestLocalCommandJoinExecutesPowerShellLauncherWithLiteralArguments(t *testing.T) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("Windows PowerShell is not installed")
	}
	directory := t.TempDir()
	script := filepath.Join(directory, "agent.ps1")
	output := filepath.Join(directory, "output.txt")
	sentinel := filepath.Join(directory, "injected.txt")
	contents := "param([string]$Value) [IO.File]::WriteAllText(" + powerShellQuote(output) + ", $Value)"
	if err := os.WriteFile(script, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	argument := "'; [IO.File]::WriteAllText(" + powerShellQuote(sentinel) + ", 'injected'); #"
	command := localCommandJoin(script, argument) + "; exit $LASTEXITCODE"
	process := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command)
	if combined, err := process.CombinedOutput(); err != nil {
		t.Fatalf("execute launcher: %v\n%s", err, combined)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != argument {
		t.Fatalf("launcher argument = %q, want %q", data, argument)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("metacharacter argument executed; sentinel error = %v", err)
	}
}

func TestWindowsTerminalAvailabilityRequiresPowerShell(t *testing.T) {
	originalLookPath := windowsLookPath
	t.Cleanup(func() { windowsLookPath = originalLookPath })
	tests := []struct {
		name       string
		terminal   string
		windowsWT  bool
		powerShell bool
		want       bool
	}{
		{name: "PowerShell only", terminal: settings.TerminalWindows, powerShell: true, want: true},
		{name: "Windows Terminal and PowerShell", terminal: settings.TerminalWindows, windowsWT: true, powerShell: true, want: true},
		{name: "Windows Terminal only", terminal: settings.TerminalWindows, windowsWT: true, want: false},
		{name: "neither", terminal: settings.TerminalWindows, want: false},
		{name: "unsupported terminal", terminal: "invalid", windowsWT: true, powerShell: true, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			windowsLookPath = func(name string) (string, error) {
				switch name {
				case "wt.exe":
					if test.windowsWT {
						return `C:\Windows\wt.exe`, nil
					}
				case "powershell.exe":
					if test.powerShell {
						return `C:\Windows\powershell.exe`, nil
					}
				}
				return "", errors.New("not found")
			}
			if got := Available(test.terminal); got != test.want {
				t.Fatalf("Available(%q) = %v, want %v", test.terminal, got, test.want)
			}
		})
	}
}

func TestCursorUsesStandaloneWindowsPowerShell(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	originalLookPath, originalStart := windowsLookPath, windowsStartConsole
	t.Cleanup(func() {
		windowsLookPath = originalLookPath
		windowsStartConsole = originalStart
	})
	windowsLookPath = func(name string) (string, error) {
		if name != "powershell.exe" {
			t.Fatalf("LookPath(%q), want powershell.exe without Windows Terminal lookup", name)
		}
		return `C:\Windows\powershell.exe`, nil
	}
	var gotExecutable, gotDirectory string
	var gotArguments []string
	windowsStartConsole = func(_ context.Context, executable, workingDirectory string, arguments ...string) error {
		gotExecutable, gotDirectory = executable, workingDirectory
		gotArguments = arguments
		return nil
	}
	if err := openTerminalForAgent(context.Background(), settings.TerminalWindows, vendors.AgentCursor, `C:\workspace`, "cursor command"); err != nil {
		t.Fatal(err)
	}
	if gotExecutable != `C:\Windows\powershell.exe` || gotDirectory != `C:\workspace` {
		t.Fatalf("standalone launch = %q in %q", gotExecutable, gotDirectory)
	}
	if len(gotArguments) == 0 {
		t.Fatal("standalone launch omitted PowerShell arguments")
	}
	script := gotArguments[len(gotArguments)-1]
	if want := powerShellCommandArguments(script, true); !reflect.DeepEqual(gotArguments, want) {
		t.Fatalf("arguments = %#v, want %#v", gotArguments, want)
	}
	contents, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "cursor command") {
		t.Fatalf("launch script does not contain Cursor command: %q", contents)
	}
}

func TestWindowsCLICommandPreservesVendorResumeForms(t *testing.T) {
	tests := []struct {
		agent     string
		sessionID string
		want      string
	}{
		{agent: vendors.AgentClaude, sessionID: "01234567-89ab-cdef-0123-456789abcdef", want: "& 'claude' '--resume' '01234567-89ab-cdef-0123-456789abcdef'"},
		{agent: vendors.AgentCodex, sessionID: "01234567-89ab-cdef-0123-456789abcdef", want: "& 'codex' 'resume' '01234567-89ab-cdef-0123-456789abcdef'"},
		{agent: vendors.AgentOpenCode, sessionID: "ses_0123456789abcdef", want: "& 'opencode' '--session' 'ses_0123456789abcdef'"},
	}
	for _, test := range tests {
		t.Run(test.agent, func(t *testing.T) {
			got, path, err := cliCommand(test.agent, test.sessionID, ResumeSession, "")
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want || path != "" {
				t.Fatalf("cliCommand() = %q, %q, want %q, empty path", got, path, test.want)
			}
		})
	}
}

func TestWindowsHandoffCommandsUsePowerShellCleanup(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	tests := []struct {
		agent string
		want  []string
	}{
		{
			agent: vendors.AgentClaude,
			want:  []string{"try { & 'claude' '--append-system-prompt-file'", "finally { Remove-Item -LiteralPath"},
		},
		{
			agent: vendors.AgentCodex,
			want:  []string{"Get-Content -Raw -Encoding UTF8 -LiteralPath", "& 'codex' '-c' ('developer_instructions=' + $handoff)", "finally { Remove-Item -LiteralPath"},
		},
		{
			agent: vendors.AgentOpenCode,
			want: []string{
				"$hadOpenCodeConfigContent = Test-Path Env:OPENCODE_CONFIG_CONTENT",
				"$previousOpenCodeConfigContent = $env:OPENCODE_CONFIG_CONTENT",
				"$env:OPENCODE_CONFIG_CONTENT =",
				"& 'opencode'",
				"if ($hadOpenCodeConfigContent) { $env:OPENCODE_CONFIG_CONTENT = $previousOpenCodeConfigContent } else { Remove-Item Env:OPENCODE_CONFIG_CONTENT -ErrorAction SilentlyContinue }",
				"Remove-Item -LiteralPath",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.agent, func(t *testing.T) {
			command, path, err := cliCommand(test.agent, "", NewSession, "handoff")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Remove(path) })
			for _, want := range test.want {
				if !strings.Contains(command, want) {
					t.Fatalf("command = %q, want substring %q", command, want)
				}
			}
			if strings.Contains(command, "rm -f") || strings.Contains(command, "OPENCODE_CONFIG_CONTENT=") {
				t.Fatalf("command uses POSIX local syntax: %q", command)
			}
		})
	}
}

func TestOpenWindowsTerminalPrefersWindowsTerminal(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	originalLookPath, originalStart := windowsLookPath, windowsStart
	t.Cleanup(func() { windowsLookPath, windowsStart = originalLookPath, originalStart })
	windowsLookPath = func(name string) (string, error) {
		switch name {
		case "powershell.exe":
			return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, nil
		case "wt.exe":
			return `C:\Windows\wt.exe`, nil
		default:
			t.Fatalf("unexpected LookPath(%q)", name)
			return "", nil
		}
	}
	var got *exec.Cmd
	windowsStart = func(command *exec.Cmd) error {
		got = command
		return nil
	}

	if err := openWindowsTerminal(context.Background(), `C:\Users\Bob's Project`, `& 'codex' 'resume' 'session'`); err != nil {
		t.Fatal(err)
	}
	if got.Path != `C:\Windows\wt.exe` {
		t.Fatalf("executable = %q", got.Path)
	}
	if got.Dir != `C:\Users\Bob's Project` {
		t.Fatalf("working directory = %q", got.Dir)
	}
	wantArgs := append(
		[]string{`C:\Windows\wt.exe`, "-d", `C:\Users\Bob's Project`, `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`},
		powerShellCommandArguments(got.Args[len(got.Args)-1], true)...,
	)
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("arguments = %#v, want %#v", got.Args, wantArgs)
	}
	if got.SysProcAttr != nil {
		t.Fatalf("Windows Terminal creation flags = %#v, want nil", got.SysProcAttr)
	}
}

func TestOpenWindowsTerminalFallsBackToWindowsPowerShell(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	originalLookPath, originalCreate, originalClose := windowsLookPath, windowsCreateProcess, windowsCloseHandle
	t.Cleanup(func() {
		windowsLookPath, windowsCreateProcess, windowsCloseHandle = originalLookPath, originalCreate, originalClose
	})
	windowsLookPath = func(name string) (string, error) {
		switch name {
		case "powershell.exe":
			return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, nil
		case "wt.exe":
			return "", errors.New("not found")
		default:
			t.Fatalf("unexpected LookPath(%q)", name)
			return "", nil
		}
	}
	var gotApplication, gotDirectory string
	var gotArguments []string
	var gotStartupInfo windows.StartupInfo
	var gotInheritHandles bool
	var gotCreationFlags uint32
	var gotEnvironment *uint16
	windowsCreateProcess = func(
		applicationName, commandLine *uint16,
		_, _ *windows.SecurityAttributes,
		inheritHandles bool,
		creationFlags uint32,
		environment, currentDirectory *uint16,
		startupInfo *windows.StartupInfo,
		processInformation *windows.ProcessInformation,
	) error {
		gotApplication = windows.UTF16PtrToString(applicationName)
		gotDirectory = windows.UTF16PtrToString(currentDirectory)
		var err error
		gotArguments, err = windows.DecomposeCommandLine(windows.UTF16PtrToString(commandLine))
		if err != nil {
			t.Fatal(err)
		}
		gotStartupInfo = *startupInfo
		gotInheritHandles = inheritHandles
		gotCreationFlags = creationFlags
		gotEnvironment = environment
		processInformation.Process = windows.Handle(11)
		processInformation.Thread = windows.Handle(12)
		return nil
	}
	var closed []windows.Handle
	windowsCloseHandle = func(handle windows.Handle) error {
		closed = append(closed, handle)
		return nil
	}

	if err := openWindowsTerminal(context.Background(), `C:\work 卡尔文`, `& 'claude' 'Bob''s session'`); err != nil {
		t.Fatal(err)
	}
	if gotApplication != `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe` {
		t.Fatalf("executable = %q", gotApplication)
	}
	if gotDirectory != `C:\work 卡尔文` {
		t.Fatalf("working directory = %q", gotDirectory)
	}
	wantArgs := append(
		[]string{`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`},
		powerShellCommandArguments(gotArguments[len(gotArguments)-1], true)...,
	)
	if !reflect.DeepEqual(gotArguments, wantArgs) {
		t.Fatalf("arguments = %#v, want %#v", gotArguments, wantArgs)
	}
	if gotStartupInfo.Cb != uint32(unsafe.Sizeof(windows.StartupInfo{})) {
		t.Fatalf("StartupInfo.Cb = %d", gotStartupInfo.Cb)
	}
	if gotStartupInfo.Flags != 0 || gotStartupInfo.StdInput != 0 || gotStartupInfo.StdOutput != 0 || gotStartupInfo.StdErr != 0 {
		t.Fatalf("StartupInfo uses inherited standard handles: %#v", gotStartupInfo)
	}
	if gotInheritHandles || gotEnvironment != nil || gotCreationFlags != windows.CREATE_NEW_CONSOLE {
		t.Fatalf("CreateProcess inherit=%v environment=%p flags=%#x", gotInheritHandles, gotEnvironment, gotCreationFlags)
	}
	if want := []windows.Handle{11, 12}; !reflect.DeepEqual(closed, want) {
		t.Fatalf("closed handles = %v, want %v", closed, want)
	}
}

func TestWindowsRemoteSSHDisablesConfiguredMultiplexing(t *testing.T) {
	destination, err := settings.ParseSSHDestination("linux-host")
	if err != nil {
		t.Fatal(err)
	}
	command := remoteSSHCommand(destination, "true")
	if !strings.Contains(command, "'ControlMaster=no'") || strings.Contains(command, "'ControlMaster=auto'") {
		t.Fatalf("command = %q", command)
	}
}

func TestOpenTerminalHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := openTerminal(ctx, settings.TerminalWindows, `C:\work`, "Write-Output 'stale'")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("openTerminal() error = %v, want context canceled", err)
	}
}

func TestPowerShellCommandUsesPrivateScriptInsteadOfArgv(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	command := "Write-Output " + powerShellQuote(strings.Repeat("卡尔文 🦖", 16<<10))
	path, err := writePowerShellScript(command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	arguments := powerShellCommandArguments(path, true)
	want := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-NoExit", "-File", path}
	if !reflect.DeepEqual(arguments, want) {
		t.Fatalf("PowerShell arguments = %#v, want %#v", arguments, want)
	}
	for _, argument := range arguments {
		if strings.Contains(argument, "卡尔文") {
			t.Fatal("prompt content was exposed in argv")
		}
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), command) || !strings.Contains(string(contents), "Remove-Item -LiteralPath") {
		t.Fatal("launch script does not contain the command and self-cleanup")
	}
}

func TestWindowsPowerShellClosesPartialHandlesOnCreateError(t *testing.T) {
	originalCreate, originalClose := windowsCreateProcess, windowsCloseHandle
	t.Cleanup(func() { windowsCreateProcess, windowsCloseHandle = originalCreate, originalClose })
	wantErr := errors.New("create failed")
	windowsCreateProcess = func(
		_, _ *uint16,
		_, _ *windows.SecurityAttributes,
		_ bool,
		_ uint32,
		_, _ *uint16,
		_ *windows.StartupInfo,
		processInformation *windows.ProcessInformation,
	) error {
		processInformation.Process = windows.Handle(21)
		processInformation.Thread = windows.Handle(22)
		return wantErr
	}
	var closed []windows.Handle
	windowsCloseHandle = func(handle windows.Handle) error {
		closed = append(closed, handle)
		return nil
	}

	err := startWindowsConsole(context.Background(), `C:\Windows\powershell.exe`, `C:\work`, "-NoExit")
	if !errors.Is(err, wantErr) {
		t.Fatalf("startWindowsConsole() error = %v, want %v", err, wantErr)
	}
	if want := []windows.Handle{21, 22}; !reflect.DeepEqual(closed, want) {
		t.Fatalf("closed handles = %v, want %v", closed, want)
	}
}
