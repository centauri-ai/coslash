package verification

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

// fakeRunner answers checks from a table so no test spawns a real process.
type fakeRunner struct {
	mutex    sync.Mutex
	answers  map[string]ExecResult
	fallback ExecResult
	calls    []call
	// observedTimeout records the bound each check was given.
	observedTimeout time.Duration
}

type call struct {
	Argv      []string
	Directory string
}

func (r *fakeRunner) Run(
	_ context.Context,
	argv []string,
	directory string,
	timeout time.Duration,
) (ExecResult, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.calls = append(r.calls, call{Argv: argv, Directory: directory})
	r.observedTimeout = timeout
	if answer, ok := r.answers[argv[0]+" "+strings.Join(argv[1:], " ")]; ok {
		return answer, nil
	}
	return r.fallback, nil
}

func newScope(t *testing.T) (*runfs.Scope, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	scope, err := runfs.OpenScope(root, runfs.ScopeOptions{})
	if err != nil {
		t.Fatalf("OpenScope: %v", err)
	}
	t.Cleanup(func() { _ = scope.Close() })
	return scope, root
}

func fixedClock() func() time.Time {
	base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	var ticks int
	return func() time.Time {
		ticks++
		return base.Add(time.Duration(ticks) * time.Second)
	}
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *Error, got %v", err)
	}
	return typed.Code
}

func baseOptions(t *testing.T, runner Runner, checks []Check) RunOptions {
	t.Helper()
	scope, root := newScope(t)
	return RunOptions{
		Scope:          scope,
		RunRoot:        root,
		Instance:       1,
		ChangeRevision: 7,
		PatchSha256:    strings.Repeat("a", 64),
		Checks:         checks,
		Runner:         runner,
		Now:            fixedClock(),
	}
}

// ---------------------------------------------------------------------------
// Verdicts
// ---------------------------------------------------------------------------

func TestVerdictPassedWhenEveryCheckExitsZero(t *testing.T) {
	runner := &fakeRunner{fallback: ExecResult{Stdout: []byte("ok\n")}}
	options := baseOptions(t, runner, []Check{
		{Name: "unit", Argv: []string{"go", "test", "./..."}},
		{Name: "lint", Argv: []string{"eslint", "."}},
	})

	document, err := Run(t.Context(), options)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if document.Verdict != VerdictPassed {
		t.Fatalf("Verdict = %q, want passed", document.Verdict)
	}
	if len(document.Checks) != 2 {
		t.Fatalf("Checks = %d, want 2", len(document.Checks))
	}
	if document.ChangeRevision != 7 || document.SchemaVersion != 1 {
		t.Fatalf("document header = %+v", document)
	}
	for _, result := range document.Checks {
		contents, err := options.Scope.ReadFile(t.Context(), result.LogPath)
		if err != nil {
			t.Fatalf("read %s: %v", result.LogPath, err)
		}
		if !strings.Contains(string(contents), "ok") {
			t.Fatalf("log %s missing check output", result.LogPath)
		}
	}
}

func TestVerdictFailedWhenAnyCheckExitsNonZero(t *testing.T) {
	runner := &fakeRunner{
		answers: map[string]ExecResult{
			"eslint .": {ExitCode: 1, Stdout: []byte("2 problems\n")},
		},
		fallback: ExecResult{},
	}
	options := baseOptions(t, runner, []Check{
		{Name: "unit", Argv: []string{"go", "test", "./..."}},
		{Name: "lint", Argv: []string{"eslint", "."}},
	})

	document, err := Run(t.Context(), options)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if document.Verdict != VerdictFailed {
		t.Fatalf("Verdict = %q, want failed", document.Verdict)
	}
	// A failing check is a verdict, not a runner error: every check still ran.
	if len(document.Checks) != 2 {
		t.Fatalf("Checks = %d, want both checks to have run", len(document.Checks))
	}
}

func TestVerdictSkippedWhenNoChecksAreConfigured(t *testing.T) {
	runner := &fakeRunner{}
	document, err := Run(t.Context(), baseOptions(t, runner, nil))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// An unconfigured board must not satisfy a publish gate by omission alone.
	if document.Verdict != VerdictSkipped {
		t.Fatalf("Verdict = %q, want skipped", document.Verdict)
	}
	if len(runner.calls) != 0 {
		t.Fatal("a check ran despite an empty configuration")
	}
}

func TestTimeoutIsRecordedInTheLog(t *testing.T) {
	runner := &fakeRunner{fallback: ExecResult{ExitCode: TimeoutExitCode, TimedOut: true}}
	options := baseOptions(t, runner, []Check{{Name: "slow", Argv: []string{"go", "test", "./..."}}})
	options.Policy.Timeout = 30 * time.Second

	document, err := Run(t.Context(), options)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if document.Verdict != VerdictFailed || document.Checks[0].ExitCode != TimeoutExitCode {
		t.Fatalf("timeout was not recorded as a failure: %+v", document.Checks[0])
	}
	if runner.observedTimeout != 30*time.Second {
		t.Fatalf("timeout passed to the runner = %v, want 30s", runner.observedTimeout)
	}
	contents, err := options.Scope.ReadFile(t.Context(), document.Checks[0].LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(contents), "timed out") {
		t.Fatal("the log does not record the timeout")
	}
}

