package agentexec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestCommandContextDoesNotWrapWindowsShims(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	path := filepath.Join(directory, "agent.cmd")
	if err := os.WriteFile(path, []byte("@echo off\r\nif not \"%~1\"==\"Bob's & project\" exit /b 2\r\nexit /b 7\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := CommandContext(context.Background(), "agent", "Bob's & project")
	if cmd.Path != path || !reflect.DeepEqual(cmd.Args, []string{"agent", "Bob's & project"}) {
		t.Fatalf("batch command = %q %q, want direct shim invocation", cmd.Path, cmd.Args)
	}
	_, err := Output(cmd)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("batch exit = %v, want status 7", err)
	}

	script := filepath.Join(directory, "agent.ps1")
	if err := os.WriteFile(script, []byte("param([string]$Value)\r\n[Console]::Write($Value + ':' + [Console]::In.ReadToEnd())\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd = CommandContext(context.Background(), script, "Bob's & project")
	if cmd.Path != script || !reflect.DeepEqual(cmd.Args, []string{script, "Bob's & project"}) {
		t.Fatalf("PowerShell command = %q %q, want direct shim invocation", cmd.Path, cmd.Args)
	}
	if _, err := Output(cmd); err == nil {
		t.Fatal("PowerShell script unexpectedly ran without a shell")
	}
}

func TestCanceledAgentProcessTerminatesDescendant(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := CommandContext(ctx, os.Args[0], "-test.run=^TestAgentExecProcessHelper$")
	cmd.Env = append(os.Environ(), "COSLASH_AGENTEXEC_PARENT=1")
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- Run(cmd) }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		cancel()
		<-done
		t.Fatal(err)
	}
	pid, err := strconv.ParseUint(strings.TrimSpace(line), 10, 32)
	if err != nil {
		cancel()
		<-done
		t.Fatal(err)
	}
	child, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		cancel()
		<-done
		t.Fatal(err)
	}
	defer windows.CloseHandle(child)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled agent process succeeded")
	}
	status, err := windows.WaitForSingleObject(child, 5_000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("descendant wait status = %#x, %v", status, err)
	}
}

func TestAgentExecProcessHelper(t *testing.T) {
	if os.Getenv("COSLASH_AGENTEXEC_CHILD") == "1" {
		for {
			time.Sleep(time.Hour)
		}
	}
	if os.Getenv("COSLASH_AGENTEXEC_PARENT") != "1" {
		return
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAgentExecProcessHelper$")
	child.Env = append(os.Environ(), "COSLASH_AGENTEXEC_CHILD=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintln(os.Stdout, child.Process.Pid)
	_ = child.Wait()
}
