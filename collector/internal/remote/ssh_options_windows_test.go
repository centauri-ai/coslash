//go:build windows

package remote

import (
	"context"
	"os/exec"
	"reflect"
	"slices"
	"strconv"
	"testing"
)

func TestWindowsSSHArgsUseOpenSFTPWithoutMultiplexing(t *testing.T) {
	got, err := SSHArgs("linux-host", 7)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=7", "linux-host", "-s", "sftp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SSH args = %q, want %q", got, want)
	}
}

func TestWindowsRemoteCommandsStayPOSIXWithoutMultiplexing(t *testing.T) {
	platform, err := HelperPlatformArgs("linux-host", 7)
	if err != nil {
		t.Fatal(err)
	}
	wantPlatform := []string{
		"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=7",
		"linux-host", "uname -s; uname -m; id -u",
	}
	if !reflect.DeepEqual(platform, wantPlatform) {
		t.Fatalf("platform args = %q, want %q", platform, wantPlatform)
	}

	helper, err := HelperArgs("linux-host", "~/.coslash/helpers/v1/coslash-helper", HelperCommandCollect, 7)
	if err != nil {
		t.Fatal(err)
	}
	wantHelper := []string{
		"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=7",
		"linux-host", `"$HOME"/'.coslash/helpers/v1/coslash-helper' collect`,
	}
	if !reflect.DeepEqual(helper, wantHelper) {
		t.Fatalf("helper args = %q, want %q", helper, wantHelper)
	}
}

func TestWindowsControlMasterLifecycleIsDisabled(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	calls := 0
	options := OpenOptions{command: func(context.Context, string, ...string) *exec.Cmd {
		calls++
		return exec.Command("cmd", "/c", "exit", "0")
	}}
	if err := ensureControlMaster(context.Background(), "linux-host", options); err != nil {
		t.Fatal(err)
	}
	if err := ExitControlMaster("linux-host", options); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("control lifecycle executed %d commands, want none", calls)
	}
	args, err := ControlExitArgs("linux-host")
	if err != nil {
		t.Fatal(err)
	}
	if args != nil {
		t.Fatalf("control cleanup args = %q, want nil", args)
	}
}

func TestWindowsInteractiveAuthenticationUsesAgentWithoutMultiplexing(t *testing.T) {
	args, err := interactiveMasterArgs("jane@linux-host")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-T", "-o", "BatchMode=no", "-o", "AddKeysToAgent=yes", "-o", "ConnectTimeout=" + strconv.Itoa(int(DefaultConnectTimeout.Seconds())),
		"-l", "jane", "linux-host", "true",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("interactive args = %q, want %q", args, want)
	}
	if slices.Contains(args, "ControlMaster=yes") {
		t.Fatalf("interactive args contain unsupported multiplexing: %q", args)
	}
}

func TestWindowsAuthenticationCheckIsNonInteractive(t *testing.T) {
	args, err := controlCheckArgs("linux-host")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=3", "linux-host", "true"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("authentication check args = %q, want %q", args, want)
	}
}

func TestWindowsAuthenticationStatusDoesNotPollRemoteHost(t *testing.T) {
	ready, err := authAttemptConnectionReady(context.Background(), "linux-host")
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("authentication status reported ready before the terminal command completed")
	}
}
