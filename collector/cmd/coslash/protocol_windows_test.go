//go:build windows

package main

import "testing"

func TestExecutableFromWindowsProtocolCommand(t *testing.T) {
	const executable = `C:\Users\example\coSlash\coslash.exe`
	command := `"` + executable + `" --protocol-url "%1"`
	if got := executableFromWindowsProtocolCommand(command); got != executable {
		t.Fatalf("protocol command executable = %q, want %q", got, executable)
	}
	if got := executableFromWindowsProtocolCommand(`coslash.exe --protocol-url "%1"`); got != "" {
		t.Fatalf("accepted unquoted executable: %q", got)
	}
}
