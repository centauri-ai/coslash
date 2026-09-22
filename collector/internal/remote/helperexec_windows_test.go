package remote

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsProcessJobTerminatesDescendants(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperExecProcess", "--")
	cmd.Env = append(os.Environ(),
		"COSLASH_FAKE_HELPER=1",
		"COSLASH_FAKE_SPAWN_CHILD=1",
		"COSLASH_FAKE_CHILD_PID="+pidPath,
		"COSLASH_FAKE_OUTPUT=ready\n",
		"COSLASH_FAKE_HANG=true",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := startProcessGroup(cmd); err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		terminateProcessGroup(cmd)
		_ = waitProcessGroup(cmd)
		t.Fatalf("helper readiness = %q, %v", line, err)
	}
	rawPID, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.ParseUint(strings.TrimSpace(string(rawPID)), 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	child, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(child)

	terminateProcessGroup(cmd)
	_ = waitProcessGroup(cmd)
	status, err := windows.WaitForSingleObject(child, 5_000)
	if err != nil {
		t.Fatal(err)
	}
	if status != windows.WAIT_OBJECT_0 {
		t.Fatalf("child process wait status = %#x", status)
	}
}
