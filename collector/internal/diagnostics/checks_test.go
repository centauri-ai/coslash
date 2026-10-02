package diagnostics

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors/pi"
)

func TestCursorDiagnosticsTreatEitherLaneAsInstalled(t *testing.T) {
	for _, source := range []Source{
		{Agent: "cursor", Label: "Cursor", State: SourceOK, Entries: 1, Sessions: 1, CLI: CLI{Name: "agent"}, IDE: &CLI{Name: "cursor", Found: true}},
		{Agent: "cursor", Label: "Cursor", State: SourceOK, Entries: 1, Sessions: 1, CLI: CLI{Name: "agent", Found: true}, IDE: &CLI{Name: "cursor"}},
	} {
		for _, check := range derive(&Snapshot{Sources: []Source{source}}) {
			if strings.HasPrefix(check.ID, "cli.cursor") {
				t.Fatalf("unexpected Cursor CLI warning with an installed lane: %#v", check)
			}
		}
	}
}

func TestPiNewReleaseDoesNotHideExtensionRecovery(t *testing.T) {
	for _, test := range []struct {
		version            string
		installed, restart bool
		status             Status
		fix                string
	}{
		{"1.0.0", true, false, StatusOK, ""},
		{"1.1.0", true, false, StatusWarn, ""},
		{"1.1.0", false, false, StatusWarn, "Restart coSlash to install the extension, then restart Pi."},
		{"1.1.0", true, true, StatusWarn, "Restart Pi to load the current extension."},
		{"0.99.0", true, false, StatusWarn, "Install a stable Pi release at least 0.99.1, then restart Pi."},
		{"unknown", true, false, StatusWarn, "Install a stable Pi release at least 0.99.1, then restart Pi."},
	} {
		check := piExtensionCheck(&Snapshot{
			piExtension: pi.ExtensionHealth{Installed: test.installed, RestartRequired: test.restart},
			Sources:     []Source{{Agent: "pi", CLI: CLI{Found: true, Version: test.version}}},
		})
		if check.Status != test.status || check.Fix != test.fix {
			t.Fatalf("%+v: %+v", test, check)
		}
		if test.version == "1.1.0" && test.installed && !test.restart && !strings.Contains(check.Detail, "not yet tested") {
			t.Fatalf("new release must be untested, not incompatible: %+v", check)
		}
	}
}

func TestPiMissingCLIReportsManagedInstallSearch(t *testing.T) {
	source := Source{Agent: "pi", Label: "Pi", State: SourceOK, Entries: 1, CLI: CLI{Name: "pi"}}
	for _, check := range derive(&Snapshot{Sources: []Source{source}}) {
		if check.ID == "cli.pi" {
			if !strings.Contains(check.Detail, "managed installation") {
				t.Fatalf("missing Pi CLI detail = %q", check.Detail)
			}
			return
		}
	}
	t.Fatal("missing Pi CLI check")
}

func TestCursorDiagnosticsWarnWhenBothLanesAreMissing(t *testing.T) {
	source := Source{Agent: "cursor", Label: "Cursor", State: SourceOK, Entries: 1, Sessions: 1, CLI: CLI{Name: "agent"}, IDE: &CLI{Name: "cursor"}}
	for _, check := range derive(&Snapshot{Sources: []Source{source}}) {
		if check.ID == "cli.cursor" && strings.Contains(check.Detail, "Neither Cursor IDE nor agent CLI") {
			return
		}
	}
	t.Fatal("missing combined Cursor launch-tool warning")
}

func TestCursorEmptySourceNamesBothLanes(t *testing.T) {
	check := sourceCheck(Source{Agent: "cursor", Label: "Cursor", Root: "/cursor", State: SourceEmpty, CLI: CLI{Name: "agent"}, IDE: &CLI{Name: "cursor"}}, "darwin")
	if !strings.Contains(check.Fix, "Cursor IDE") || !strings.Contains(check.Fix, "agent") {
		t.Fatalf("fix = %q, want both Cursor lanes", check.Fix)
	}
}

func TestWindowsUnreadableSourceUsesNativeGuidance(t *testing.T) {
	check := sourceCheck(Source{Label: "Codex", Root: `C:\Users\me\.codex\sessions`, State: SourceUnreadable, Error: "access denied"}, "windows")
	if strings.Contains(check.Fix, "ls -la") || !strings.Contains(check.Fix, "Windows account") {
		t.Fatalf("fix = %q, want Windows access guidance", check.Fix)
	}
}

func TestCursorIDEExecutableFindsApplicationBundle(t *testing.T) {
	t.Setenv("PATH", "")
	home := t.TempDir()
	path := filepath.Join(home, "Applications", "Cursor.app", "Contents", "MacOS", "Cursor")
	if runtime.GOOS == "windows" {
		path = filepath.Join(home, "AppData", "Local", "Programs", "cursor", "Cursor.exe")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := cursorIDEExecutable(home); got != path {
		t.Fatalf("Cursor executable = %q, want %q", got, path)
	}
}

func TestCursorIDEExecutableSkipsNonExecutableBundleBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows executables do not use Unix execute bits")
	}
	t.Setenv("PATH", "")
	home := t.TempDir()
	path := filepath.Join(home, "Applications", "Cursor.app", "Contents", "MacOS", "Cursor")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := cursorIDEExecutable(home); got == path {
		t.Fatalf("Cursor executable = %q, want non-executable candidate skipped", got)
	}
}

func TestUnsupportedPiSynthesisOffersSupportedBackend(t *testing.T) {
	snapshot := &Snapshot{piSynthesisUnsupported: true, Synthesis: Synthesis{Enabled: true}}
	for _, check := range derive(snapshot) {
		if check.ID == "synthesis" {
			if check.Status != StatusWarn || check.Detail != "Pi synthesis is unavailable on this platform." || check.Fix != "Open Settings and choose another synthesis backend." {
				t.Fatalf("wrong platform recovery: %#v", check)
			}
			return
		}
	}
	t.Fatal("missing synthesis check")
}

func TestGrokSourceLabel(t *testing.T) {
	if got := sourceLabel("grok"); got != "Grok" {
		t.Fatalf("sourceLabel(grok) = %q, want Grok", got)
	}
}
