//go:build windows

package session

import (
	"os"
	"os/exec"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("COSLASH_EXIT_STILL_ACTIVE") == "1" {
		os.Exit(259)
	}
	os.Exit(m.Run())
}

func TestIsProcessAlive(t *testing.T) {
	if IsProcessAlive(0) {
		t.Fatal("zero PID reported alive")
	}
	if !IsProcessAlive(os.Getpid()) {
		t.Fatal("current process reported dead")
	}

	command := exec.Command(os.Args[0], "-test.run=^$")
	command.Env = append(os.Environ(), "COSLASH_EXIT_STILL_ACTIVE=1")
	if err := command.Run(); err == nil {
		t.Fatal("helper process unexpectedly succeeded")
	}
	if IsProcessAlive(command.Process.Pid) {
		t.Fatal("process that exited with STILL_ACTIVE reported alive")
	}
}
