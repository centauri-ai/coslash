package remote

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
)

const (
	HelperCommandCapabilities = "version"
	HelperCommandCollect      = "collect"

	// MaxHelperPathBytes bounds the installed helper path the Mac selects.
	MaxHelperPathBytes = 256
	// DefaultCapabilityTimeout bounds the handshake, which reads a few hundred
	// bytes and must not inherit the collection deadline.
	DefaultCapabilityTimeout = 20 * time.Second
)

// helperExitGrace bounds waiting for a helper that stopped writing but has not
// exited. Past it the process group is terminated. It is a variable so tests can
// shorten it.
var helperExitGrace = 5 * time.Second

// helperPathPattern is deliberately narrow: the installed path is chosen by the
// Mac, never by a request or by remote output, and only these characters can
// appear in a versioned install directory.
var helperPathPattern = regexp.MustCompile(`^(/|~/)[A-Za-z0-9._][A-Za-z0-9._/-]*$`)

var (
	ErrInvalidHelperPath   = errors.New("invalid helper path")
	ErrInvalidHelperArgs   = errors.New("invalid helper command")
	ErrHelperOutputLimit   = errors.New("helper output exceeds the response limit")
	ErrHelperMissing       = errors.New("helper is not installed")
	ErrHelperBlocked       = errors.New("helper is not executable")
	ErrHelperIncompatible  = errors.New("helper protocol is incompatible")
	ErrHelperFailed        = errors.New("helper failed")
	ErrHelperPartial       = errors.New("helper reported partial coverage")
	ErrHelperRequestBounds = errors.New("collect request exceeds the byte limit")
)

// HelperArgs builds the exec argv for one fixed helper subcommand. Request data
// never appears here: the request travels on stdin, so the remote command line
// is a validated path plus one word from a closed set.
func HelperArgs(alias, helperPath, subcommand string, connectTimeoutSeconds int) ([]string, error) {
	if subcommand != HelperCommandCapabilities && subcommand != HelperCommandCollect {
		return nil, fmt.Errorf("%w: %q", ErrInvalidHelperArgs, subcommand)
	}
	command, err := helperCommand(helperPath, subcommand)
	if err != nil {
		return nil, err
	}
	return sshCommandArgs(alias, connectTimeoutSeconds, command)
}

func sshCommandArgs(alias string, connectTimeoutSeconds int, remoteCommand string) ([]string, error) {
	args, err := sshArgs(alias, connectTimeoutSeconds)
	if err != nil {
		return nil, err
	}
	return append(args, remoteCommand), nil
}

// helperCommand renders the remote command. OpenSSH hands the command to the
// remote shell, so the path is quoted and a home-relative install expands only
// the fixed $HOME name.
func helperCommand(helperPath, subcommand string) (string, error) {
	if helperPath == "" || len(helperPath) > MaxHelperPathBytes {
		return "", fmt.Errorf("%w: length", ErrInvalidHelperPath)
	}
	if !helperPathPattern.MatchString(helperPath) {
		return "", fmt.Errorf("%w: unsupported characters", ErrInvalidHelperPath)
	}
	if strings.Contains(helperPath, "..") {
		return "", fmt.Errorf("%w: relative traversal", ErrInvalidHelperPath)
	}
	if relative, ok := strings.CutPrefix(helperPath, "~/"); ok {
		if relative == "" {
			return "", fmt.Errorf("%w: empty home-relative path", ErrInvalidHelperPath)
		}
		return `"$HOME"/` + shellQuote(relative) + " " + subcommand, nil
	}
	// The pattern already required an absolute path here.
	return shellQuote(helperPath) + " " + subcommand, nil
}

// shellQuote wraps a value so the remote shell treats it as one literal word.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// HelperResult reports one collect exchange. The proposal holds every record
// that applied cleanly for diagnostics, even when the response was cut short;
// only RequestComplete makes that proposal eligible for durable publication.
type HelperResult struct {
	Capabilities    remoteprotocol.Capabilities
	Proposal        remoteprotocol.Generation
	Coverage        []AgentCoverage
	Records         int
	ResponseBytes   int
	RequestBytes    int
	RequestComplete bool
	ExitCode        int
	Stderr          string
	RoundTrip       time.Duration
}

