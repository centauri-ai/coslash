package remote

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"sync"

	"github.com/pkg/sftp"
)

var aliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func SSHArgs(alias string, connectTimeoutSeconds int) ([]string, error) {
	if !aliasPattern.MatchString(alias) {
		return nil, ErrInvalidAlias
	}
	if connectTimeoutSeconds <= 0 {
		connectTimeoutSeconds = int(DefaultConnectTimeout.Seconds())
	}
	return []string{
		"-T",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=" + strconv.Itoa(connectTimeoutSeconds),
		alias,
		"-s", "sftp",
	}, nil
}

// Session owns one system-SSH process and its read-only SFTP source.
type Session struct {
	client *sftp.Client
	source *Source
	cancel context.CancelFunc
	cmd    *exec.Cmd
	stderr *cappedStderr

	closeOnce sync.Once
	closeErr  error
}

type OpenOptions struct {
	SSHBin  string
	Limits  Limits
	command func(context.Context, string, ...string) *exec.Cmd
}

func OpenSession(ctx context.Context, alias string, options OpenOptions) (*Session, error) {
	limits := options.Limits.withDefaults()
	args, err := SSHArgs(alias, int(limits.ConnectTimeout.Seconds()))
	if err != nil {
		return nil, err
	}
	bin := options.SSHBin
	if bin == "" {
		bin = "ssh"
	}
	sessionCtx, cancel := context.WithTimeout(ctx, limits.Deadline)
	command := options.command
	if command == nil {
		command = exec.CommandContext
	}
	cmd := command(sessionCtx, bin, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open SSH stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open SSH stdout: %w", err)
	}
	stderr := &cappedStderr{limit: limits.MaxStderrBytes, cancel: cancel}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start SSH: %w", err)
	}
	client, err := sftp.NewClientPipe(stdout, stdin)
	if err != nil {
		_ = stdin.Close()
		cancel()
		_ = cmd.Wait()
		if stderr.overflow {
			return nil, ErrStderrLimit
		}
		return nil, fmt.Errorf("open SFTP subsystem: %w", err)
	}
	operations := sftpOperations{
		realPath: client.RealPath,
		lstat:    client.Lstat,
		readDir:  client.ReadDir,
		open: func(path string) (io.ReadCloser, error) {
			return client.Open(path)
		},
	}
	source, err := newSource(operations, limits)
	if err != nil {
		_ = client.Close()
		cancel()
		_ = cmd.Wait()
		return nil, err
	}
	return &Session{
		client: client, source: source, cancel: cancel, cmd: cmd, stderr: stderr,
	}, nil
}

func (session *Session) Source() *Source {
	return session.source
}

func (session *Session) Stderr() string {
	return session.stderr.String()
}

func (session *Session) Close() error {
	session.closeOnce.Do(func() {
		clientErr := session.client.Close()
		session.cancel()
		_ = session.cmd.Wait()
		if clientErr != nil {
			session.closeErr = clientErr
		}
		if session.stderr.overflow {
			session.closeErr = ErrStderrLimit
		}
	})
	return session.closeErr
}

type cappedStderr struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (stderr *cappedStderr) Write(data []byte) (int, error) {
	stderr.mu.Lock()
	defer stderr.mu.Unlock()
	if stderr.overflow {
		return len(data), nil
	}
	remaining := stderr.limit - stderr.buffer.Len()
	if remaining <= 0 {
		stderr.overflow = true
		stderr.cancel()
		return len(data), nil
	}
	if len(data) > remaining {
		_, _ = stderr.buffer.Write(data[:remaining])
		stderr.overflow = true
		stderr.cancel()
		return len(data), nil
	}
	_, _ = stderr.buffer.Write(data)
	return len(data), nil
}

func (stderr *cappedStderr) String() string {
	stderr.mu.Lock()
	defer stderr.mu.Unlock()
	return stderr.buffer.String()
}
