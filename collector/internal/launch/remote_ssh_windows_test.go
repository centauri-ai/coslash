package launch

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func TestWindowsRemoteSSHCommandDisablesConfiguredMultiplexing(t *testing.T) {
	destination, err := settings.ParseSSHDestination("agent-box")
	if err != nil {
		t.Fatal(err)
	}
	got := remoteSSHCommand(destination, "cd '/repo' && 'codex'")
	want := `& 'ssh' '-tt' '-o' 'ControlMaster=no' 'agent-box' 'cd ''/repo'' && ''codex'''`
	if got != want {
		t.Fatalf("remote SSH command = %q, want %q", got, want)
	}
}

func TestWindowsSSHAuthenticationSetsCoSlashHomeInPowerShell(t *testing.T) {
	t.Setenv("COSLASH_HOME", `C:\Users\calvin\AppData\Local\coSlash`)
	got := sshAuthenticationCommand(`C:\Program Files\coSlash\coslash.exe`, "0123456789abcdef0123456789abcdef")
	want := `$env:COSLASH_HOME = 'C:\Users\calvin\AppData\Local\coSlash'; & 'C:\Program Files\coSlash\coslash.exe' 'ssh-auth' '0123456789abcdef0123456789abcdef'`
	if got != want {
		t.Fatalf("authentication command = %q, want %q", got, want)
	}
}
