package launch

import (
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestRemoteCLICommandUsesRemoteHandoffFile(t *testing.T) {
	command, err := remoteCLICommand(vendors.AgentClaude, "", NewSession, "keep this private")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, "mktemp") || !strings.Contains(command, "base64 -d") ||
		!strings.Contains(command, "'--append-system-prompt-file' \"$handoff\"") {
		t.Fatalf("command = %q", command)
	}
	if strings.Contains(command, "keep this private") {
		t.Fatalf("command contains the raw handoff: %q", command)
	}
}

func TestRemoteCodexCLICommandReadsRemoteHandoffFile(t *testing.T) {
	command, err := remoteCLICommand(vendors.AgentCodex, "", NewSession, "keep this private")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, `developer_instructions=$(cat "$handoff")`) || strings.Contains(command, `cat \"$handoff\"`) {
		t.Fatalf("command = %q", command)
	}
}

func TestRemoteCLICommandResumesValidatedSession(t *testing.T) {
	command, err := remoteCLICommand(vendors.AgentCodex, "01234567-89ab-cdef-0123-456789abcdef", ResumeSession, "")
	if err != nil {
		t.Fatal(err)
	}
	if command != "'codex' 'resume' '01234567-89ab-cdef-0123-456789abcdef'" {
		t.Fatalf("command = %q", command)
	}
}
