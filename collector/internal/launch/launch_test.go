package launch

import (
	"context"
	"errors"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestTerminalRemovesHandoffWhenTerminalOpenFails(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	originalOpener := localTerminalOpener
	t.Cleanup(func() { localTerminalOpener = originalOpener })
	wantErr := errors.New("terminal open failed")
	localTerminalOpener = func(context.Context, string, string, string, string) error { return wantErr }

	err := Terminal(context.Background(), settings.TerminalWindows, vendors.AgentClaude, t.TempDir(), "", NewSession, "private handoff")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Terminal() error = %v, want %v", err, wantErr)
	}
	entries, readErr := os.ReadDir(handoffDir())
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("failed launch left handoff files: %v", entries)
	}
}

func TestTerminalWithPromptRemovesClaudeFilesWhenTerminalOpenFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("secure interactive prompts require a POSIX terminal")
	}
	provideFakeExpect(t)
	t.Setenv("COSLASH_HOME", t.TempDir())
	originalOpener := localTerminalOpener
	t.Cleanup(func() { localTerminalOpener = originalOpener })
	localTerminalOpener = func(context.Context, string, string, string, string) error {
		return errors.New("terminal open failed")
	}
	if err := TerminalWithPrompt(context.Background(), settings.Defaults().Launch.Terminal, vendors.AgentClaude, t.TempDir(), "", NewSession, "private handoff", "request"); err == nil {
		t.Fatal("terminal opener failure was ignored")
	}
	entries, err := os.ReadDir(handoffDir())
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed launch left handoff files: %v, %v", entries, err)
	}
}

func TestRemoveHandoffFileIgnoresMissingFile(t *testing.T) {
	if err := removeHandoffFile(t.TempDir() + "/missing"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteCLICommandPreservesVendorResumeForms(t *testing.T) {
	tests := []struct {
		agent     string
		sessionID string
		want      string
	}{
		{agent: vendors.AgentClaude, sessionID: "01234567-89ab-cdef-0123-456789abcdef", want: "'claude' '--resume' '01234567-89ab-cdef-0123-456789abcdef'"},
		{agent: vendors.AgentCodex, sessionID: "01234567-89ab-cdef-0123-456789abcdef", want: "'codex' 'resume' '01234567-89ab-cdef-0123-456789abcdef'"},
		{agent: vendors.AgentOpenCode, sessionID: "ses_0123456789abcdef", want: "'opencode' '--session' 'ses_0123456789abcdef'"},
	}
	for _, test := range tests {
		t.Run(test.agent, func(t *testing.T) {
			got, err := remoteCLICommand(test.agent, test.sessionID, ResumeSession, "")
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("remoteCLICommand() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestGrokLaunchAndResumeArguments(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	cli, err := cliName(vendors.AgentGrok)
	if err != nil {
		t.Fatal(err)
	}
	if cli != "grok" {
		t.Fatalf("cliName() = %q, want grok", cli)
	}
	got, err := resumeArguments(vendors.AgentGrok, cli, "01a0f8c9-e0fa-7ec0-a5bf-79da860d539a")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"grok", "--resume", "01a0f8c9-e0fa-7ec0-a5bf-79da860d539a"}; !slices.Equal(got, want) {
		t.Fatalf("resumeArguments() = %q, want %q", got, want)
	}
	if slices.Contains(got, "-s") || slices.Contains(got, "--session-id") {
		t.Fatalf("resumeArguments() = %q uses a new-session id flag", got)
	}
	t.Setenv("GROK_HOME", "")
	if command, _, err := cliCommand(vendors.AgentGrok, "", NewSession, ""); err != nil || command != localCommandJoin("grok") {
		t.Fatalf("cliCommand(NewSession) = %q, %v, want grok alone", command, err)
	}
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	id := "01a0f8c9-e0fa-7ec0-a5bf-79da860d539a"
	if command, _, err := cliCommand(vendors.AgentGrok, "", NewSession, ""); err != nil || command != grokHomeCommand(home, localCommandJoin("grok")) {
		t.Fatalf("cliCommand(NewSession) = %q, %v", command, err)
	}
	if command, _, err := cliCommand(vendors.AgentGrok, id, ResumeSession, ""); err != nil || command != grokHomeCommand(home, localCommandJoin("grok", "--resume", id)) {
		t.Fatalf("cliCommand(ResumeSession) = %q, %v", command, err)
	}
}

func TestGrokHomePrefixQuotesTheStorePath(t *testing.T) {
	if got, want := posixGrokHomePrefix("/tmp/o'brien"), "GROK_HOME='/tmp/o'\\''brien' "; got != want {
		t.Fatalf("posix prefix = %q, want %q", got, want)
	}
	got := windowsGrokHomeCommand(`C:\Users\o'brien`, "& 'grok'")
	for _, want := range []string{
		"$hadGrokHome = Test-Path Env:GROK_HOME",
		"$env:GROK_HOME = 'C:\\Users\\o''brien'",
		"try { & 'grok' } finally {",
		"if ($hadGrokHome) { $env:GROK_HOME = $previousGrokHome }",
		"Remove-Item Env:GROK_HOME -ErrorAction SilentlyContinue",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("windows command = %q, missing %q", got, want)
		}
	}
}
