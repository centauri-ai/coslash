//go:build unix

package remote

import (
	"os/exec"
	"syscall"
)

// startProcessGroup puts the SSH client in its own process group so a
// timeout, a cancellation, or an output flood can terminate the client and
// anything it spawned (a ProxyCommand, for example) rather than only the client.
func startProcessGroup(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	return cmd.Start()
}

func startInteractiveProcess(cmd *exec.Cmd) error { return cmd.Start() }

func waitProcessGroup(cmd *exec.Cmd) error { return cmd.Wait() }

func terminateProcessGroup(cmd *exec.Cmd) bool {
	if cmd.Process == nil {
		return false
	}
	// The group ID equals the child PID because the child leads its own group.
	groupErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	processErr := cmd.Process.Kill()
	return groupErr == nil || processErr == nil
}

func terminateInteractiveProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func processWasTerminated(exitCode int) bool { return exitCode < 0 }
