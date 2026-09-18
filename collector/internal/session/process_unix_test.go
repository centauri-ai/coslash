//go:build !windows

package session

import (
	"os"
	"os/exec"
	"testing"
)

func TestIsProcessAlive(t *testing.T) {
	if IsProcessAlive(0) {
		t.Fatal("zero PID reported alive")
	}
	if !IsProcessAlive(os.Getpid()) {
		t.Fatal("current process reported dead")
	}

	command := exec.Command("sh", "-c", "exit 0")
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	if IsProcessAlive(command.Process.Pid) {
		t.Fatal("exited process reported alive")
	}
}
