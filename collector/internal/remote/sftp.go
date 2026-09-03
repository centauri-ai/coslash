package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/pkg/sftp"
)

var aliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

const defaultControlPersist = "10m"

func controlSocketPath() string {
	return filepath.Join(settings.Home(), "ssh", "cm-%C")
}

func ensureSSHControlDir() error {
	dir := filepath.Join(settings.Home(), "ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create SSH control directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect SSH control directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("SSH control directory is not a real directory")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure SSH control directory: %w", err)
	}
	return nil
}

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
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + controlSocketPath(),
		"-o", "ControlPersist=" + defaultControlPersist,
		alias,
		"-s", "sftp",
	}, nil
}

func ControlExitArgs(alias string) ([]string, error) {
	if !aliasPattern.MatchString(alias) {
		return nil, ErrInvalidAlias
	}
	return []string{
		"-O", "exit",
		"-o", "ControlPath=" + controlSocketPath(),
		alias,
	}, nil
}

func controlCheckArgs(alias string) ([]string, error) {
	if !aliasPattern.MatchString(alias) {
		return nil, ErrInvalidAlias
	}
	return []string{
		"-O", "check",
		"-o", "ControlPath=" + controlSocketPath(),
		alias,
	}, nil
}

func controlMasterStartArgs(alias string, connectTimeoutSeconds int) ([]string, error) {
	if !aliasPattern.MatchString(alias) {
		return nil, ErrInvalidAlias
	}
	if connectTimeoutSeconds <= 0 {
		connectTimeoutSeconds = int(DefaultConnectTimeout.Seconds())
	}
	// -f -N leaves a background master so later SFTP clients can die without the tunnel.
	return []string{
		"-f",
		"-N",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=" + strconv.Itoa(connectTimeoutSeconds),
		"-o", "ControlMaster=yes",
		"-o", "ControlPath=" + controlSocketPath(),
		"-o", "ControlPersist=" + defaultControlPersist,
		alias,
	}, nil
}

