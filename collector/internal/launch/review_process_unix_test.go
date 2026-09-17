//go:build unix

package launch

import (
	"os/exec"
	"testing"
)

func TestConfigureReviewProcessCreatesProcessGroup(t *testing.T) {
	command := exec.Command("true")
	configureReviewProcess(command)
	if command.SysProcAttr == nil || !command.SysProcAttr.Setpgid {
		t.Fatal("reviewer was not placed in its own process group")
	}
}
