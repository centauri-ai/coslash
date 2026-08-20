package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

var (
	ErrStdoutOverflow = errors.New("remote stdout exceeded limit")
	ErrStderrOverflow = errors.New("remote stderr exceeded limit")
	ErrCanceled       = errors.New("remote command canceled")
)

// RunLimits bounds one SSH invocation.
type RunLimits struct {
	Deadline  time.Duration
	MaxStdout int
	MaxStderr int
}

// RunResult is one completed SSH attempt without interpreting the payload.
type RunResult struct {
	Stdout     []byte
	Stderr     []byte
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
	Overflow   error
}

// ProcessRunner executes a local ssh process.
type ProcessRunner interface {
	Run(ctx context.Context, alias, remoteCommand string, stdin []byte, limits RunLimits) (RunResult, error)
}

type commandExecutor func(ctx context.Context, bin string, args []string, stdin []byte, limits RunLimits) (RunResult, error)

// Runner is the default OpenSSH ProcessRunner.
type Runner struct {
	SSHBin string
	exec   commandExecutor
	now    func() time.Time
}

// NewRunner returns an SSH runner using the system ssh binary.
func NewRunner() *Runner {
	return &Runner{SSHBin: "ssh", exec: executeSSH, now: time.Now}
}

func (r *Runner) Run(ctx context.Context, alias, remoteCommand string, stdin []byte, limits RunLimits) (RunResult, error) {
	args, err := SSHArgv(alias, remoteCommand)
	if err != nil {
		return RunResult{}, err
	}
	execFn := r.exec
	if execFn == nil {
		execFn = executeSSH
	}
	nowFn := r.now
	if nowFn == nil {
		nowFn = time.Now
	}
	bin := r.SSHBin
	if bin == "" {
		bin = "ssh"
	}
	if limits.Deadline <= 0 {
		limits.Deadline = SnapshotDeadline
	}
	if limits.MaxStdout <= 0 {
		limits.MaxStdout = SnapshotStdoutCap()
	}
	if limits.MaxStderr <= 0 {
		limits.MaxStderr = MaxStderrBytes
	}
	runCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		runCtx, cancel = context.WithTimeout(ctx, limits.Deadline)
	}
	defer cancel()
	started := nowFn()
	result, err := execFn(runCtx, bin, args, stdin, limits)
	if result.StartedAt.IsZero() {
		result.StartedAt = started
	}
	if result.FinishedAt.IsZero() {
		result.FinishedAt = nowFn()
	}
	return result, err
}

func executeSSH(ctx context.Context, bin string, args []string, stdin []byte, limits RunLimits) (RunResult, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr cappedBuffer
	stdout.limit = limits.MaxStdout
	stderr.limit = limits.MaxStderr
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	err := cmd.Run()
	result := RunResult{
		Stdout: stdout.bytes(),
		Stderr: stderr.bytes(),
	}
	if stdout.overflow {
		result.Overflow = ErrStdoutOverflow
	} else if stderr.overflow {
		result.Overflow = ErrStderrOverflow
	}
	if result.Overflow != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return result, result.Overflow
	}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return result, ErrCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.ExitCode = -1
		return result, nil
	}
	return result, err
}

type cappedBuffer struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	limit    int
	overflow bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.overflow {
		return len(p), nil
	}
	remaining := c.limit - c.buf.Len()
	if remaining <= 0 {
		c.overflow = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = c.buf.Write(p[:remaining])
		c.overflow = true
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *cappedBuffer) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]byte, c.buf.Len())
	copy(out, c.buf.Bytes())
	return out
}

// FakeRunner is a deterministic ProcessRunner for tests.
type FakeRunner struct {
	mu    sync.Mutex
	Calls []FakeCall
	Hook  func(call FakeCall) (RunResult, error)
}

type FakeCall struct {
	Alias         string
	RemoteCommand string
	Stdin         []byte
	Limits        RunLimits
}

func (f *FakeRunner) Run(ctx context.Context, alias, remoteCommand string, stdin []byte, limits RunLimits) (RunResult, error) {
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}
	if _, err := SSHArgv(alias, remoteCommand); err != nil {
		return RunResult{}, err
	}
	call := FakeCall{Alias: alias, RemoteCommand: remoteCommand, Stdin: append([]byte(nil), stdin...), Limits: limits}
	f.mu.Lock()
	f.Calls = append(f.Calls, call)
	hook := f.Hook
	f.mu.Unlock()
	if hook == nil {
		return RunResult{}, fmt.Errorf("FakeRunner hook not set")
	}
	return hook(call)
}

var _ io.Writer = (*cappedBuffer)(nil)
