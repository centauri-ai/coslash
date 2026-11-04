package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

const switchAttemptID = "10000000-0000-4000-8000-000000000301"

// switchFixture saves Hub A, gives this computer the listed credentials, and
// returns Hub B as a loopback server that answers every request with 404.
func switchFixture(t *testing.T, credentials map[string]string) (manager *onboardingManager, hubA, hubB string, shown *int) {
	t.Helper()
	t.Setenv("COSLASH_HOME", t.TempDir())
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	hubA, hubB = "https://hub-a.coslash.io", server.URL
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
	if err := manager.StartPairing(hubB, switchAttemptID, strings.Repeat("A", 43)); err != nil {
		t.Fatal(err)
	}
	if storedHub(t) != hubB || manager.PendingSwitch() != nil || *shown != 0 {
		t.Fatalf("stored=%q pending=%+v shown=%d", storedHub(t), manager.PendingSwitch(), *shown)
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
