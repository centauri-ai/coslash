package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

const switchAttemptID = "10000000-0000-4000-8000-000000000301"

type credentialLoadErrorStore struct{ err error }

func (s credentialLoadErrorStore) Load(context.Context) (string, error) { return "", s.err }
func (credentialLoadErrorStore) Save(context.Context, string) error     { return nil }
func (credentialLoadErrorStore) Delete(context.Context) error           { return nil }

// switchFixture saves Hub A and gives this computer the listed credentials.
func switchFixture(t *testing.T, credentials map[string]string) (manager *onboardingManager, hubA, hubB string, shown *int) {
	t.Helper()
	t.Setenv("COSLASH_HOME", t.TempDir())
	hubA, hubB = "https://hub-a.coslash.io", "https://hub-b.coslash.io"
	if err := writeStoredHubURL(hubA); err != nil {
		t.Fatal(err)
	}
	previous := hubCredentialStore
	hubCredentialStore = func(origin string) hubclient.CredentialStore {
		return &testCredentialStore{value: credentials[origin]}
	}
	t.Cleanup(func() { hubCredentialStore = previous })
	manager = newOnboardingManager("0.1.0")
	t.Cleanup(manager.Close)
	shown = new(int)
	manager.showLocal = func() { *shown++ }
	return manager, hubA, hubB, shown
}