// HelperCapabilities runs the handshake subcommand and returns the helper's
// supported ranges. Compatibility is decided from overlapping ranges, so a
// helper built from a different commit than this Mac is still usable.
func HelperCapabilities(
	ctx context.Context,
	alias, helperPath string,
	options OpenOptions,
) (remoteprotocol.Capabilities, string, error) {
	limits := options.Limits.withDefaults()
	args, err := HelperArgs(
		alias, helperPath, HelperCommandCapabilities, int(limits.ConnectTimeout.Seconds()),
	)
	if err != nil {
		return remoteprotocol.Capabilities{}, "", err
	}
	runCtx, cancel := context.WithTimeout(ctx, DefaultCapabilityTimeout)
	defer cancel()
	process, err := startHelper(runCtx, alias, args, cancel, options)
	if err != nil {
		return remoteprotocol.Capabilities{}, "", err
	}
	_ = process.stdin.Close()
	capabilities, decodeErr := remoteprotocol.DecodeCapabilities(process.stdout)
	exitCode, waitErr := process.finish(decodeErr != nil)
	stderr := process.stderr.String()
	if reason := helperProcessError(runCtx, exitCode, waitErr, process); reason != nil {
		return remoteprotocol.Capabilities{}, stderr, reason
	}
	if decodeErr != nil {
		return remoteprotocol.Capabilities{}, stderr, fmt.Errorf(
			"%w: %w", ErrHelperIncompatible, decodeErr,
		)
	}
	if !capabilities.Compatible() {
		return capabilities, stderr, fmt.Errorf(
			"%w: helper offers protocol %d-%d, schema %d-%d",
			ErrHelperIncompatible, capabilities.Protocol.Min, capabilities.Protocol.Max,
			capabilities.Schema.Min, capabilities.Schema.Max,
		)
	}
	return capabilities, stderr, nil
}

