package launch

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWindowsPowerShell51UnicodeConsoleSmoke(t *testing.T) {
	powerShell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("Windows PowerShell 5.1 is unavailable")
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "handoff-🦖.txt")
	output := filepath.Join(dir, "captured-🦖.txt")
	want := "Unicode handoff: 卡尔文 🦖"
	if err := os.WriteFile(input, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "if ($PSVersionTable.PSVersion.Major -ne 5) { exit 51 }; " +
		"$handoff = Get-Content -Raw -Encoding UTF8 -LiteralPath " + powerShellQuote(input) + "; " +
		"[IO.File]::WriteAllText(" + powerShellQuote(output) + ", $handoff, [Text.UTF8Encoding]::new($false))"
	command := exec.Command(powerShell, "-NoProfile", "-Command", script)
	configureWindowsConsole(command, dir)
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("Unicode handoff = %q, want %q", got, want)
	}
}