func TestOversizedOutputIsTruncatedInTheMiddle(t *testing.T) {
	head := strings.Repeat("H", int(MaxLogBytes))
	tail := strings.Repeat("T", int(MaxLogBytes))
	runner := &fakeRunner{fallback: ExecResult{Stdout: []byte(head + tail)}}
	options := baseOptions(t, runner, []Check{{Name: "loud", Argv: []string{"go", "test", "./..."}}})

	document, err := Run(t.Context(), options)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !document.Checks[0].Truncated {
		t.Fatal("Truncated = false for an oversized log")
	}
	contents, err := options.Scope.ReadFile(t.Context(), document.Checks[0].LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	text := string(contents)
	// Both ends survive: the head shows what ran, the tail shows how it ended.
	if !strings.Contains(text, "+ \"go\"") {
		t.Fatal("the invocation was truncated away")
	}
	if !strings.Contains(text, "TTTT") {
		t.Fatal("the tail was truncated away")
	}
	if !strings.Contains(text, "truncated") {
		t.Fatal("the truncation notice is missing")
	}
}

func TestCheckLogsAreWrittenPerInstance(t *testing.T) {
	runner := &fakeRunner{}
	options := baseOptions(t, runner, []Check{{Name: "unit", Argv: []string{"go", "test"}}})
	options.Instance = 3

	document, err := Run(t.Context(), options)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := ".fleetlog/run/verify/3/unit.log"; document.Checks[0].LogPath != want {
		t.Fatalf("LogPath = %q, want %q", document.Checks[0].LogPath, want)
	}
}

func TestCheckNameIsSanitizedIntoTheLogFilename(t *testing.T) {
	runner := &fakeRunner{}
	options := baseOptions(t, runner, []Check{{Name: "unit tests 1.0", Argv: []string{"go", "test"}}})

	document, err := Run(t.Context(), options)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := ".fleetlog/run/verify/1/unit_tests_1.0.log"; document.Checks[0].LogPath != want {
		t.Fatalf("LogPath = %q, want %q", document.Checks[0].LogPath, want)
	}
}

// ---------------------------------------------------------------------------
// Policy
// ---------------------------------------------------------------------------

func TestValidateChecksRefusesUnsafeConfiguration(t *testing.T) {
	tests := map[string][]Check{
		"command outside the allowlist": {{Name: "evil", Argv: []string{"curl", "https://example.test"}}},
		"absolute path command":         {{Name: "evil", Argv: []string{"/bin/sh", "-c", "id"}}},
		"empty argv":                    {{Name: "empty", Argv: nil}},
		"control character in argv":     {{Name: "sneaky", Argv: []string{"go", "test\x00--"}}},
		"newline in argv":               {{Name: "sneaky", Argv: []string{"go", "test\ncurl evil"}}},
		"untrimmed argv token":          {{Name: "sneaky", Argv: []string{"go", " test "}}},
		"invalid name":                  {{Name: "../escape", Argv: []string{"go", "test"}}},
		"empty name":                    {{Name: "", Argv: []string{"go", "test"}}},
		"duplicate names": {
			{Name: "unit", Argv: []string{"go", "test"}},
			{Name: "unit", Argv: []string{"go", "vet"}},
		},
	}
	for name, checks := range tests {
		t.Run(name, func(t *testing.T) {
			if err := ValidateChecks(checks, Policy{}); err == nil {
				t.Fatal("an unsafe configuration was accepted")
			} else if codeOf(t, err) != CodePolicyViolation {
				t.Fatalf("code = %q, want %q", codeOf(t, err), CodePolicyViolation)
			}
		})
	}

	if err := ValidateChecks([]Check{{Name: "unit", Argv: []string{"npm", "test"}}}, Policy{}); err != nil {
		t.Fatalf("a valid configuration was refused: %v", err)
	}
}

func TestPolicyBoundsCheckCount(t *testing.T) {
	checks := make([]Check, 5)
	for index := range checks {
		checks[index] = Check{Name: "check" + string(rune('a'+index)), Argv: []string{"go", "test"}}
	}
	if err := ValidateChecks(checks, Policy{MaxChecks: 3}); err == nil {
		t.Fatal("a board exceeding MaxChecks was accepted")
	}
	if err := ValidateChecks(checks, Policy{MaxChecks: 8}); err != nil {
		t.Fatalf("a board within MaxChecks was refused: %v", err)
	}
}

func TestPolicyAllowlistIsOverridable(t *testing.T) {
	checks := []Check{{Name: "custom", Argv: []string{"bazel", "test", "//..."}}}
	if err := ValidateChecks(checks, Policy{}); err == nil {
		t.Fatal("bazel was accepted by the default allowlist")
	}
	if err := ValidateChecks(checks, Policy{AllowedCommands: []string{"bazel"}}); err != nil {
		t.Fatalf("an explicitly allowed command was refused: %v", err)
	}
}

func TestRunRefusesInvalidChecksBeforeRunningAnything(t *testing.T) {
	runner := &fakeRunner{}
	options := baseOptions(t, runner, []Check{
		{Name: "ok", Argv: []string{"go", "test"}},
		{Name: "bad", Argv: []string{"curl", "https://example.test"}},
	})

	if _, err := Run(t.Context(), options); codeOf(t, err) != CodePolicyViolation {
		t.Fatal("Run accepted an unsafe check list")
	}
	// Nothing may execute when the configuration as a whole is refused.
	if len(runner.calls) != 0 {
		t.Fatalf("%d checks ran despite a refused configuration", len(runner.calls))
	}
}

func TestRunRequiresScopeAndRunRoot(t *testing.T) {
	if _, err := Run(t.Context(), RunOptions{RunRoot: "/tmp"}); codeOf(t, err) != CodeRunNotReady {
		t.Fatal("a missing scope was accepted")
	}
	scope, _ := newScope(t)
	if _, err := Run(t.Context(), RunOptions{Scope: scope}); codeOf(t, err) != CodeRunNotReady {
		t.Fatal("a missing run root was accepted")
	}
}

func TestChecksRunInTheRunRoot(t *testing.T) {
	runner := &fakeRunner{}
	options := baseOptions(t, runner, []Check{{Name: "unit", Argv: []string{"go", "test"}}})

	if _, err := Run(t.Context(), options); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if runner.calls[0].Directory != options.RunRoot {
		t.Fatalf("check ran in %q, want the run root %q", runner.calls[0].Directory, options.RunRoot)
	}
}

// ---------------------------------------------------------------------------
// Environment and errors
// ---------------------------------------------------------------------------

func TestCheckEnvironmentIsAnAllowlist(t *testing.T) {
	original := lookupEnv
	t.Cleanup(func() { lookupEnv = original })
	lookupEnv = func(name string) (string, bool) {
		values := map[string]string{
			"PATH":          "/usr/bin",
			"HOME":          "/home/test",
			"GH_TOKEN":      "super-secret",
			"AWS_SECRET":    "super-secret",
			"GIT_DIR":       "/elsewhere/.git",
			"GIT_WORK_TREE": "/elsewhere",
		}
		value, ok := values[name]
		return value, ok
	}

	environment := allowedEnvironment()
	joined := strings.Join(environment, "\n")
	for _, forbidden := range []string{"GH_TOKEN", "AWS_SECRET", "GIT_DIR", "GIT_WORK_TREE"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("%s reached a check environment", forbidden)
		}
	}
	if !strings.Contains(joined, "PATH=/usr/bin") || !strings.Contains(joined, "HOME=/home/test") {
		t.Fatalf("the allowlisted entries are missing: %v", environment)
	}
}