// HelperCollect runs one collect exchange against the accumulator. It returns
// the proposed generation and a reason; the caller decides whether a partial
// proposal is worth committing.
func HelperCollect(
	ctx context.Context,
	alias, helperPath string,
	request remoteprotocol.Request,
	baseline remoteprotocol.Generation,
	options OpenOptions,
) (HelperResult, error) {
	started := time.Now()
	accumulator, err := remoteprotocol.NewAccumulator(request, baseline)
	if err != nil {
		return HelperResult{}, err
	}
	payload, err := marshalRequestLine(request)
	if err != nil {
		return HelperResult{}, err
	}
	limits := options.Limits.withDefaults()
	args, err := HelperArgs(
		alias, helperPath, HelperCommandCollect, int(limits.ConnectTimeout.Seconds()),
	)
	if err != nil {
		return HelperResult{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, limits.Deadline)
	defer cancel()
	process, err := startHelper(runCtx, alias, args, cancel, options)
	if err != nil {
		return HelperResult{}, err
	}
	written := process.writeStdin(payload)

	requestCompleted := make(chan struct{})
	streamed := make(chan streamOutcome, 1)
	go func() {
		streamed <- streamRecords(process.stdout, request, accumulator, requestCompleted)
	}()
	var stream streamOutcome
	var exitCode int
	var waitErr error
	select {
	case stream = <-streamed:
		// Reap before collecting the write result: a helper that answered without
		// draining stdin would otherwise leave the writer blocked on a full pipe.
		// Clean EOF can carry a meaningful helper or shell exit status; finish is
		// already bounded if the child closed stdout without exiting.
		exitCode, waitErr = process.finish(stream.err != nil)
	case <-requestCompleted:
		// Keep draining through EOF so trailing output is rejected. Wait must run
		// after the drain because os/exec closes StdoutPipe during Wait.
		select {
		case stream = <-streamed:
		case <-time.After(helperExitGrace):
			process.terminationRequested = terminateProcessGroup(process.cmd)
			stream = <-streamed
		}
		exitCode, waitErr = process.finish(false)
	}
	writeErr := <-written
	result := HelperResult{
		Proposal: accumulator.Proposal(), Records: stream.records,
		Coverage:      stream.coverage,
		ResponseBytes: stream.bytes, RequestBytes: len(payload),
		RequestComplete: stream.complete, ExitCode: exitCode,
		Stderr: process.stderr.String(), RoundTrip: time.Since(started),
	}
	if reason := helperProcessError(runCtx, exitCode, waitErr, process); reason != nil {
		return result, reason
	}
	if stream.err != nil {
		return result, stream.err
	}
	if writeErr != nil {
		return result, fmt.Errorf("send collect request: %w", writeErr)
	}
	if !stream.complete {
		// A helper that ran and could not finish enumerating is partial coverage,
		// not a failure: the families it did publish stay in the proposal.
		if exitCode == helperExitPartial {
			return result, ErrHelperPartial
		}
		return result, fmt.Errorf("%w: response ended before completion", ErrHelperFailed)
	}
	return result, nil
}

func marshalRequestLine(request remoteprotocol.Request) ([]byte, error) {
	payload, err := remoteprotocol.EncodeRequest(request)
	if err != nil {
		if errors.Is(err, remoteprotocol.ErrRequestBounds) {
			return nil, ErrHelperRequestBounds
		}
		return nil, err
	}
	return payload, nil
}

type streamOutcome struct {
	records  int
	bytes    int
	coverage []AgentCoverage
	complete bool
	err      error
}

// streamRecords applies each whole record as it arrives. Records are applied
// incrementally so validation remains bounded and the caller can inspect a
// failed proposal; no partial record reaches it and an incomplete proposal is
// never durably published.
func streamRecords(
	reader io.Reader,
	request remoteprotocol.Request,
	accumulator *remoteprotocol.Accumulator,
	requestCompleted chan<- struct{},
) streamOutcome {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), request.Limits.MaxRecordBytes+1)
	outcome := streamOutcome{}
	for scanner.Scan() {
		line := scanner.Bytes()
		outcome.bytes += len(line) + 1
		if outcome.bytes > request.Limits.MaxResponseBytes {
			outcome.err = ErrHelperOutputLimit
			return outcome
		}
		if len(line) > request.Limits.MaxRecordBytes {
			outcome.err = ErrHelperOutputLimit
			return outcome
		}
		record, err := decodeRecordLine(line)
		if err != nil {
			outcome.err = err
			return outcome
		}
		if err := accumulator.Apply(record); err != nil {
			outcome.err = fmt.Errorf("%w: %w", ErrHelperFailed, err)
			return outcome
		}
		if record.Type == remoteprotocol.RecordVendorComplete {
			outcome.coverage = append(outcome.coverage, AgentCoverage{
				Agent: record.Vendor, CandidateFiles: record.Counts.CandidateFiles,
				SelectedFiles: record.Counts.SelectedFiles,
				Truncated:     !record.InventoryComplete || record.Counts.SkippedFamilies > 0,
			})
		}
		outcome.records++
		if record.Type == remoteprotocol.RecordRequestComplete {
			outcome.complete = true
			close(requestCompleted)
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			outcome.err = ErrHelperOutputLimit
			return outcome
		}
		outcome.err = fmt.Errorf("read helper response: %w", err)
	}
	return outcome
}

func decodeRecordLine(line []byte) (remoteprotocol.Record, error) {
	if len(bytes.TrimSpace(line)) == 0 {
		return remoteprotocol.Record{}, fmt.Errorf("%w: blank record", ErrHelperFailed)
	}
	record, err := remoteprotocol.DecodeRecord(line)
	if err != nil {
		return remoteprotocol.Record{}, fmt.Errorf("%w: decode record: %w", ErrHelperFailed, err)
	}
	return record, nil
}

// helperProcess owns one SSH child and its bounded pipes.
type helperProcess struct {
	cmd                  *exec.Cmd
	stdin                io.WriteCloser
	stdout               io.ReadCloser
	stderr               *cappedStderr
	terminationRequested bool
}

