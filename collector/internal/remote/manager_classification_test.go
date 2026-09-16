package remote

import (
	"errors"
	"testing"
)

func TestClassifyErrorRecognizesInteractiveAuthenticationMethods(t *testing.T) {
	for _, message := range []string{
		"Permission denied (password).",
		"Permission denied (keyboard-interactive).",
	} {
		t.Run(message, func(t *testing.T) {
			if got := classifyError(errors.New(message)); got != ReasonAuthentication {
				t.Fatalf("classifyError() = %q, want %q", got, ReasonAuthentication)
			}
		})
	}
}

func TestClassifyErrorDistinguishesChangedAndUnknownHostKeys(t *testing.T) {
	tests := []struct {
		message string
		want    Reason
	}{
		{"Offending ECDSA key in /home/me/.ssh/known_hosts:4", ReasonHostKeyChanged},
		{"No host key is known for agent-box and you have requested strict checking", ReasonHostKeyConfirmation},
		{"ssh: Could not resolve hostname offending-box: Name or service not known", ReasonConnectionFailed},
	}
	for _, test := range tests {
		if got := classifyError(errors.New(test.message)); got != test.want {
			t.Errorf("classifyError(%q) = %q, want %q", test.message, got, test.want)
		}
	}
}
