//go:build !windows

package launch

import (
	"strings"
	"testing"
)

func TestRemoteSSHCommandReusesControlSocket(t *testing.T) {
	t.Setenv("COSLASH_HOME", "/tmp/coslash-test")
	command := remoteSSHCommand("agent-box", "true")
	if !strings.Contains(command, "'ControlMaster=auto'") ||
		!strings.Contains(command, "'ControlPath=/tmp/coslash-test/ssh/cm-%C'") {
		t.Fatalf("command = %q", command)
	}
}