func startHelper(
	ctx context.Context,
	alias string,
	args []string,
	cancel context.CancelFunc,
	options OpenOptions,
) (*helperProcess, error) {
	limits := options.Limits.withDefaults()
	// Injected commands are unit-test fakes; skip real OpenSSH master setup there.
	if options.command == nil {
		if err := ensureControlMaster(ctx, alias, options); err != nil {
			return nil, err
		}
	}
	bin := options.SSHBin
	if bin == "" {
		bin = "ssh"
	}
	command := options.command
	if command == nil {
		command = exec.CommandContext
	}
	cmd := command(ctx, bin, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open SSH stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open SSH stdout: %w", err)
	}
	stderr := &cappedStderr{limit: limits.MaxStderrBytes, cancel: cancel}
	cmd.Stderr = stderr
	if err := startProcessGroup(cmd); err != nil {
		return nil, fmt.Errorf("start SSH: %w", err)
	}
	return &helperProcess{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

func waitProcessGroupContext(ctx context.Context, cmd *exec.Cmd) error {
	return waitProcessContext(ctx, func() error { return waitProcessGroup(cmd) }, func() {
		terminateProcessGroup(cmd)
	})
}

func waitInteractiveProcessContext(ctx context.Context, cmd *exec.Cmd) error {
	return waitProcessContext(ctx, func() error { return waitProcessGroup(cmd) }, func() {
		terminateInteractiveProcess(cmd)
	})
}

func waitProcessContext(ctx context.Context, wait func() error, terminate func()) error {
	waited := make(chan error, 1)
	go func() { waited <- wait() }()
	select {
	case err := <-waited:
		return err
	case <-ctx.Done():
		terminate()
		return <-waited
	}
}

// writeStdin sends the request without blocking record reading, and reports the
// write result on a channel so the caller never leaves the goroutine running.
func (process *helperProcess) writeStdin(payload []byte) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := process.stdin.Write(payload)
		closeErr := process.stdin.Close()
		if err == nil && closeErr != nil && !benignSessionCloseErr(closeErr) {
			err = closeErr
		}
		done <- err
	}()
	return done
}

// finish reaps the child. A helper that stopped writing without exiting, or one
// abandoned mid-response, has its whole process group terminated so no orphan
// SSH client is left holding the pipes.
func (process *helperProcess) finish(aborted bool) (int, error) {
	if aborted {
		process.terminationRequested = terminateProcessGroup(process.cmd)
	}
	waited := make(chan error, 1)
	go func() { waited <- waitProcessGroup(process.cmd) }()
	select {
	case err := <-waited:
		return exitCodeOf(err), err
	case <-time.After(helperExitGrace):
		process.terminationRequested = terminateProcessGroup(process.cmd)
		err := <-waited
		return exitCodeOf(err), err
	}
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// helperProcessError separates SSH transport failure, a missing or blocked
// helper, and a helper that ran and reported partial coverage.
func helperProcessError(
	ctx context.Context,
	exitCode int,
	waitErr error,
	process *helperProcess,
) error {
	if process.stderr.overflow {
		return ErrStderrLimit
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// A child terminated by this side can report a platform-specific exit code;
	// the stream outcome or context carries the reason for ending it.
	if process.terminationRequested && processWasTerminated(exitCode) {
		return nil
	}
	switch exitCode {
	case 0, helperExitPartial:
		return nil
	case helperExitShellNotFound:
		return ErrHelperMissing
	case helperExitShellNotExecutable:
		return ErrHelperBlocked
	case helperExitBadRequest:
		return fmt.Errorf("%w: helper rejected the request", ErrHelperIncompatible)
	case helperExitResource:
		return ErrHelperOutputLimit
	case helperExitInternal:
		return wrapSSHError(ErrHelperFailed, process.stderr.String())
	case helperExitSSH:
		return wrapSSHError(
			fmt.Errorf("SSH exec failed: %w", waitErr), process.stderr.String(),
		)
	}
	if waitErr != nil {
		return wrapSSHError(waitErr, process.stderr.String())
	}
	return nil
}

// Exit codes the helper and the remote shell use.
const (
	helperExitPartial            = 3
	helperExitBadRequest         = 4
	helperExitInternal           = 5
	helperExitResource           = 6
	helperExitShellNotExecutable = 126
	helperExitShellNotFound      = 127
	helperExitSSH                = 255
)
