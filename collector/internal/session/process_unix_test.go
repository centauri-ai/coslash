//go:build !windows

package session

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func TestProcessSignalAlive(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "success", want: true},
		{name: "permission denied", err: syscall.EPERM, want: true},
		{name: "dead", err: syscall.ESRCH, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := processSignalAlive(test.err); got != test.want {
				t.Fatalf("processSignalAlive(%v) = %t, want %t", test.err, got, test.want)
			}
		})
	}
}

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
