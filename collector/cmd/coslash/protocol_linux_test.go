//go:build linux

package main

import (
	"testing"
)

func TestLinuxProtocolDesktopEntryKeepsExecutableAndScheme(t *testing.T) {
	executable := `/home/example/Co Slash/bin/coslash%nightly`
	entry := linuxProtocolDesktopEntry(executable)
	if got := linuxProtocolExecutable(entry); got != executable {
		t.Fatalf("desktop executable = %q, want %q", got, executable)
	}
	if !linuxDesktopClaimsCoslash(entry) {
		t.Fatalf("desktop entry does not claim coslash: %s", entry)
	}
}

func TestLinuxProtocolDefaultReadsDefaultApplicationsOnly(t *testing.T) {
	contents := `[Added Associations]
x-scheme-handler/coslash=old.desktop;
[Default Applications]
x-scheme-handler/coslash=io.coslash.local.desktop;other.desktop;
`
	if got := linuxProtocolDefaultInMimeApps(contents); got != "io.coslash.local.desktop" {
		t.Fatalf("default desktop = %q", got)
	}
}
