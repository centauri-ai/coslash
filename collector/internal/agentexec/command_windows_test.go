package agentexec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestCommandContextRunsWindowsShims(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(directory, "agent.cmd"), []byte("@echo off\r\nif not \"%~1\"==\"Bob's & project\" exit /b 2\r\nexit /b 7\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := CommandContext(context.Background(), "agent", "Bob's & project")
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
	cmd.Stdin = strings.NewReader("private prompt")
	output, err := Output(cmd)
	if err != nil || string(output) != "Bob's & project:private prompt" {
		t.Fatalf("PowerShell output = %q, %v", output, err)
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

func TestCancelBeforeJobAssignmentKillsSuspendedAgent(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	cmd := CommandContext(context.Background(), os.Args[0], "-test.run=^TestAgentExecProcessHelper$")
	cmd.Env = append(os.Environ(), "COSLASH_AGENTEXEC_MARKER="+marker)
	originalAssign := assignAgentProcess
	t.Cleanup(func() { assignAgentProcess = originalAssign })
	called := false
	assignAgentProcess = func(job, process windows.Handle) error {
		called = true
		if err := cmd.Cancel(); err != nil {
			t.Errorf("cancel suspended agent: %v", err)
		}
		return windows.AssignProcessToJobObject(job, process)
	}
	if err := Run(cmd); err == nil {
		t.Fatal("canceled agent process succeeded")
	}
	if !called {
		t.Fatal("job assignment was not reached")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled agent reached executable; marker error = %v", err)
	}
}

func TestOutputBoundsStderrAndPreservesBothEnds(t *testing.T) {
	cmd := CommandContext(context.Background(), os.Args[0], "-test.run=^TestAgentExecProcessHelper$")
	cmd.Env = append(os.Environ(), "COSLASH_AGENTEXEC_STDERR=1")
	_, err := Output(cmd)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("agent exit = %v, want status 7", err)
	}
	if len(exit.Stderr) > 2*stderrPartLimit+64 || !strings.HasPrefix(string(exit.Stderr), "prefix") ||
		!strings.HasSuffix(string(exit.Stderr), "suffix") || !strings.Contains(string(exit.Stderr), "omitting") {
		t.Fatalf("stderr length = %d, first and last diagnostics were not retained", len(exit.Stderr))
	}
	var small boundedStderr
	_, _ = small.Write([]byte("short "))
	_, _ = small.Write([]byte("diagnostic"))
	if got := string(small.Bytes()); got != "short diagnostic" {
		t.Fatalf("short stderr = %q", got)
	}
}

func TestAgentExecProcessHelper(t *testing.T) {
	if marker := os.Getenv("COSLASH_AGENTEXEC_MARKER"); marker != "" {
		if err := os.WriteFile(marker, []byte("started"), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	if os.Getenv("COSLASH_AGENTEXEC_STDERR") == "1" {
		_, _ = fmt.Fprint(os.Stderr, "prefix", strings.Repeat("x", 100<<10), "suffix")
		os.Exit(7)
	}
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
