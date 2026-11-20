package agentexec

import (
	"os/exec"

	"github.com/centauri-ai/coslash/collector/internal/backgroundexec"
)

func configureProbeProcess(command *exec.Cmd) func() {
	backgroundexec.HideConsole(command)
	return func() {} // Run closes the Job Object, including remaining descendants.
}
