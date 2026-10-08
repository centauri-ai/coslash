package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestSSHLifecycleRemotePreservesPlatformProbeExitError(t *testing.T) {
	remote, err := NewSSHLifecycleRemote("host", fakeOptions(nil, 23, "probe failed", false))
	if err != nil {
		t.Fatal(err)
	}
	_, err = remote.ProbePlatform(context.Background())
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("platform probe error = %v, want exit code 23", err)
	}
	if got := sshErrorStderr(err); got != "probe failed" {
		t.Fatalf("platform probe stderr = %q, want %q", got, "probe failed")
	}
}

func TestLifecycleSFTPPrimitivesCreateVerifyAndRejectSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the in-process SFTP fixture cannot model a POSIX remote root on Windows")
	}
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
	// A helper install must not depend on, or mutate, the permissions of the
	// user's unrelated XDG-style directory tree.
	local := filepath.Join(root, ".local")
	if err := os.Mkdir(local, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(local, 0o775); err != nil {
		t.Fatal(err)
	}
	uid := uint32(os.Getuid())
	if err := validateLifecycleDirectories(client, home, "v1", uid, true); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(local)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o775 {
		t.Fatalf("unrelated .local permissions = %v", info.Mode().Perm())
	}
	absolute := path.Join(home, ".coslash/helpers/v1/coslash-helper")
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
	symlink := filepath.Join(root, ".coslash/helpers/symlink")
	if err := os.Symlink(filepath.Join(root, ".coslash/helpers/v1"), symlink); err != nil {
		t.Fatal(err)
	}
	if err := validateLifecycleDirectories(client, home, "symlink", uid, false); !errors.Is(err, ErrHelperVerification) {
		t.Fatalf("symlink validation error = %v", err)
	}
}

func TestResolveLifecyclePathAcceptsOnlyExactKnownLayout(t *testing.T) {
	want, _ := helperPath("v1")
	absolute, version, err := resolveLifecyclePath("/home/user", want)
	if err != nil || absolute != "/home/user/.coslash/helpers/v1/coslash-helper" || version != "v1" {
		t.Fatalf("path = %q, version = %q, error = %v", absolute, version, err)
	}
	for _, candidate := range []string{
		"~/.coslash/helpers/v1/other",
		"~/.coslash/helpers/v1/coslash-helper.new",
		"~/.coslash/helpers/../coslash-helper",
	} {
		if _, _, err := resolveLifecyclePath("/home/user", candidate); !errors.Is(err, ErrUnknownHelperPath) {
			t.Fatalf("accepted lifecycle path %q: %v", candidate, err)
		}
	}
}

// pipelineGate holds the first SFTP data request until a second one arrives,
// so a client that waits for each reply before sending the next fails.
type pipelineGate struct {
	once     sync.Once
	second   chan struct{}
	calls    atomic.Int32
	pipeline atomic.Bool
}

func newPipelineGate() *pipelineGate { return &pipelineGate{second: make(chan struct{})} }

func (gate *pipelineGate) enter() {
	if gate.calls.Add(1) != 1 {
		gate.once.Do(func() { close(gate.second) })
		return
	}
	select {
	case <-gate.second:
		gate.pipeline.Store(true)
	case <-time.After(5 * time.Second):
	}
}

type gatedFile struct {
	io.ReaderAt
	io.WriterAt
	gate *pipelineGate
}

func (file gatedFile) ReadAt(data []byte, offset int64) (int, error) {
	file.gate.enter()
	return file.ReaderAt.ReadAt(data, offset)
}

func (file gatedFile) WriteAt(data []byte, offset int64) (int, error) {
	file.gate.enter()
	return file.WriterAt.WriteAt(data, offset)
}

type gatedHandlers struct {
	sftp.Handlers
	reads, writes *pipelineGate
}

func (handlers gatedHandlers) Fileread(request *sftp.Request) (io.ReaderAt, error) {
	reader, err := handlers.FileGet.Fileread(request)
	return gatedFile{ReaderAt: reader, gate: handlers.reads}, err
}

func (handlers gatedHandlers) Filewrite(request *sftp.Request) (io.WriterAt, error) {
	writer, err := handlers.FilePut.Filewrite(request)
	return gatedFile{WriterAt: writer, gate: handlers.writes}, err
}

func TestHelperTransferPipelinesSFTPRequests(t *testing.T) {
	memory := sftp.InMemHandler()
	gated := gatedHandlers{Handlers: memory, reads: newPipelineGate(), writes: newPipelineGate()}
	clientConn, serverConn := net.Pipe()
	server := sftp.NewRequestServer(serverConn, sftp.Handlers{
		FileGet: gated, FilePut: gated, FileCmd: memory.FileCmd, FileList: memory.FileList,
	})
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve() }()
	client, err := newSFTPClient(clientConn, clientConn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		<-serveDone
	})
	content := bytes.Repeat([]byte("helper"), 200_000)
	file, err := client.OpenFile("/coslash-helper", os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(file, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	remoteFile, err := inspectLifecycleFile(client, "/coslash-helper", "reported")
	if err != nil || remoteFile.SHA256 != digest(content) || remoteFile.Size != int64(len(content)) {
		t.Fatalf("remote file = %#v, error = %v", remoteFile, err)
	}
	if !gated.writes.pipeline.Load() || !gated.reads.pipeline.Load() {
		t.Fatalf("helper transfer waited for each reply: writes pipelined=%v reads pipelined=%v",
			gated.writes.pipeline.Load(), gated.reads.pipeline.Load())
	}
}

func TestCappedWriterRejectsGrowthPastReportedSize(t *testing.T) {
	capped := &cappedWriter{writer: sha256.New(), remaining: 4}
	if _, err := capped.Write([]byte("four")); err != nil {
		t.Fatal(err)
	}
	if _, err := capped.Write([]byte("x")); !errors.Is(err, ErrHelperVerification) {
		t.Fatalf("growth error = %v, want ErrHelperVerification", err)
	}
}
