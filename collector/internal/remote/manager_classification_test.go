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
