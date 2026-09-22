//go:build !unix && !windows

package remote

import "os/exec"

// coSlash ships for macOS. Elsewhere there is no process group to place the SSH
// client in, so cancellation falls back to killing the client itself.
func startProcessGroup(cmd *exec.Cmd) error { return cmd.Start() }

func startInteractiveProcess(cmd *exec.Cmd) error { return cmd.Start() }

func waitProcessGroup(cmd *exec.Cmd) error { return cmd.Wait() }

func terminateProcessGroup(cmd *exec.Cmd) bool {
	if cmd.Process != nil {
		return cmd.Process.Kill() == nil
	}
	return false
}

func terminateInteractiveProcess(cmd *exec.Cmd) { terminateProcessGroup(cmd) }

func processWasTerminated(exitCode int) bool { return exitCode < 0 }
