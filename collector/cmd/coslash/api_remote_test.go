package main

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/remote"
)

func TestMachineAuthenticationContract(t *testing.T) {
	reason := remote.ReasonAuthentication
	machine := machineFromHealth(remote.Health{Label: "agent-box", State: remote.StateStale, Reason: &reason})
	if machine.ActionRequired != remote.ActionAuthenticate || machine.AuthState != remote.AuthRequired {
		t.Fatalf("authentication contract = %q/%q", machine.ActionRequired, machine.AuthState)
	}
	changed := remote.ReasonHostKeyChanged
	machine = machineFromHealth(remote.Health{Label: "agent-box", State: remote.StateError, Reason: &changed})
	if machine.ActionRequired != remote.ActionVerifyHostKey || machine.AuthState != remote.AuthNotRequired {
		t.Fatalf("changed-key contract = %q/%q", machine.ActionRequired, machine.AuthState)
	}
}
