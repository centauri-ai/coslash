//go:build !windows

package remote

import (
	"reflect"
	"testing"
)

func TestUnixSSHOptionsIncludeMultiplexing(t *testing.T) {
	got := sshOptions(7)
	want := []string{
		"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=7",
		"-o", "ControlMaster=auto", "-o", "ControlPath=" + controlSocketPath(),
		"-o", "ControlPersist=" + defaultControlPersist,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SSH options = %q, want %q", got, want)
	}
}
