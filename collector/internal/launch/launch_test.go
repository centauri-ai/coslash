package launch

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

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
