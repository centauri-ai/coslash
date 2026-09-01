//go:build !unix

package remote

import "os/exec"

// coSlash ships for macOS. Elsewhere there is no process group to place the SSH
// client in, so cancellation falls back to killing the client itself.
func configureProcessGroup(*exec.Cmd) {}

func terminateProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
