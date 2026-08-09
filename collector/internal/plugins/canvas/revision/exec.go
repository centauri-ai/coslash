package revision

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultMaxOutputBytes bounds a single captured stream. A patch is the largest
// thing this package reads back, and MaxPatchBytes is well under this.
const DefaultMaxOutputBytes int64 = 64 << 20

// DefaultTimeout bounds any single git invocation. Clone of a large repository
// is the slow case; everything else finishes in milliseconds.
const DefaultTimeout = 10 * time.Minute

// Command is one git invocation. Args never pass through a shell: the argv is
// the entire interface, so no caller-supplied text can become a command.
type Command struct {
	Args []string
	// Dir is the working directory. Empty runs in the process working directory,
	// which callers avoid by passing -C or an explicit Dir.
	Dir string
	// Env holds extra KEY=VALUE entries merged over the hardened base.
	Env []string
	// MaxOutputBytes bounds stdout and stderr independently. Zero selects
	// DefaultMaxOutputBytes.
	MaxOutputBytes int64
	// Timeout bounds the invocation. Zero selects DefaultTimeout.
	Timeout time.Duration
}

// Result is the captured outcome of one invocation. Stdout and Stderr are raw
// bytes because a patch is not guaranteed to be valid UTF-8.
type Result struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	// Truncated reports that a stream hit MaxOutputBytes and was cut.
	Truncated bool
}

// Runner executes git. Tests substitute a fake; production uses ExecRunner.
type Runner interface {
	Run(ctx context.Context, command Command) (Result, error)
}

// environmentAllowlist names the process environment entries forwarded to git.
//
// The legacy implementation forwarded the entire process environment and then
// overrode four variables. An allowlist is the safer direction: a stray
// GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE, or GIT_OBJECT_DIRECTORY in the
// collector's environment would otherwise silently redirect every operation
// here away from the run root.
var environmentAllowlist = []string{
	"PATH",
	"HOME",
	"SSH_AUTH_SOCK",
	"TMPDIR",
}

// hardenedEnvironment builds the environment for one invocation.
//
// The global and system config files live outside the run root, so an agent
// cannot edit them — but the user may have set something that changes what the
// controller measures, and evidence capture has to be reproducible. LC_ALL
// fixes git's own message locale so parsing stays stable.
func hardenedEnvironment(extra []string) []string {
	environment := make([]string, 0, len(environmentAllowlist)+len(extra)+6)
	for _, name := range environmentAllowlist {
		if value, ok := os.LookupEnv(name); ok {
			environment = append(environment, name+"="+value)
		}
	}
	environment = append(environment,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"LC_ALL=C",
		"LANG=C",
	)
	return append(environment, extra...)
}

// ExecRunner runs the real git binary with an explicit argv and no shell.
type ExecRunner struct {
	// Binary overrides the executable name. Tests point this at a fake git.
	Binary string
}

// NewExecRunner returns a runner bound to the git found on PATH.
func NewExecRunner() *ExecRunner { return &ExecRunner{} }

func (r *ExecRunner) binary() string {
	if r.Binary != "" {
		return r.Binary
	}
	return "git"
}

// Run executes the command and captures bounded output. A non-zero exit is
// reported through Result, not through error; error is reserved for failures to
// start, context cancellation, and timeouts.
func (r *ExecRunner) Run(ctx context.Context, command Command) (Result, error) {
	if len(command.Args) == 0 {
		return Result{}, newError(CodeGitFailed, "git invocation has no arguments")
	}
	limit := command.MaxOutputBytes
	if limit <= 0 {
		limit = DefaultMaxOutputBytes
	}
	timeout := command.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	process := exec.CommandContext(ctx, r.binary(), command.Args...)
	process.Dir = command.Dir
	process.Env = hardenedEnvironment(command.Env)
	process.Stdin = nil

	var stdout, stderr boundedBuffer
	stdout.limit = limit
	stderr.limit = limit
	process.Stdout = &stdout
	process.Stderr = &stderr

	runError := process.Run()
	result := Result{
		Stdout:    stdout.Bytes(),
		Stderr:    stderr.Bytes(),
		Truncated: stdout.truncated || stderr.truncated,
	}

	if runError == nil {
		return result, nil
	}
	var exitError *exec.ExitError
	if errors.As(runError, &exitError) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, newError(CodeGitFailed, "git timed out").
				withDetail(string(result.Stderr)).withCause(ctxErr)
		}
		result.ExitCode = exitError.ExitCode()
		return result, nil
	}
	return result, newError(CodeGitFailed, "git could not be started").
		withDetail(runError.Error()).withCause(runError)
}

