package remote

import (
	"slices"
	"testing"
)

func TestHandoffSSHArgsUseParsedDestination(t *testing.T) {
	args, err := handoffSSHArgs("developer@agent-box", "true", 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-l", "developer", "agent-box", "true"}
	if got := args[len(args)-len(want):]; !slices.Equal(got, want) {
		t.Fatalf("destination arguments = %q, want %q", got, want)
	}
}
