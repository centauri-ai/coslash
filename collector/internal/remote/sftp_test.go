package remote

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

func TestSSHArgsRequestsOnlySFTPSubsystem(t *testing.T) {
	args, err := SSHArgs("build-host", 8)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8",
		"build-host", "-s", "sftp",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	for _, alias := range []string{"-oProxyCommand=bad", "host name", "host;bad", ""} {
		if _, err := SSHArgs(alias, 8); !errors.Is(err, ErrInvalidAlias) {
			t.Fatalf("alias %q: expected invalid alias, got %v", alias, err)
		}
	}
}

func TestSSHArgsAllowOpenSSHProxyJumpConfiguration(t *testing.T) {
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("system OpenSSH is unavailable")
	}
	config := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(config, []byte(strings.TrimSpace(`
Host jump
  HostName jump.example
Host gpu-server
  HostName internal.example
  User build
  ProxyJump jump
`)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args, err := SSHArgs("gpu-server", 8)
	if err != nil {
		t.Fatal(err)
	}
	commandArgs := append([]string{"-G", "-F", config}, args...)
	output, err := exec.Command(ssh, commandArgs...).Output()
	if err != nil {
		t.Fatal(err)
	}
	resolved := string(output)
	for _, want := range []string{"hostname internal.example", "user build", "proxyjump jump"} {
		if !strings.Contains(resolved, want) {
			t.Fatalf("resolved config does not contain %q:\n%s", want, resolved)
		}
	}
}

func TestOpenSessionReadsAndReapsHelperProcess(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, ".claude", "projects", "repo", "session.jsonl")
	writeTestFile(t, transcript, "{}\n")
	var captured []string
	session, err := OpenSession(context.Background(), "build-host", OpenOptions{
		Limits: Limits{Deadline: 5 * time.Second},
		command: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			captured = append([]string(nil), args...)
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestSSHHelperProcess")
			cmd.Env = append(os.Environ(),
				"COSLASH_SSH_HELPER=serve",
				"COSLASH_SSH_HELPER_ROOT="+root,
			)
			return cmd
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(captured, []string{
		"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8",
		"build-host", "-s", "sftp",
	}) {
		t.Fatalf("unexpected SSH args: %#v", captured)
	}
	file, err := session.Source().Open(transcript)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if string(data) != "{}\n" {
		t.Fatalf("unexpected transcript: %q", data)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if session.cmd.ProcessState == nil || !session.cmd.ProcessState.Exited() {
		t.Fatal("SSH helper was not reaped")
	}
}

func TestOpenSessionCancellationStopsHandshake(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := OpenSession(ctx, "build-host", OpenOptions{
		Limits: Limits{Deadline: time.Second},
		command: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestSSHHelperProcess")
			cmd.Env = append(os.Environ(), "COSLASH_SSH_HELPER=hang")
			return cmd
		},
	})
	if err == nil {
		t.Fatal("expected canceled handshake")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("cancellation took too long: %s", time.Since(started))
	}
}

func TestOpenSessionBoundsStderr(t *testing.T) {
	_, err := OpenSession(context.Background(), "build-host", OpenOptions{
		Limits: Limits{Deadline: time.Second, MaxStderrBytes: 16},
		command: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestSSHHelperProcess")
			cmd.Env = append(os.Environ(), "COSLASH_SSH_HELPER=stderr")
			return cmd
		},
	})
	if !errors.Is(err, ErrStderrLimit) {
		t.Fatalf("expected stderr limit, got %v", err)
	}
}

func TestSSHHelperProcess(t *testing.T) {
	mode := os.Getenv("COSLASH_SSH_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "hang":
		select {}
	case "stderr":
		_, _ = os.Stderr.WriteString(strings.Repeat("x", 128))
		select {}
	case "serve":
		root := os.Getenv("COSLASH_SSH_HELPER_ROOT")
		server, err := sftp.NewServer(
			stdioReadWriteCloser{},
			sftp.ReadOnly(),
			sftp.WithServerWorkingDirectory(root),
		)
		if err != nil {
			os.Exit(2)
		}
		err = server.Serve()
		_ = server.Close()
		if err != nil && !errors.Is(err, io.EOF) {
			os.Exit(3)
		}
		os.Exit(0)
	default:
		os.Exit(4)
	}
}

type stdioReadWriteCloser struct{}

func (stdioReadWriteCloser) Read(buffer []byte) (int, error)  { return os.Stdin.Read(buffer) }
func (stdioReadWriteCloser) Write(buffer []byte) (int, error) { return os.Stdout.Write(buffer) }
func (stdioReadWriteCloser) Close() error                     { return nil }
