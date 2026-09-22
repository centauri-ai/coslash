//go:build !windows

package launch

import (
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func TestRemoteSSHCommandReusesControlSocket(t *testing.T) {
	t.Setenv("COSLASH_HOME", "/tmp/coslash-test")
	destination, err := settings.ParseSSHDestination("agent-box")
	if err != nil {
		t.Fatal(err)
	}
	command := remoteSSHCommand(destination, "true")
	if !strings.Contains(command, "'ControlMaster=auto'") ||
		!strings.Contains(command, "'ControlPath=/tmp/coslash-test/ssh/cm-%C'") {
		t.Fatalf("command = %q", command)
	}
}
