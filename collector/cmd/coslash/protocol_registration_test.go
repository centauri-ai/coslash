package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/diagnostics"
)

type fakeProtocolHandlerBackend struct {
	registrations map[string]string
	registration  protocolHandlerRegistration
	stalePaths    []string
	registered    []protocolHandlerRecord
	unregistered  []string
}

func (fake *fakeProtocolHandlerBackend) Register(executable, _ string, previous protocolHandlerRecord) (string, error) {
	fake.registered = append(fake.registered, previous)
	if previous.Path != "" {
		delete(fake.registrations, previous.Path)
	}
	path := "fake://protocol-handler/" + filepath.Base(executable)
	if fake.registrations == nil {
		fake.registrations = make(map[string]string)
	}
	fake.registrations[path] = executable
	return path, nil
}

func (fake *fakeProtocolHandlerBackend) Status(_, _ string) (protocolHandlerRegistration, error) {
	return fake.registration, nil
}

func (fake *fakeProtocolHandlerBackend) StaleHandlers(_, _ string) ([]string, error) {
	return append([]string(nil), fake.stalePaths...), nil
}

func (fake *fakeProtocolHandlerBackend) Unregister(path string) error {
	fake.unregistered = append(fake.unregistered, path)
	return nil
}

func useFakeProtocolHandlerBackend(t *testing.T, fake *fakeProtocolHandlerBackend) {
	t.Helper()
	previous := activeProtocolHandlerBackend
	activeProtocolHandlerBackend = fake
	t.Cleanup(func() { activeProtocolHandlerBackend = previous })
}

func setProtocolHandlerTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_HOME", filepath.Join(home, ".coslash"))
	return home
}

func TestRegisterProtocolHandlerGuardsTemporaryHomeAndAllowsExplicitTestOverride(t *testing.T) {
	setProtocolHandlerTestHome(t)
	fake := &fakeProtocolHandlerBackend{}
	useFakeProtocolHandlerBackend(t, fake)
	t.Setenv(protocolHandlerTestHomeOverride, "")

	if err := registerProtocolHandler(); err == nil {
		t.Fatal("registerProtocolHandler() accepted a temporary HOME")
	}
	if len(fake.registered) != 0 {
		t.Fatalf("registrar called for temporary HOME: %#v", fake.registered)
	}

	t.Setenv(protocolHandlerTestHomeOverride, "1")
	if err := registerProtocolHandler(); err != nil {
		t.Fatal(err)
	}
	if len(fake.registered) != 1 {
		t.Fatalf("registrar calls = %d, want one", len(fake.registered))
	}
	contents, err := os.ReadFile(protocolHandlerRecordPath())
	if err != nil {
		t.Fatal(err)
	}
	var record protocolHandlerRecord
	if err := json.Unmarshal(contents, &record); err != nil {
		t.Fatal(err)
	}
	executable, err := currentExecutablePath()
	if err != nil {
		t.Fatal(err)
	}
	if record.Executable != executable || record.Path == "" {
		t.Fatalf("record = %#v, want current executable %q and handler path", record, executable)
	}
}

func TestRegistrationPassesPreviousPathAndReRegistersMarkerMismatch(t *testing.T) {
	setProtocolHandlerTestHome(t)
	t.Setenv(protocolHandlerTestHomeOverride, "1")
	fake := &fakeProtocolHandlerBackend{registrations: map[string]string{"/old/handler.app": "/old/coslash"}}
	useFakeProtocolHandlerBackend(t, fake)
	if err := writeProtocolHandlerRecord(protocolHandlerRecord{Executable: "/old/coslash", Path: "/old/handler.app"}); err != nil {
		t.Fatal(err)
	}
	if err := registerProtocolHandler(); err != nil {
		t.Fatal(err)
	}
	if len(fake.registered) != 1 || fake.registered[0] != (protocolHandlerRecord{Executable: "/old/coslash", Path: "/old/handler.app"}) {
		t.Fatalf("previous registration passed to registrar = %#v", fake.registered)
	}
	if len(fake.registrations) != 1 {
		t.Fatalf("registrations after replacement = %#v, want only current registration", fake.registrations)
	}
	for _, executable := range fake.registrations {
		current, err := currentExecutablePath()
		if err != nil {
			t.Fatal(err)
		}
		if executable != current {
			t.Fatalf("registered executable = %q, want current %q", executable, current)
		}
	}
}

