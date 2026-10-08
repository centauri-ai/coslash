package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
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

// pipelineGate holds the first data operation until another matching SFTP
// request has been observed in the client's outgoing stream.
type pipelineGate struct {
	once     sync.Once
	second   chan struct{}
	requests atomic.Int32
	calls    atomic.Int32
	waiting  atomic.Bool
	pipeline atomic.Bool
}

func newPipelineGate() *pipelineGate { return &pipelineGate{second: make(chan struct{})} }

func (gate *pipelineGate) requestSent() {
	if gate.requests.Add(1) > 1 {
		if gate.waiting.Load() {
			gate.pipeline.Store(true)
		}
		gate.once.Do(func() { close(gate.second) })
	}
}

func (gate *pipelineGate) enter() bool {
	if gate.calls.Add(1) != 1 {
		return false
	}
	gate.waiting.Store(true)
	if gate.requests.Load() > 1 {
		gate.pipeline.Store(true)
	}
	// The timeout only prevents a broken client from hanging the fixture.
	select {
	case <-gate.second:
	case <-time.After(30 * time.Second):
	}
	return true
}

func (gate *pipelineGate) leave() {
	gate.waiting.Store(false)
}

type observedSFTPWriter struct {
	io.WriteCloser
	mu            sync.Mutex
	pending       []byte
	reads, writes *pipelineGate
}

const (
	sftpReadRequestPacket  = 5
	sftpWriteRequestPacket = 6
)

func (writer *observedSFTPWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	n, err := writer.WriteCloser.Write(data)
	writer.pending = append(writer.pending, data[:n]...)
	for len(writer.pending) >= 4 {
		packetLength := int(binary.BigEndian.Uint32(writer.pending[:4]))
		packetSize := 4 + packetLength
		if packetLength == 0 || len(writer.pending) < packetSize {
			break
		}
		switch writer.pending[4] {
		case sftpReadRequestPacket:
			writer.reads.requestSent()
		case sftpWriteRequestPacket:
			writer.writes.requestSent()
		}
		writer.pending = writer.pending[packetSize:]
	}
	return n, err
}

type gatedFile struct {
	io.ReaderAt
	io.WriterAt
	gate *pipelineGate
}

func (file gatedFile) ReadAt(data []byte, offset int64) (int, error) {
	first := file.gate.enter()
	if first {
		defer file.gate.leave()
	}
	return file.ReaderAt.ReadAt(data, offset)
}

func (file gatedFile) WriteAt(data []byte, offset int64) (int, error) {
	first := file.gate.enter()
	if first {
		defer file.gate.leave()
	}
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
	writer := &observedSFTPWriter{WriteCloser: clientConn, reads: gated.reads, writes: gated.writes}
	client, err := newSFTPClient(clientConn, writer)
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
