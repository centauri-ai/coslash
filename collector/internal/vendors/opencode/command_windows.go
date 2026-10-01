package opencode

import (
	"os/exec"

	"github.com/centauri-ai/coslash/collector/internal/agentexec"
)

func runOwnedCommand(cmd *exec.Cmd) error { return agentexec.Run(cmd) }
