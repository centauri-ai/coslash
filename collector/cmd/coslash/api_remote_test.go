package main

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/remote"
)

func TestTerminalAuthenticationPermittedRejectsChangedHostKey(t *testing.T) {
	changed := remote.ReasonHostKeyChanged
	if terminalAuthenticationPermitted(remote.Health{Reason: &changed}) {
		t.Fatal("changed host key was accepted for terminal authentication")
	}
	confirmation := remote.ReasonHostKeyConfirmation
	if !terminalAuthenticationPermitted(remote.Health{Reason: &confirmation}) {
		t.Fatal("unknown host key was rejected for terminal confirmation")
	}
	authentication := remote.ReasonAuthentication
	if !terminalAuthenticationPermitted(remote.Health{Reason: &authentication}) {
		t.Fatal("authentication failure was rejected")
	}
}

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
