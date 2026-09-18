package launch

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsPowerShell51UnicodeConsoleSmoke(t *testing.T) {
	powerShell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("Windows PowerShell 5.1 is unavailable")
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "handoff-🦖.txt")
	output := filepath.Join(dir, "captured-🦖.txt")
	temporaryOutput := output + ".tmp"
	want := "Unicode handoff: 卡尔文 🦖"
	if err := os.WriteFile(input, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "[string]$handoff = Get-Content -Raw -Encoding UTF8 -LiteralPath " + powerShellQuote(input) + "; " +
		"$result = [ordered]@{version=$PSVersionTable.PSVersion.Major; inputRedirected=[Console]::IsInputRedirected; " +
		"outputRedirected=[Console]::IsOutputRedirected; handoff=$handoff} | ConvertTo-Json -Compress; " +
		"[IO.File]::WriteAllText(" + powerShellQuote(temporaryOutput) + ", $result, [Text.UTF8Encoding]::new($false)); " +
		"[IO.File]::Move(" + powerShellQuote(temporaryOutput) + ", " + powerShellQuote(output) + "); exit"
	originalLookPath := windowsLookPath
	t.Cleanup(func() { windowsLookPath = originalLookPath })
	windowsLookPath = func(name string) (string, error) {
		if name == "wt.exe" {
			return "", errors.New("not found")
		}
		return powerShell, nil
	}
	if err := openWindowsTerminal(dir, script); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var data []byte
	for {
		data, err = os.ReadFile(output)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) || time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
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
