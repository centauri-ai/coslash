//go:build windows

package remote

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
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
	if _, err := os.Stat(filepath.Join(home, "ssh")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Unix control directory exists: %v", err)
	}
	args, err := ControlExitArgs("linux-host")
	if err != nil {
		t.Fatal(err)
	}
	if args != nil {
		t.Fatalf("control cleanup args = %q, want nil", args)
	}
}
