package remote

import (
	"reflect"
	"testing"
)

func TestSSHOptionsOmitMultiplexingWhenDisabled(t *testing.T) {
	got := sshOptions(7, false)
	want := []string{"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=7"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SSH options = %q, want %q", got, want)
	}
}

func TestSSHOptionsIncludeMultiplexingWhenEnabled(t *testing.T) {
	got := sshOptions(7, true)
	want := []string{
		"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=7",
		"-o", "ControlMaster=auto", "-o", "ControlPath=" + controlSocketPath(),
		"-o", "ControlPersist=" + defaultControlPersist,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SSH options = %q, want %q", got, want)
	}
}
