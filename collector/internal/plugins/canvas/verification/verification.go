// Package verification owns bounded workflow verification commands.
//
// Verification runs argv-only checks against a frozen revision. There is no
// model in the loop: a check either exits zero or it does not.
//
// Running every configured check to completion and the verdict those checks
// produce are deliberately separate facts. A component that ran all its checks
// succeeded even when the verdict is "failed"; repair routing reads the verdict,
// while the controller reads the error. Collapsing the two makes a failing test
// suite indistinguishable from a broken runner.
//
// Nothing here performs a workflow stage transition. Run returns a document; the
// controller decides what that document means for the run.
package verification

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

// Bounds applied to every check.
const (
	MaxLogBytes     int64 = 1 << 20
	MaxArgvTokens         = 64
	MaxArgvTokenLen       = 512
	MaxChecks             = 32
	DefaultTimeout        = 10 * time.Minute
	TimeoutExitCode       = 124
)

// AllowedCommands is the default executable allowlist.
//
// It does not stop a repository from running arbitrary code — `npm test` runs
// whatever package.json says. The boundary it draws is narrower and still worth
// having: a board cannot introduce an executable the repository does not
// already invoke.
var AllowedCommands = []string{
	"npm", "pnpm", "yarn", "bun", "node", "deno",
	"make", "just", "cargo", "go", "python3", "pytest", "ruff", "tsc",
	"vitest", "jest", "eslint", "prettier",
	"mvn", "gradle", "dotnet", "swift",
	"bundle", "rake", "rspec", "phpunit", "composer",
}

