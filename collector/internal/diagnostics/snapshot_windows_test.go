//go:build windows

package diagnostics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestGrokDiagnosticsFindsCLIOutsidePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GROK_HOME", home)
	bin := filepath.Join(home, "bin", "grok.exe")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	source := collectSource(context.Background(), home, vendors.SourceHealth{Agent: vendors.AgentGrok}, false)
	if !source.CLI.Found || source.CLI.Path != displayPath(home, bin) {
		t.Fatalf("Grok CLI = %+v, want %q", source.CLI, displayPath(home, bin))
	}
}

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
