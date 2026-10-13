//go:build !windows

package launch

import (
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func TestRemoteSSHCommandReusesControlSocket(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	destination, err := settings.ParseSSHDestination("agent-box")
	if err != nil {
		t.Fatal(err)
	}
	command, err := remoteSSHCommand(destination, "true")
	if err != nil {
		t.Fatal(err)
	}
	controlPath, err := settings.SSHControlPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, "'ControlMaster=auto'") ||
		!strings.Contains(command, "'ControlPath="+controlPath+"'") {
		t.Fatalf("command = %q", command)
	}
}