// checkNamePattern bounds a name that is only ever shown to a human and used as
// a log filename stem, so it is restricted to characters safe in both roles.
var checkNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}$`)

// Verdict is the outcome the repair router reads.
type Verdict string

const (
	VerdictPassed  Verdict = "passed"
	VerdictFailed  Verdict = "failed"
	VerdictSkipped Verdict = "skipped"
)

// Check is one configured command.
type Check struct {
	Name string   `json:"name"`
	Argv []string `json:"argv"`
}

// Result records one executed check. LogPath is relative to the run root.
type Result struct {
	Name       string   `json:"name"`
	Argv       []string `json:"argv"`
	ExitCode   int      `json:"exitCode"`
	DurationMs int64    `json:"durationMs"`
	LogPath    string   `json:"logPath"`
	Truncated  bool     `json:"truncated"`
}

// Document is the verification record bound to one frozen revision.
type Document struct {
	SchemaVersion  uint64    `json:"schemaVersion"`
	ChangeRevision uint64    `json:"changeRevision"`
	PatchSha256    string    `json:"patchSha256"`
	Verdict        Verdict   `json:"verdict"`
	Checks         []Result  `json:"checks"`
	StartedAt      time.Time `json:"startedAt"`
	FinishedAt     time.Time `json:"finishedAt"`
}

// ExecResult is one command outcome as reported by a Runner.
type ExecResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	TimedOut bool
}

// Runner executes a check. Tests substitute a fake; production uses ExecRunner.
type Runner interface {
	Run(ctx context.Context, argv []string, directory string, timeout time.Duration) (ExecResult, error)
}

// environmentAllowlist names the process environment entries forwarded to a
// check. A check is repository code, so it needs a toolchain on PATH and a HOME
// for package caches, but nothing that would let it reach the collector's own
// credentials.
var environmentAllowlist = []string{"PATH", "HOME", "LANG", "TMPDIR"}

// ExecRunner runs a real check with an explicit argv and no shell.
type ExecRunner struct{}

// Run executes argv in directory with a hard timeout and bounded output.
func (ExecRunner) Run(
	ctx context.Context,
	argv []string,
	directory string,
	timeout time.Duration,
) (ExecResult, error) {
	if len(argv) == 0 {
		return ExecResult{}, newError(CodeInvalidCheck, "the check argv is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	process := exec.CommandContext(ctx, argv[0], argv[1:]...)
	process.Dir = directory
	process.Env = allowedEnvironment()
	process.Stdin = nil

	var stdout, stderr boundedBuffer
	stdout.limit = MaxLogBytes
	stderr.limit = MaxLogBytes
	process.Stdout = &stdout
	process.Stderr = &stderr

	runError := process.Run()
	result := ExecResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if runError == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		result.TimedOut = true
		result.ExitCode = TimeoutExitCode
		return result, nil
	}
	var exitError *exec.ExitError
	if errors.As(runError, &exitError) {
		result.ExitCode = exitError.ExitCode()
		return result, nil
	}
	// A check whose executable is missing is a configuration failure, not a
	// test failure. It is reported as a non-zero exit so the verdict is honest,
	// with the cause withheld from the client-facing message.
	result.ExitCode = 127
	result.Stderr = append(result.Stderr, []byte("\ncheck could not be started\n")...)
	return result, nil
}

func allowedEnvironment() []string {
	environment := make([]string, 0, len(environmentAllowlist)+1)
	for _, name := range environmentAllowlist {
		if value, ok := lookupEnv(name); ok {
			environment = append(environment, name+"="+value)
		}
	}
	return append(environment, "CI=1")
}

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

// Policy bounds what a board may configure.
type Policy struct {
	// AllowedCommands overrides the default executable allowlist.
	AllowedCommands []string
	// Timeout bounds one check. Zero selects DefaultTimeout.
	Timeout time.Duration
	// MaxChecks bounds how many checks one board may declare. Zero selects
	// MaxChecks.
	MaxChecks int
}

func (p Policy) allowed() map[string]bool {
	commands := p.AllowedCommands
	if len(commands) == 0 {
		commands = AllowedCommands
	}
	allowed := make(map[string]bool, len(commands))
	for _, command := range commands {
		allowed[command] = true
	}
	return allowed
}

func (p Policy) timeout() time.Duration {
	if p.Timeout <= 0 {
		return DefaultTimeout
	}
	return p.Timeout
}

func (p Policy) maxChecks() int {
	if p.MaxChecks <= 0 {
		return MaxChecks
	}
	return p.MaxChecks
}

// ValidateChecks refuses any configuration this package will not run.
//
// Argv tokens are exec'd directly, so shell metacharacters are harmless.
// Control characters are not: they can truncate or confuse the exec boundary
// and the logs.
func ValidateChecks(checks []Check, policy Policy) error {
	if len(checks) > policy.maxChecks() {
		return newError(CodePolicyViolation,
			fmt.Sprintf("a board may declare at most %d checks", policy.maxChecks()))
	}
	allowed := policy.allowed()
	seen := make(map[string]bool, len(checks))
	for index, check := range checks {
		if !checkNamePattern.MatchString(check.Name) {
			return newError(CodePolicyViolation, fmt.Sprintf("check [%d] has an invalid name", index))
		}
		if seen[check.Name] {
			// Two checks with one name would overwrite each other's log.
			return newError(CodePolicyViolation, fmt.Sprintf("check [%d] repeats a check name", index))
		}
		seen[check.Name] = true
		if len(check.Argv) == 0 {
			return newError(CodePolicyViolation, fmt.Sprintf("check [%d] has an empty argv", index))
		}
		if len(check.Argv) > MaxArgvTokens {
			return newError(CodePolicyViolation, fmt.Sprintf("check [%d] has too many argv tokens", index))
		}
		if !allowed[check.Argv[0]] {
			return newError(CodePolicyViolation,
				fmt.Sprintf("check [%d] uses a command that is not allowed", index))
		}
		for _, token := range check.Argv {
			if !validArgvToken(token) {
				return newError(CodePolicyViolation,
					fmt.Sprintf("check [%d] has an invalid argv token", index))
			}
		}
	}
	return nil
}

func validArgvToken(token string) bool {
	if token == "" || len(token) > MaxArgvTokenLen {
		return false
	}
	if strings.TrimSpace(token) != token {
		return false
	}
	for _, character := range token {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

// RunOptions binds one verification pass to a run root and a frozen revision.
type RunOptions struct {
	// Scope is rooted at the run root and owns every log write.
	Scope *runfs.Scope
	// RunRoot is the absolute directory checks execute in.
	RunRoot string
	// Instance separates repeated verification passes in the log layout.
	Instance int
	// ChangeRevision and PatchSha256 bind the document to a frozen revision.
	ChangeRevision uint64
	PatchSha256    string
	Checks         []Check
	Policy         Policy
	// Runner defaults to ExecRunner.
	Runner Runner
	// Now defaults to time.Now.
	Now func() time.Time
}

// Run executes every configured check and returns the verification document.
//
// An empty check list yields a "skipped" verdict rather than a vacuous "passed",
// so a board that forgot to configure verification cannot satisfy a publish gate
// by omission alone. Whether "skipped" is acceptable is the gate's decision.
func Run(ctx context.Context, options RunOptions) (Document, error) {
	if options.Scope == nil {
		return Document{}, newError(CodeRunNotReady, "verification requires a run scope")
	}
	if options.RunRoot == "" {
		return Document{}, newError(CodeRunNotReady, "verification requires a run root")
	}
	if options.Instance < 1 {
		options.Instance = 1
	}
	if err := ValidateChecks(options.Checks, options.Policy); err != nil {
		return Document{}, err
	}
	runner := options.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}

	startedAt := now().UTC()
	logDirectory := path.Join(".fleetlog", "run", "verify", strconv.Itoa(options.Instance))
	if err := options.Scope.MkdirAll(ctx, logDirectory); err != nil {
		return Document{}, newError(CodeRunNotReady, "the verification log directory could not be created").
			withDetail(err.Error()).withCause(err)
	}

	results := make([]Result, 0, len(options.Checks))
	for _, check := range options.Checks {
		began := now()
		execResult, err := runner.Run(ctx, check.Argv, options.RunRoot, options.Policy.timeout())
		if err != nil {
			return Document{}, newError(CodeCheckFailed, "a verification check could not be run").
				withDetail(err.Error()).withCause(err)
		}
		duration := now().Sub(began)

		transcript, truncated := renderTranscript(check, execResult, options.Policy.timeout())
		logPath := path.Join(logDirectory, logFileName(check.Name))
		if err := options.Scope.AtomicWrite(ctx, logPath, transcript); err != nil {
			return Document{}, newError(CodeCheckFailed, "a verification log could not be written").
				withDetail(err.Error()).withCause(err)
		}
		results = append(results, Result{
			Name:       check.Name,
			Argv:       check.Argv,
			ExitCode:   execResult.ExitCode,
			DurationMs: duration.Milliseconds(),
			LogPath:    logPath,
			Truncated:  truncated,
		})
	}

	verdict := VerdictSkipped
	if len(results) > 0 {
		verdict = VerdictPassed
		for _, result := range results {
			if result.ExitCode != 0 {
				verdict = VerdictFailed
				break
			}
		}
	}

	return Document{
		SchemaVersion:  1,
		ChangeRevision: options.ChangeRevision,
		PatchSha256:    options.PatchSha256,
		Verdict:        verdict,
		Checks:         results,
		StartedAt:      startedAt,
		FinishedAt:     now().UTC(),
	}, nil
}

// renderTranscript builds the stored log, truncating the middle so both the
// invocation and the final failure survive. Keeping only the head would hide
// the error; keeping only the tail would hide what ran.
func renderTranscript(check Check, result ExecResult, timeout time.Duration) ([]byte, bool) {
	var builder strings.Builder
	builder.WriteString("+ ")
	for index, token := range check.Argv {
		if index > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteString(strconv.Quote(token))
	}
	builder.WriteString("\n\n")
	builder.Write(result.Stdout)
	if len(result.Stderr) > 0 {
		builder.WriteString("\n--- stderr ---\n")
		builder.Write(result.Stderr)
	}
	if result.TimedOut {
		builder.WriteString(fmt.Sprintf("\ncheck timed out after %s\n", timeout))
	}
	return truncateMiddle([]byte(builder.String()), MaxLogBytes)
}

func truncateMiddle(data []byte, limit int64) ([]byte, bool) {
	if int64(len(data)) <= limit {
		return data, false
	}
	half := int(limit / 2)
	dropped := int64(len(data)) - limit
	notice := fmt.Sprintf("\n\n…[truncated %d bytes]…\n\n", dropped)
	out := make([]byte, 0, limit+int64(len(notice)))
	out = append(out, data[:half]...)
	out = append(out, notice...)
	out = append(out, data[len(data)-half:]...)
	return out, true
}

func logFileName(name string) string {
	var builder strings.Builder
	for _, character := range name {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '.', character == '_', character == '-':
			builder.WriteRune(character)
		default:
			builder.WriteByte('_')
		}
	}
	return builder.String() + ".log"
}
