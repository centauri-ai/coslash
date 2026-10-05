//go:build windows

package diagnostics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/grok"
)

func TestWindowsGrokDiagnosticsSourceStates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	state := func() SourceState {
		return collectSource(context.Background(), home, grok.Health(), false).State
	}
	if got := state(); got != SourceMissing {
		t.Fatalf("missing source = %q", got)
	}
	root := filepath.Join(home, "sessions")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := state(); got != SourceEmpty {
		t.Fatalf("empty source = %q", got)
	}
	group := filepath.Join(root, "C%3A%5Cwork%5Crepo", "session")
	if err := os.MkdirAll(group, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(group, "summary.json"), []byte(`{"info":{"id":"session","cwd":"C:\\work\\repo"},"chat_format_version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := state(); got != SourceOK {
		t.Fatalf("found source = %q", got)
	}
	unreadableHome := t.TempDir()
	t.Setenv("GROK_HOME", unreadableHome)
	if err := os.WriteFile(filepath.Join(unreadableHome, "sessions"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := state(); got != SourceUnreadable {
		t.Fatalf("unreadable source = %q", got)
	}
}

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
