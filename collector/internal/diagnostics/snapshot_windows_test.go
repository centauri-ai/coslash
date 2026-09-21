//go:build windows

package diagnostics

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func TestPlatformSnapshotReportsWindowsTerminalAvailability(t *testing.T) {
	originalAvailable := terminalAvailable
	t.Cleanup(func() { terminalAvailable = originalAvailable })
	tests := []struct {
		name      string
		terminal  string
		available bool
	}{
		{name: "available", terminal: settings.TerminalWindows, available: true},
		{name: "unavailable", terminal: settings.TerminalWindows, available: false},
		{name: "invalid terminal", terminal: "invalid", available: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			terminalAvailable = func(terminal string) bool {
				if terminal != test.terminal {
					t.Fatalf("availability probe terminal = %q, want %q", terminal, test.terminal)
				}
				return test.available
			}
			platform := platformSnapshot(test.terminal)
			if platform.OS != "windows" || platform.TerminalLaunchSupported != test.available {
				t.Fatalf("platform snapshot = %#v, want availability %v", platform, test.available)
			}
		})
	}
}
