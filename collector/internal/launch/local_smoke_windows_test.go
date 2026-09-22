package launch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPowerShell51UnicodeConsoleSmoke(t *testing.T) {
	powerShell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("Windows PowerShell 5.1 is unavailable")
	}
	dir := t.TempDir()
	t.Setenv("COSLASH_HOME", filepath.Join(dir, "coslash-home"))
	input := filepath.Join(dir, "handoff-🦖.txt")
	output := filepath.Join(dir, "captured-🦖.txt")
	errorOutput := filepath.Join(dir, "error.txt")
	temporaryOutput := output + ".tmp"
	want := "Unicode handoff: 卡尔文 🦖"
	if err := os.WriteFile(input, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "try { [string]$handoff = Get-Content -Raw -Encoding UTF8 -LiteralPath " + powerShellQuote(input) + "; " +
		"$result = [ordered]@{version=$PSVersionTable.PSVersion.Major; inputRedirected=[Console]::IsInputRedirected; " +
		"outputRedirected=[Console]::IsOutputRedirected; handoff=$handoff} | ConvertTo-Json -Compress; " +
		"[IO.File]::WriteAllText(" + powerShellQuote(temporaryOutput) + ", $result, [Text.UTF8Encoding]::new($false)); " +
		"[IO.File]::Move(" + powerShellQuote(temporaryOutput) + ", " + powerShellQuote(output) + ") } " +
		"catch { [IO.File]::WriteAllText(" + powerShellQuote(errorOutput) + ", $_.Exception.ToString()); exit 1 }"
	originalLookPath, originalClose := windowsLookPath, windowsCloseHandle
	t.Cleanup(func() { windowsLookPath, windowsCloseHandle = originalLookPath, originalClose })
	windowsLookPath = func(name string) (string, error) {
		if name == "wt.exe" {
			return "", errors.New("not found")
		}
		return powerShell, nil
	}
	var process windows.Handle
	windowsCloseHandle = func(handle windows.Handle) error {
		if process == 0 {
			process = handle
			return nil
		}
		return windows.CloseHandle(handle)
	}
	if err := openWindowsTerminalMode(context.Background(), dir, script, false); err != nil {
		t.Fatal(err)
	}
	if process == 0 {
		t.Fatal("PowerShell process handle was not returned")
	}
	status, err := windows.WaitForSingleObject(process, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if status != windows.WAIT_OBJECT_0 {
		t.Fatalf("PowerShell completion wait status = %#x", status)
	}
	_ = windows.CloseHandle(process)
	data, err := os.ReadFile(output)
	if err != nil {
		diagnostic, _ := os.ReadFile(errorOutput)
		t.Fatalf("%v: %s", err, diagnostic)
	}
	var got struct {
		Version          int    `json:"version"`
		InputRedirected  bool   `json:"inputRedirected"`
		OutputRedirected bool   `json:"outputRedirected"`
		Handoff          string `json:"handoff"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode PowerShell result %q: %v", data, err)
	}
	if got.Version != 5 || got.InputRedirected || got.OutputRedirected || got.Handoff != want {
		t.Fatalf("PowerShell result = %#v", got)
	}
}