func storedHub(t *testing.T) string {
	t.Helper()
	stored, err := readStoredHubURL()
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestPairingFromAnotherHubWaitsForConfirmation(t *testing.T) {
	manager, hubA, hubB, shown := switchFixture(t, map[string]string{"https://hub-a.coslash.io": "credential-a"})
	token := strings.Repeat("A", 43)

	if err := manager.StartPairing(hubB, switchAttemptID, token); !errors.Is(err, errHubSwitchPending) {
		t.Fatalf("StartPairing error=%v, want pending switch", err)
	}
	pending := manager.PendingSwitch()
	if storedHub(t) != hubA || pending == nil || pending.From != hubA || pending.To != hubB || !pending.CanSwitch || *shown != 1 {
		t.Fatalf("stored=%q pending=%+v shown=%d", storedHub(t), pending, *shown)
	}

	manager.cancel()
	if err := manager.ResolveSwitch(hubB, true); err != nil {
		t.Fatal(err)
	}
	if storedHub(t) != hubB || manager.PendingSwitch() != nil {
		t.Fatalf("confirmed switch stored=%q pending=%+v", storedHub(t), manager.PendingSwitch())
	}
	if err := manager.ResolveSwitch(hubB, true); !errors.Is(err, errNoHubSwitch) {
		t.Fatalf("second resolve error=%v, want no request", err)
	}
}

func TestDeclinedHubSwitchKeepsTheSavedHub(t *testing.T) {
	manager, hubA, hubB, _ := switchFixture(t, map[string]string{"https://hub-a.coslash.io": "credential-a"})
	if err := manager.StartPairing(hubB, switchAttemptID, strings.Repeat("A", 43)); !errors.Is(err, errHubSwitchPending) {
		t.Fatal(err)
	}
	if err := manager.ResolveSwitch("https://hub-c.coslash.io", true); !errors.Is(err, errNoHubSwitch) {
		t.Fatalf("resolve for another Hub error=%v", err)
	}
	if err := manager.ResolveSwitch(hubB, false); err != nil {
		t.Fatal(err)
	}
	if storedHub(t) != hubA || manager.PendingSwitch() != nil {
		t.Fatalf("declined switch stored=%q pending=%+v", storedHub(t), manager.PendingSwitch())
	}
}

func TestPairingWithoutASavedCredentialStartsAtOnce(t *testing.T) {
	manager, _, hubB, shown := switchFixture(t, nil)
	manager.cancel()
	if err := manager.StartPairing(hubB, switchAttemptID, strings.Repeat("A", 43)); err != nil {
		t.Fatal(err)
	}
	if storedHub(t) != hubB || manager.PendingSwitch() != nil || *shown != 0 {
		t.Fatalf("stored=%q pending=%+v shown=%d", storedHub(t), manager.PendingSwitch(), *shown)
	}
}

func TestPairingRefusesToSwitchWhenSavedCredentialCannotBeRead(t *testing.T) {
	manager, hubA, hubB, shown := switchFixture(t, nil)
	storeErr := errors.New("credential store unavailable")
	previous := hubCredentialStore
	hubCredentialStore = func(origin string) hubclient.CredentialStore {
		if origin == hubA {
			return credentialLoadErrorStore{err: storeErr}
		}
		return &testCredentialStore{}
	}
	t.Cleanup(func() { hubCredentialStore = previous })

	err := manager.StartPairing(hubB, switchAttemptID, strings.Repeat("A", 43))
	if !errors.Is(err, storeErr) {
		t.Fatalf("StartPairing error=%v, want saved credential store error", err)
	}
	if storedHub(t) != hubA || manager.PendingSwitch() != nil || *shown != 0 {
		t.Fatalf("stored=%q pending=%+v shown=%d", storedHub(t), manager.PendingSwitch(), *shown)
	}
}

func TestNotPairedSavedCredentialAllowsDirectPairing(t *testing.T) {
	manager, hubA, hubB, shown := switchFixture(t, nil)
	manager.cancel()
	previous := hubCredentialStore
	hubCredentialStore = func(origin string) hubclient.CredentialStore {
		if origin == hubA {
			return credentialLoadErrorStore{err: hubclient.ErrNotPaired}
		}
		return &testCredentialStore{}
	}
	t.Cleanup(func() { hubCredentialStore = previous })

	if err := manager.StartPairing(hubB, switchAttemptID, strings.Repeat("A", 43)); err != nil {
		t.Fatal(err)
	}
	if storedHub(t) != hubB || manager.PendingSwitch() != nil || *shown != 0 {
		t.Fatalf("stored=%q pending=%+v shown=%d", storedHub(t), manager.PendingSwitch(), *shown)
	}
}

func TestFailedHubSwitchKeepsRequestForRetry(t *testing.T) {
	manager, hubA, hubB, _ := switchFixture(t, nil)
	manager.setSyncHooks(syncHookFuncs{ensure: func(*hubclient.Client) error {
		return errors.New("temporary Hub check-in failure")
	}})
	manager.askSwitch(hubSwitch{From: hubA, To: hubB, CanSwitch: true, action: "check-in"})

	for attempt := 0; attempt < 2; attempt++ {
		err := manager.ResolveSwitch(hubB, true)
		if err == nil || errors.Is(err, errNoHubSwitch) {
			t.Fatalf("ResolveSwitch attempt %d error=%v, want operation failure with pending request", attempt+1, err)
		}
		pending := manager.PendingSwitch()
		if pending == nil || pending.To != hubB || pending.From != hubA {
			t.Fatalf("ResolveSwitch attempt %d pending=%+v, want original request retained", attempt+1, pending)
		}
	}
}

func TestWakeFromAnotherHubAsksOnlyWhenThisComputerIsPaired(t *testing.T) {
	for _, test := range []struct {
		name        string
		credentialB string
		canSwitch   bool
	}{
		{name: "Hub B paired before", credentialB: "credential-b", canSwitch: true},
		{name: "Hub B never paired", canSwitch: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			credentials := map[string]string{"https://hub-a.coslash.io": "credential-a"}
			manager, hubA, hubB, shown := switchFixture(t, credentials)
			credentials[hubB] = test.credentialB
			if err := manager.RequestWake(hubB); !errors.Is(err, errForeignWakeHub) {
				t.Fatalf("RequestWake error=%v", err)
			}
			pending := manager.PendingSwitch()
			if storedHub(t) != hubA || pending == nil || pending.CanSwitch != test.canSwitch || *shown != 1 {
				t.Fatalf("stored=%q pending=%+v shown=%d", storedHub(t), pending, *shown)
			}
		})
	}
}

func TestWakeFromAnotherHubReachesLocalOnlyAfterSetup(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	const link = "coslash://wake?hub=https%3A%2F%2Fhub-b.coslash.io"
	if intent, done := handleProtocolActivation(link); intent != nil || !done {
		t.Fatalf("wake before setup intent=%+v done=%v, want ignored", intent, done)
	}
	if err := writeStoredHubURL("https://hub-a.coslash.io"); err != nil {
		t.Fatal(err)
	}
	intent, done := handleProtocolActivation(link)
	if done || intent == nil || intent.Action != "wake" || intent.HubURL.String() != "https://hub-b.coslash.io" {
		t.Fatalf("wake from another Hub intent=%+v done=%v, want it handed to Local", intent, done)
	}
}