// boundedBuffer accumulates up to limit bytes and silently drops the rest,
// recording that it did. A hostile repository cannot exhaust memory through
// command output.
type boundedBuffer struct {
	buffer    bytes.Buffer
	limit     int64
	truncated bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	remaining := b.limit - int64(b.buffer.Len())
	if remaining <= 0 {
		b.truncated = true
		return len(data), nil
	}
	if int64(len(data)) > remaining {
		b.buffer.Write(data[:remaining])
		b.truncated = true
		return len(data), nil
	}
	b.buffer.Write(data)
	return len(data), nil
}

func (b *boundedBuffer) Bytes() []byte { return b.buffer.Bytes() }

// Git wraps a Runner with the hardening every controller invocation needs.
//
// The `-c` flags below apply to EVERY invocation because config inside a run
// root is agent-writable. Without core.hooksPath and core.attributesFile,
// `git add -A` runs agent-authored hooks and clean filters, and evidence
// capture becomes an execution vector.
type Git struct {
	runner Runner
	// hooksDirectory must be a real, empty directory: git treats a missing
	// hooksPath as unset and silently falls back to the repository's own hooks.
	hooksDirectory string
}

// NewGit binds a runner to an empty hooks directory, creating the directory
// when it does not exist. The directory must be outside every run root.
func NewGit(runner Runner, hooksDirectory string) (*Git, error) {
	if runner == nil {
		return nil, newError(CodeGitFailed, "a git runner is required")
	}
	if hooksDirectory == "" || !filepath.IsAbs(hooksDirectory) {
		return nil, newError(CodeInvalidPath, "the hooks directory must be an absolute path")
	}
	if err := os.MkdirAll(hooksDirectory, 0o700); err != nil {
		return nil, newError(CodeInvalidPath, "the hooks directory could not be created").
			withDetail(err.Error()).withCause(err)
	}
	entries, err := os.ReadDir(hooksDirectory)
	if err != nil {
		return nil, newError(CodeInvalidPath, "the hooks directory could not be read").
			withDetail(err.Error()).withCause(err)
	}
	if len(entries) != 0 {
		return nil, newError(CodeInvalidPath, "the hooks directory must be empty")
	}
	return &Git{runner: runner, hooksDirectory: hooksDirectory}, nil
}

// HooksDirectory exposes the empty hooks directory so sibling packages can
// harden their own git invocations identically.
func (g *Git) HooksDirectory() string { return g.hooksDirectory }

func (g *Git) hardenedArgs(args []string) []string {
	hardened := make([]string, 0, len(args)+8)
	hardened = append(hardened,
		"-c", "core.hooksPath="+g.hooksDirectory,
		"-c", "core.attributesFile=/dev/null",
		"-c", "push.default=nothing",
		"-c", "protocol.ext.allow=never",
	)
	return append(hardened, args...)
}

// Run executes a hardened git command and returns its raw result, including
// non-zero exits.
func (g *Git) Run(ctx context.Context, command Command) (Result, error) {
	command.Args = g.hardenedArgs(command.Args)
	return g.runner.Run(ctx, command)
}

// Output runs a command that must succeed and returns its trimmed stdout.
func (g *Git) Output(ctx context.Context, command Command) (string, error) {
	result, err := g.Run(ctx, command)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		operation := "git"
		if len(command.Args) > 0 {
			operation = "git " + command.Args[0]
		}
		return "", newError(CodeGitFailed, fmt.Sprintf("%s failed", operation)).
			withDetail(strings.TrimSpace(string(result.Stderr)))
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

// Try runs a command whose failure is an expected answer rather than an error,
// such as probing for a ref that may not exist. It returns ok=false on any
// non-zero exit.
func (g *Git) Try(ctx context.Context, command Command) (string, bool) {
	result, err := g.Run(ctx, command)
	if err != nil || result.ExitCode != 0 {
		return "", false
	}
	return strings.TrimSpace(string(result.Stdout)), true
}

// RawOutput runs a command that must succeed and returns untrimmed stdout
// bytes. Patches are read this way: trimming would change their hash.
func (g *Git) RawOutput(ctx context.Context, command Command) ([]byte, error) {
	result, err := g.Run(ctx, command)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		operation := "git"
		if len(command.Args) > 0 {
			operation = "git " + command.Args[0]
		}
		return nil, newError(CodeGitFailed, fmt.Sprintf("%s failed", operation)).
			withDetail(strings.TrimSpace(string(result.Stderr)))
	}
	if result.Truncated {
		return nil, newError(CodePatchTooLarge, "git output exceeded the configured limit")
	}
	return result.Stdout, nil
}
