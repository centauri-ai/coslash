//go:build windows

package pi

import (
	"os"
	"os/exec"
	"strconv"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsProcessIdentityUsesExactFiletime(t *testing.T) {
	identity, err := ProcessStartIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &created, &exited, &kernel, &user); err != nil {
		t.Fatal(err)
	}
	// DateTime.ToFileTimeUtc returns these same 100 ns intervals since 1601.
	ticks := uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
	want := "windows:" + strconv.FormatUint(ticks, 10)
	if identity != want {
		t.Fatalf("identity %q want %q", identity, want)
	}
	if processAbsent(os.Getpid()) {
		t.Fatal("current process reported absent")
	}
	for _, pid := range []int{0, -1} {
		if identity, err := ProcessStartIdentity(pid); err == nil || identity != "" {
			t.Fatalf("invalid PID %d identity %q error %v", pid, identity, err)
		}
		if !processAbsent(pid) {
			t.Fatalf("invalid PID %d not absent", pid)
		}
	}
}

func TestWindowsExitedOwner(t *testing.T) {
	if os.Getenv("COSLASH_PI_TEST_EXITED_OWNER") == "1" {
		os.Exit(0)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestWindowsExitedOwner$")
	command.Env = append(os.Environ(), "COSLASH_PI_TEST_EXITED_OWNER=1")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	pid := command.Process.Pid
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	record := testRecord()
	record.PID = pid
	if got := ownerState(runtimeEvidence{Record: record}); got != "dead" {
		t.Fatalf("exited owner state %q", got)
	}
}