func TestErrorsWithholdCheckOutput(t *testing.T) {
	err := newError(CodeCheckFailed, "a verification check could not be run").
		withDetail("/Users/private/path: secret contents")
	if strings.Contains(err.Error(), "secret contents") || strings.Contains(err.Error(), "/Users/private") {
		t.Fatalf("the client-facing message leaked detail: %q", err.Error())
	}
	if err.Detail() == "" {
		t.Fatal("Detail() is empty, so the diagnostic was lost rather than withheld")
	}
	if !errors.Is(err, ErrVerification) {
		t.Fatal("errors.Is(err, ErrVerification) = false")
	}
}

// ---------------------------------------------------------------------------
// Real runner
// ---------------------------------------------------------------------------

func TestExecRunnerBoundsOutputAndReportsExitCodes(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh available")
	}
	runner := ExecRunner{}

	// A missing executable is a configuration failure surfaced as a non-zero
	// exit, not a runner error, so the verdict stays honest.
	result, err := runner.Run(t.Context(), []string{"definitely-not-a-real-binary"}, t.TempDir(), time.Second)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatal("a missing executable reported success")
	}
}

func TestExecRunnerHonoursTimeout(t *testing.T) {
	if _, err := os.Stat("/bin/sleep"); err != nil {
		t.Skip("no /bin/sleep available")
	}
	runner := ExecRunner{}

	began := time.Now()
	result, err := runner.Run(t.Context(), []string{"/bin/sleep", "30"}, t.TempDir(), 200*time.Millisecond)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.TimedOut || result.ExitCode != TimeoutExitCode {
		t.Fatalf("result = %+v, want a recorded timeout", result)
	}
	if elapsed := time.Since(began); elapsed > 10*time.Second {
		t.Fatalf("the timeout did not bound the check: %v", elapsed)
	}
}
