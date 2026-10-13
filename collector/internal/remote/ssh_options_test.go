//go:build !windows

package remote

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func TestUnixSSHOptionsIncludeMultiplexing(t *testing.T) {
	got, err := sshOptions(7)
	if err != nil {
		t.Fatal(err)
	}
	controlPath, err := controlSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=7",
		"-o", "ControlMaster=auto", "-o", "ControlPath=" + controlPath,
		"-o", "ControlPersist=" + defaultControlPersist,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SSH options = %q, want %q", got, want)
	}
}

func TestControlSocketWithLongLocalHome(t *testing.T) {
	t.Setenv("COSLASH_HOME", filepath.Join(t.TempDir(), strings.Repeat("home", 50)))
	t.Setenv("TMPDIR", settings.Home())
	if err := ensureSSHControlDir(); err != nil {
		t.Fatal(err)
	}
	// OpenSSH appends a dot and 16 random characters before publishing the socket.
	controlPath, err := controlSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	path := strings.Replace(controlPath, "%C", strings.Repeat("a", 40), 1) + "." + strings.Repeat("b", 16)
	// Reserve room for a ten-digit UID and the terminating NUL.
	if len(path)+10-len(strconv.Itoa(os.Getuid()))+1 > 104 {
		t.Fatalf("OpenSSH listener path is %d bytes, must be below 104: %s", len(path), path)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}
