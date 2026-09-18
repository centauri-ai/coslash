package launch

import "testing"

func TestWindowsRemoteSSHCommandOmitsMultiplexing(t *testing.T) {
	got := remoteSSHCommand("agent-box", "cd '/repo' && 'codex'")
	want := `& 'ssh' '-tt' 'agent-box' 'cd ''/repo'' && ''codex'''`
	if got != want {
		t.Fatalf("remote SSH command = %q, want %q", got, want)
	}
}