func runSSHCommand(ctx context.Context, options OpenOptions, args []string) error {
	bin := options.SSHBin
	if bin == "" {
		bin = "ssh"
	}
	command := options.command
	if command == nil {
		command = exec.CommandContext
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := command(runCtx, bin, args...)
	configureProcessGroup(cmd)
	output := &cappedStderr{limit: options.Limits.withDefaults().MaxStderrBytes, cancel: cancel}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start SSH command: %w", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	var err error
	select {
	case err = <-waited:
	case <-runCtx.Done():
		terminateProcessGroup(cmd)
		err = <-waited
	}
	if output.overflow {
		return ErrStderrLimit
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return wrapSSHError(err, output.String())
	}
	return nil
}

func ensureControlMaster(ctx context.Context, alias string, options OpenOptions) error {
	if err := ensureSSHControlDir(); err != nil {
		return err
	}
	checkArgs, err := controlCheckArgs(alias)
	if err != nil {
		return err
	}
	limits := options.Limits.withDefaults()
	checkCtx, cancelCheck := context.WithTimeout(ctx, limits.ConnectTimeout)
	err = runSSHCommand(checkCtx, options, checkArgs)
	cancelCheck()
	if err == nil {
		return nil
	} else if errors.Is(err, ErrStderrLimit) || ctx.Err() != nil {
		return err
	}
	startArgs, err := controlMasterStartArgs(alias, int(limits.ConnectTimeout.Seconds()))
	if err != nil {
		return err
	}
	startCtx, cancel := context.WithTimeout(ctx, limits.ConnectTimeout)
	defer cancel()
	if err := runSSHCommand(startCtx, options, startArgs); err != nil {
		return fmt.Errorf("start SSH control master: %w", err)
	}
	return nil
}

// ExitControlMaster asks OpenSSH to drop coSlash's multiplexed master for alias.
func ExitControlMaster(alias string, options OpenOptions) error {
	args, err := ControlExitArgs(alias)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runSSHCommand(ctx, options, args); err != nil {
		return fmt.Errorf("exit SSH control master: %w", err)
	}
	return nil
}

func exitControlMasterBestEffort(alias string) {
	if alias == "" {
		return
	}
	_ = ExitControlMaster(alias, OpenOptions{})
}

func benignSessionCloseErr(err error) bool {
	if err == nil {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "file already closed") ||
		strings.Contains(message, "use of closed")
}

// Session owns one system-SSH process and its read-only SFTP source.
type Session struct {
	client *sftp.Client
	source *Source
	ctx    context.Context
	cancel context.CancelFunc
	cmd    *exec.Cmd
	stderr *cappedStderr

	closeOnce sync.Once
	closeErr  error
}

type OpenOptions struct {
	SSHBin        string
	Limits        Limits
	command       func(context.Context, string, ...string) *exec.Cmd
	lifecycleOnly bool
}

type sshProcessError struct {
	err    error
	stderr string
}

func (err *sshProcessError) Error() string { return err.err.Error() }

func (err *sshProcessError) Unwrap() error { return err.err }

func wrapSSHError(err error, stderr string) error {
	if stderr == "" {
		return err
	}
	return &sshProcessError{err: err, stderr: stderr}
}

func sshErrorStderr(err error) string {
	var processErr *sshProcessError
	if errors.As(err, &processErr) {
		return processErr.stderr
	}
	return ""
}

func OpenSession(ctx context.Context, alias string, options OpenOptions) (*Session, error) {
	limits := options.Limits.withDefaults()
	args, err := SSHArgs(alias, int(limits.ConnectTimeout.Seconds()))
	if err != nil {
		return nil, err
	}
	// Injected commands are unit-test fakes; skip real OpenSSH master setup there.
	if options.command == nil {
		if err := ensureControlMaster(ctx, alias, options); err != nil {
			return nil, err
		}
	} else if err := ensureSSHControlDir(); err != nil {
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
	type clientResult struct {
		client *sftp.Client
		err    error
	}
	opened := make(chan clientResult, 1)
	go func() {
		client, openErr := sftp.NewClientPipe(stdout, stdin)
		opened <- clientResult{client: client, err: openErr}
	}()
	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, limits.ConnectTimeout)
	var client *sftp.Client
	select {
	case result := <-opened:
		cancelHandshake()
		client, err = result.client, result.err
	case <-handshakeCtx.Done():
		cancelHandshake()
		_ = stdin.Close()
		cancel()
		_ = cmd.Wait()
		return nil, handshakeCtx.Err()
	}
	if err != nil {
		_ = stdin.Close()
		cancel()
		_ = cmd.Wait()
		if stderr.overflow {
			return nil, ErrStderrLimit
		}
		if sessionCtx.Err() != nil {
			return nil, sessionCtx.Err()
		}
		return nil, wrapSSHError(fmt.Errorf("open SFTP subsystem: %w", err), stderr.String())
	}
	operations := sftpOperations{
		realPath: client.RealPath,
		lstat:    client.Lstat,
		readDir:  client.ReadDir,
		open: func(path string) (io.ReadCloser, error) {
			return client.Open(path)
		},
	}
	var source *Source
	if options.lifecycleOnly {
		home, homeErr := client.RealPath(".")
		if homeErr != nil {
			err = fmt.Errorf("resolve SFTP home: %w", homeErr)
		} else {
			source = &Source{home: path.Clean(home), limits: limits}
		}
	} else {
		source, err = newSource(operations, limits)
	}
	if err != nil {
		_ = client.Close()
		_ = cmd.Wait()
		cancel()
		return nil, wrapSSHError(err, stderr.String())
	}
	return &Session{
		client: client, source: source, ctx: sessionCtx, cancel: cancel, cmd: cmd, stderr: stderr,
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
		// Wait before cancel so a clean SFTP close does not SIGKILL the child first.
		clientErr := session.client.Close()
		waitErr := session.cmd.Wait()
		ctxErr := session.ctx.Err()
		session.cancel()
		if ctxErr != nil {
			session.closeErr = ctxErr
		} else if !benignSessionCloseErr(clientErr) {
			session.closeErr = clientErr
		} else if waitErr != nil {
			session.closeErr = wrapSSHError(waitErr, session.stderr.String())
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
