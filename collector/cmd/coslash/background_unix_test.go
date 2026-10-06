//go:build darwin || linux

package main

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func TestBackgroundIgnoresHangup(t *testing.T) {
	if os.Getenv("COSLASH_TEST_HANGUP_CHILD") == "1" {
		ignoreBackgroundHangup()
		if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestBackgroundIgnoresHangup$")
	command.Env = append(os.Environ(), "COSLASH_TEST_HANGUP_CHILD=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("background child did not ignore SIGHUP: %v (%s)", err, output)
	}
}