func TestUnregisterPreviousProtocolHandlerLeavesCurrentPath(t *testing.T) {
	var unregistered []string
	if err := unregisterPreviousProtocolHandler("/old/handler.app", "/new/handler.app", func(path string) error {
		unregistered = append(unregistered, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(unregistered) != 1 || unregistered[0] != "/old/handler.app" {
		t.Fatalf("unregistered paths = %v", unregistered)
	}
	if err := unregisterPreviousProtocolHandler("/same/handler.app", "/same/handler.app", func(path string) error {
		t.Fatalf("unregistered current path %q", path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := unregisterPreviousProtocolHandler("/old/handler.app", "/new/handler.app", func(string) error {
		return errors.New("unregister failed")
	}); err == nil {
		t.Fatal("unregister failure was ignored")
	}
}

func TestProtocolHandlerDoctorStates(t *testing.T) {
	setProtocolHandlerTestHome(t)
	fake := &fakeProtocolHandlerBackend{}
	useFakeProtocolHandlerBackend(t, fake)
	current, err := currentExecutablePath()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name         string
		registration protocolHandlerRegistration
		want         string
		status       diagnostics.Status
	}{
		{name: "this copy", registration: protocolHandlerRegistration{Registered: true, Executable: current}, want: "registered (this copy)", status: diagnostics.StatusOK},
		{name: "another copy", registration: protocolHandlerRegistration{Registered: true, Executable: "/old/coslash"}, want: "registered to another copy at /old/coslash", status: diagnostics.StatusWarn},
		{name: "missing", want: "not registered", status: diagnostics.StatusWarn},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake.registration = test.registration
			snapshot := &diagnostics.Snapshot{}
			addProtocolHandlerDoctorCheck(snapshot)
			if len(snapshot.Checks) != 1 || snapshot.Checks[0].Detail != test.want || snapshot.Checks[0].Status != test.status {
				t.Fatalf("doctor link check = %#v, want detail %q and status %q", snapshot.Checks, test.want, test.status)
			}
		})
	}
}

func TestFixLinksPrintsPlanAndRequiresYes(t *testing.T) {
	setProtocolHandlerTestHome(t)
	fake := &fakeProtocolHandlerBackend{stalePaths: []string{"/old/handler.app", "/missing/handler.app"}}
	useFakeProtocolHandlerBackend(t, fake)
	var stdout, stderr bytes.Buffer
	if code := planProtocolHandlerCleanup(&stdout, &stderr, false); code != 0 {
		t.Fatalf("plan code = %d, stderr = %q", code, stderr.String())
	}
	if len(fake.unregistered) != 0 || !bytes.Contains(stdout.Bytes(), []byte("Will unregister: /old/handler.app")) || !bytes.Contains(stdout.Bytes(), []byte("--fix-links --yes")) {
		t.Fatalf("dry-run output = %q; unregister calls = %v", stdout.String(), fake.unregistered)
	}
	stdout.Reset()
	if code := planProtocolHandlerCleanup(&stdout, &stderr, true); code != 0 {
		t.Fatalf("apply code = %d, stderr = %q", code, stderr.String())
	}
	if len(fake.unregistered) != 2 || fake.unregistered[0] != "/old/handler.app" || fake.unregistered[1] != "/missing/handler.app" {
		t.Fatalf("unregistered = %v", fake.unregistered)
	}
}

func TestAutomaticProtocolRegistrationPreservesOtherHomeAndBinary(t *testing.T) {
	home := setProtocolHandlerTestHome(t)
	t.Setenv("COSLASH_HOME", filepath.Join(home, "dev"))
	t.Setenv(protocolHandlerTestHomeOverride, "1")
	fake := &fakeProtocolHandlerBackend{registration: protocolHandlerRegistration{Registered: true, Executable: "/release/coslash"}}
	useFakeProtocolHandlerBackend(t, fake)
	if err := ensureProtocolHandler(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COSLASH_HOME", filepath.Join(home, ".coslash"))
	if err := ensureProtocolHandler(); err != nil {
		t.Fatal(err)
	}
	if len(fake.registered) != 0 {
		t.Fatal("automatic registration replaced another Local")
	}
}
