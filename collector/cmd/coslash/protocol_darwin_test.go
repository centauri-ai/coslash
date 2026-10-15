//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseLaunchServicesDumpFindsOnlyCoslashHandlers(t *testing.T) {
	dump := `bundle id: 12
path: /Users/example/old/coSlash Local Pairing Handler.app (0xabc)
claimed schemes: 1
    scheme: coslash
bundle id: 13
path: /Applications/Other.app (0xdef)
claimed schemes: https:
bundle id: 14
path: /Users/example/new/coSlash Local Pairing Handler.app
claimed schemes: coslash:
`
	handlers := parseLaunchServicesDump(dump)
	if len(handlers) != 2 {
		t.Fatalf("handlers = %#v, want two coSlash registrations", handlers)
	}
	if handlers[0].Path != "/Users/example/old/coSlash Local Pairing Handler.app" || handlers[1].Path != "/Users/example/new/coSlash Local Pairing Handler.app" {
		t.Fatalf("handler paths = %#v", handlers)
	}
}

func TestAutomaticProtocolRegistrationRefreshesRemovedHomebrewHandler(t *testing.T) {
	home := setProtocolHandlerTestHome(t)
	t.Setenv(protocolHandlerTestHomeOverride, "1")
	previousRelease, previousBranch := releaseBuildMetadata, branchBuildMetadata
	releaseBuildMetadata, branchBuildMetadata = "true", ""
	t.Cleanup(func() {
		releaseBuildMetadata, branchBuildMetadata = previousRelease, previousBranch
	})

	handlerPath := filepath.Join(home, "Library", "Application Support", "coSlash Local", macProtocolBundleName)
	marker := filepath.Join(handlerPath, "Contents", "Resources", "coslash-executable")
	oldKegExecutable := filepath.Join(home, "brew", "Cellar", "coslash", "old", "bin", "coslash")
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(oldKegExecutable), 0o600); err != nil {
		t.Fatal(err)
	}
	executable, err := currentExecutablePath()
	if err != nil {
		t.Fatal(err)
	}
	handlers := []launchServicesHandler{{Path: handlerPath}}
	fake := &fakeProtocolHandlerBackend{registration: macProtocolHandlerStatus(executable, home, handlers)}
	useFakeProtocolHandlerBackend(t, fake)
	if err := ensureProtocolHandler(); err != nil {
		t.Fatal(err)
	}
	if len(fake.registered) != 1 {
		t.Fatalf("startup registrations = %d, want one refresh", len(fake.registered))
	}

	if err := os.MkdirAll(filepath.Dir(oldKegExecutable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldKegExecutable, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	fake.registration = macProtocolHandlerStatus(executable, home, handlers)
	fake.registered = nil
	if err := ensureProtocolHandler(); err != nil {
		t.Fatal(err)
	}
	if len(fake.registered) != 0 {
		t.Fatal("startup replaced a live handler owned by another copy")
	}

	if err := os.Remove(oldKegExecutable); err != nil {
		t.Fatal(err)
	}
	releaseBuildMetadata = ""
	fake.registration = macProtocolHandlerStatus(executable, home, handlers)
	if err := ensureProtocolHandler(); err != nil {
		t.Fatal(err)
	}
	if len(fake.registered) != 0 {
		t.Fatal("a dev build replaced another copy's stale handler")
	}
}
