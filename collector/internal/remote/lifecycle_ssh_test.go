package remote

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"
)

func TestSSHLifecycleRemoteUsesBoundedFixedPlatformProbe(t *testing.T) {
	remote, err := NewSSHLifecycleRemote("host", fakeOptions([]byte("Linux\nx86_64\n501\n"), 0, "", false))
	if err != nil {
		t.Fatal(err)
	}
	platform, err := remote.ProbePlatform(context.Background())
	if err != nil || platform.OS != "linux" || platform.Arch != "amd64" || platform.UID != 501 {
		t.Fatalf("platform = %#v, error = %v", platform, err)
	}
	remote.Options = fakeOptions(bytes.Repeat([]byte("x"), 129), 0, "", false)
	if _, err := remote.ProbePlatform(context.Background()); !errors.Is(err, ErrUnsupportedHelperPlatform) {
		t.Fatalf("oversized probe error = %v", err)
	}
}

func TestLifecycleSFTPPrimitivesCreateVerifyAndRejectSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	clientConn, serverConn := net.Pipe()
	server, err := sftp.NewServer(serverConn, sftp.WithServerWorkingDirectory(root))
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve() }()
	client, err := sftp.NewClientPipe(clientConn, clientConn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		<-serveDone
	})
	home, err := client.RealPath(".")
	if err != nil {
		t.Fatal(err)
	}
	uid := uint32(os.Getuid())
	if err := validateLifecycleDirectories(client, home, "v1", uid, true); err != nil {
		t.Fatal(err)
	}
	absolute := path.Join(home, ".local/lib/coslash/helpers/v1/coslash-helper")
	file, err := client.OpenFile(absolute, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		t.Fatal(err)
	}
	content := syntheticELF("amd64")
	if _, err := file.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := file.Chmod(0o700); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	remoteFile, err := inspectLifecycleFile(client, absolute, "reported")
	if err != nil || remoteFile.SHA256 != digest(content) || remoteFile.Mode.Perm() != 0o700 || remoteFile.UID != uid {
		t.Fatalf("remote file = %#v, error = %v", remoteFile, err)
	}
	symlink := filepath.Join(root, ".local/lib/coslash/helpers/symlink")
	if err := os.Symlink(filepath.Join(root, ".local/lib/coslash/helpers/v1"), symlink); err != nil {
		t.Fatal(err)
	}
	if err := validateLifecycleDirectories(client, home, "symlink", uid, false); !errors.Is(err, ErrHelperVerification) {
		t.Fatalf("symlink validation error = %v", err)
	}
}

func TestResolveLifecyclePathAcceptsOnlyExactKnownLayout(t *testing.T) {
	want, _ := helperPath("v1")
	absolute, version, err := resolveLifecyclePath("/home/user", want)
	if err != nil || absolute != "/home/user/.local/lib/coslash/helpers/v1/coslash-helper" || version != "v1" {
		t.Fatalf("path = %q, version = %q, error = %v", absolute, version, err)
	}
	for _, candidate := range []string{
		"~/.local/lib/coslash/helpers/v1/other",
		"~/.local/lib/coslash/helpers/v1/coslash-helper.new",
		"~/.local/lib/coslash/helpers/../coslash-helper",
	} {
		if _, _, err := resolveLifecyclePath("/home/user", candidate); !errors.Is(err, ErrUnknownHelperPath) {
			t.Fatalf("accepted lifecycle path %q: %v", candidate, err)
		}
	}
}
