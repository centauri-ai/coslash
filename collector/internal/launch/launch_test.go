package launch

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestTerminalRemovesHandoffWhenTerminalOpenFails(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	originalOpener := localTerminalOpener
	t.Cleanup(func() { localTerminalOpener = originalOpener })
	wantErr := errors.New("terminal open failed")
	localTerminalOpener = func(context.Context, string, string, string) error { return wantErr }

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
