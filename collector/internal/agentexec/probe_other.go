//go:build !unix && !windows

package agentexec

import "os/exec"

func configureProbeProcess(*exec.Cmd) func() { return func() {} }
