package main

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestWakeIntentURLGrammar(t *testing.T) {
	valid := "coslash://wake?hub=https%3A%2F%2Fhub.coslash.io"
	intent, err := parseWakeIntentURL(valid)
	if err != nil || intent.HubURL.String() != "https://hub.coslash.io" {
		t.Fatalf("wake intent=%#v error=%v", intent, err)
	}
	for _, raw := range []string{
		"coslash://wake?hub=http%3A%2F%2Flocalhost",
		"coslash://wake?hub=https%3A%2F%2Fhub.coslash.io%3F",
		valid + "&extra=1",
		"coslash://wake?hub=https%3A%2F%2Fhub.coslash.io&hub=https%3A%2F%2Fhub.coslash.io",
		"coslash://wake?hub=https%3A%2F%2Fhub.coslash.io#fragment",
		"coslash://wake/path?hub=https%3A%2F%2Fhub.coslash.io",
		"coslash://unknown?hub=https%3A%2F%2Fhub.coslash.io",
		"coslash://wake?unknown=https%3A%2F%2Fhub.coslash.io",
		"coslash://wake?hub=https%3A%2F%2Fhub.coslash.io&" + strings.Repeat("x", 500),
		"coslash://wake?hub=" + url.QueryEscape("https://"+strings.Repeat("a", 510)+".example"),
	} {
		if _, err := parseWakeIntentURL(raw); err == nil {
			t.Fatalf("parseWakeIntentURL accepted %q", raw)
		}
	}
}

func TestWakeIntentIsIgnoredWithoutMatchingStoredHub(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	manager := newOnboardingManager("0.1.0")
	defer manager.Close()
	if err := manager.RequestWake("https://hub.coslash.io"); !errors.Is(err, errForeignWakeHub) {
		t.Fatalf("wake without stored Hub error=%v", err)
	}
}
