//go:build !windows

package opencode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeleteRepairProcessPathWithSpaces(t *testing.T) {
	got := parseTUIProcesses("123 Thu Oct 1 10:00:00 2026 opencode /tmp/Vendor App/opencode\n")
	if len(got) == 0 {
		t.Fatal("fresh process probe omitted default native TUI whose executable path contains a space")
	}
}

func TestDeleteRepairCapturedFreshProcessPath(t *testing.T) {
	data := "69513 Thu Oct  1 14:32:52 2026 opencode /tmp/synthetic/Vendor App/opencode\n"
	if got := parseTUIProcesses(data); len(got) == 0 {
		t.Fatal("captured native executable path with spaces was omitted")
	}
}

func TestDeleteRepairProcessBounds(t *testing.T) {
	if got := parseOpenCodeProcesses(strings.Repeat("x", 4<<20)+"/opencode", true); len(got) == 0 {
		t.Fatal("oversized process output treated as closed")
	}
	if got := parseOpenCodeProcesses("bad-date /tmp/Vendor App/opencode\n", true); len(got) == 0 {
		t.Fatal("malformed relevant process treated as closed")
	}
	if got := parseTUIProcesses(strings.Repeat("x", 65537)); len(got) != 0 {
		t.Fatal("oversized snapshot became interactive")
	}
}

func TestUnixProcessExecutableBoundary(t *testing.T) {
	prefix := "123 Thu Oct 1 10:00:00 2026 "
	for _, command := range []string{"tail /usr/bin/tail -f /tmp/opencode", "cat /usr/bin/cat /tmp/opencode --session ses_neighbor", "cat cat opencode", "cat /usr/bin/cat relative/opencode", "python /usr/bin/python /tmp/opencode"} {
		if got := parseTUIProcesses(prefix + command); len(got) != 0 {
			t.Fatalf("argument became an interactive executable: %q", command)
		}
		if got := parseOpenCodeProcesses(prefix+command, true); len(got) != 0 {
			t.Fatalf("known argument became a native process: %q", command)
		}
	}
	for _, command := range []string{"opencode", "/tmp/Vendor App/opencode", `"/tmp/Vendor App/opencode" --session ses_native`, "/tmp/Vendor App/opencode run"} {
		got := parseOpenCodeProcesses(prefix+"opencode "+command, true)
		if len(got) != 1 || got[0].pid != 123 {
			t.Fatalf("native executable lost: %q", command)
		}
	}
}

func TestUnixMalformedNativeSnapshotIsUnverified(t *testing.T) {
	for _, row := range []string{"123 malformed opencode", "bad Thu Oct 1 10:00:00 2026 opencode opencode", "0 Thu Oct 1 10:00:00 2026 opencode opencode", "123 bad Oct 1 10:00:00 2026 opencode opencode", "123 Thu Oct 1 10:00:00 2026 opencode /tmp/Program With opencode", "123 Thu Oct 1 10:00:00 2026 opencode bash -c opencode", "123 Thu Oct 1 10:00:00 2026 /tmp/Vendor App/opencode"} {
		if got := parseOpenCodeProcesses(row, true); len(got) != 1 || got[0].pid != 0 {
			t.Fatalf("uncertain native snapshot became closure: %q", row)
		}
		if got := parseTUIProcesses(row); len(got) != 0 {
			t.Fatalf("uncertain snapshot became interactive: %q", row)
		}
	}
	if got := parseOpenCodeProcesses("\n", true); len(got) != 0 {
		t.Fatal("empty valid snapshot refused")
	}
}

func TestEffectiveDatabaseRootLookupIsIsolated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_DB", "")
	bin := t.TempDir()
	executable := filepath.Join(bin, "opencode")
	// The stand-in exercises the same environment boundary as native startup.
	script := "#!/bin/sh\n[ \"$HOME\" != \"" + home + "\" ] || exit 2\nprintf 'isolated' > \"$HOME/startup-marker\"\nprintf '%s/opencode/opencode-dev.db\\n' \"$XDG_DATA_HOME\"\n"
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	got, err := RootContext(context.Background())
	want := filepath.Join(home, ".local", "share", "opencode", "opencode-dev.db")
	if err != nil || got != want {
		t.Fatalf("root=%q error=%v, want %q", got, err, want)
	}
	if _, err := os.Stat(filepath.Join(home, "startup-marker")); !os.IsNotExist(err) {
		t.Fatal("lookup wrote into supplied home")
	}
}
