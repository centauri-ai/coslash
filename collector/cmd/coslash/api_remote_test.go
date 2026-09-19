package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/settings"
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

func TestRemoteAuthStartReportsProbeFailureReason(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	tests := []struct {
		name, code, copy string
		err              error
	}{
		{name: "connection", err: errors.New("connection refused"), code: string(remote.ReasonConnectionFailed), copy: "not reachable"},
		{name: "timeout", err: context.DeadlineExceeded, code: string(remote.ReasonRefreshTimeout), copy: "timed out"},
		{name: "changed key", err: errors.New("REMOTE HOST IDENTIFICATION HAS CHANGED"), code: string(remote.ReasonHostKeyChanged), copy: "host key changed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := remote.NewManager(remote.Options{
				Open: func(context.Context, string, remote.OpenOptions) (*remote.Session, error) {
					return nil, test.err
				},
			})
			request := httptest.NewRequest(http.MethodPost, "/api/remote/auth/start", bytes.NewBufferString(`{"sshAlias":"agent-box"}`))
			response := httptest.NewRecorder()
			handleRemoteAuthStart(response, request, manager, settings.Open())

			if response.Code != http.StatusConflict {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
			}
			var body apiErrorBody
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Code != test.code || !strings.Contains(strings.ToLower(body.Error), test.copy) {
				t.Fatalf("error = %#v, want code %q containing %q", body, test.code, test.copy)
			}
		})
	}
}
