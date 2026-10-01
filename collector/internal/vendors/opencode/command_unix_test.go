//go:build !windows

package opencode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

type controlledProbeContext struct {
	context.Context
	done  chan struct{}
	cause error
}

func (c controlledProbeContext) Done() <-chan struct{} { return c.done }
func (c controlledProbeContext) Err() error {
	select {
	case <-c.done:
		return c.cause
	default:
		return nil
	}
}

func TestOwnedCommandHelper(t *testing.T) {
	mode := os.Getenv("COSLASH_OWNED_HELPER")
	if mode == "" {
		return
	}
	if mode == "child" {
		_, _ = io.Copy(io.Discard, os.NewFile(3, "hold"))
		os.Exit(0)
	}
	hold, keep, err := os.Pipe()
	if err != nil {
		os.Exit(2)
	}
	defer keep.Close()
	child := exec.Command(os.Args[0], "-test.run=^TestOwnedCommandHelper$")
	child.Env = append(os.Environ(), "COSLASH_OWNED_HELPER=child")
	child.ExtraFiles = []*os.File{hold, keep}
	if child.Start() != nil {
		os.Exit(2)
	}
	fmt.Fprintln(os.NewFile(3, "ready"), child.Process.Pid)
	_, _ = io.Copy(io.Discard, os.Stdin)
	if mode == "overflow" {
		fmt.Print(strings.Repeat("x", 128))
	}
	os.Exit(0)
}

func TestBoundedCommandOwnsLauncherChildren(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "overflow", "success"} {
		t.Run(mode, func(t *testing.T) {
			ctx := controlledProbeContext{Context: context.Background(), done: make(chan struct{}), cause: context.Canceled}
			if mode == "deadline" {
				ctx.cause = context.DeadlineExceeded
			}
			ready, notify, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer ready.Close()
			defer notify.Close()
			input, release, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			defer release.Close()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnedCommandHelper$")
			cmd.Env = append(os.Environ(), "COSLASH_OWNED_HELPER="+mode)
			cmd.Stdin, cmd.ExtraFiles = input, []*os.File{notify}
			finished := make(chan error, 1)
			go func() { _, err := boundedCommandOutput(cmd, 16); finished <- err }()
			var pid int
			if _, err := fmt.Fscanln(ready, &pid); err != nil || pid <= 0 {
				t.Fatalf("child readiness: %v", err)
			}
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
			if mode == "cancel" || mode == "deadline" {
				close(ctx.done)
			} else {
				_ = release.Close()
			}
			err = <-finished
			if (mode == "success") != (err == nil) {
				t.Fatalf("%s completion: %v", mode, err)
			}
			// Kernel exit/reaping may complete after the launcher is waited. No
			// elapsed-time threshold determines the assertion.
			for range 10000 {
				if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
					return
				}
				runtime.Gosched()
			}
			t.Fatal("owned child still exists: " + strconv.Itoa(pid))
		})
	}
}
