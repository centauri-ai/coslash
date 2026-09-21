package launch

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
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
			want:  []string{"Get-Content -Raw -LiteralPath", "& 'codex' '-c' ('developer_instructions=' + $handoff)", "finally { Remove-Item -LiteralPath"},
		},
		{
			agent: vendors.AgentOpenCode,
			want:  []string{"$env:OPENCODE_CONFIG_CONTENT =", "& 'opencode'", "Remove-Item Env:OPENCODE_CONFIG_CONTENT", "Remove-Item -LiteralPath"},
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
	originalLookPath, originalStart := windowsLookPath, windowsStart
	t.Cleanup(func() { windowsLookPath, windowsStart = originalLookPath, originalStart })
	windowsLookPath = func(name string) (string, error) {
		if name != "wt.exe" {
			t.Fatalf("LookPath(%q), want wt.exe", name)
		}
		return `C:\Windows\wt.exe`, nil
	}
	var gotName string
	var gotDirectory string
	var gotArgs []string
	windowsStart = func(name, directory string, arguments ...string) error {
		gotName, gotDirectory, gotArgs = name, directory, arguments
		return nil
	}

	if err := openWindowsTerminal(`C:\Users\Bob's Project`, `& 'codex' 'resume' 'session'`); err != nil {
		t.Fatal(err)
	}
	if gotName != `C:\Windows\wt.exe` {
		t.Fatalf("executable = %q", gotName)
	}
	if gotDirectory != `C:\Users\Bob's Project` {
		t.Fatalf("working directory = %q", gotDirectory)
	}
	wantArgs := []string{"-d", `C:\Users\Bob's Project`, "powershell.exe", "-NoExit", "-Command", `& 'codex' 'resume' 'session'`}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("arguments = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestOpenWindowsTerminalFallsBackToWindowsPowerShell(t *testing.T) {
	originalLookPath, originalStart := windowsLookPath, windowsStart
	t.Cleanup(func() { windowsLookPath, windowsStart = originalLookPath, originalStart })
	windowsLookPath = func(name string) (string, error) {
		switch name {
		case "wt.exe":
			return "", errors.New("not found")
		case "powershell.exe":
			return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, nil
		default:
			t.Fatalf("unexpected LookPath(%q)", name)
			return "", nil
		}
	}
	var gotName string
	var gotDirectory string
	var gotArgs []string
	windowsStart = func(name, directory string, arguments ...string) error {
		gotName, gotDirectory, gotArgs = name, directory, arguments
		return nil
	}

	if err := openWindowsTerminal(`C:\work`, `& 'claude'`); err != nil {
		t.Fatal(err)
	}
	if gotName != `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe` {
		t.Fatalf("executable = %q", gotName)
	}
	if gotDirectory != `C:\work` {
		t.Fatalf("working directory = %q", gotDirectory)
	}
	wantArgs := []string{"-NoExit", "-Command", `& 'claude'`}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("arguments = %#v, want %#v", gotArgs, wantArgs)
	}
}
