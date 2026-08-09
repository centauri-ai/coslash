package agentexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const testUUID = "123e4567-e89b-42d3-a456-426614174000"

func TestBuildUsesExplicitArgvAndAllowlists(t *testing.T) {
	dir := t.TempDir()
	request := Request{
		Vendor: Claude, Mode: Fork, CWD: dir, SessionID: testUUID,
		ParentVendor: Claude, ParentSessionID: "223e4567-e89b-42d3-a456-426614174000",
		Model: "opus", Effort: "high", Permission: "acceptEdits",
		Prompt: "review; touch /tmp/pwned\nthen stop", Environment: map[string]string{"TERM": "xterm-256color"},
	}
	command, err := Build(request)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--resume", request.ParentSessionID, "--fork-session", "--session-id", testUUID, "--model", "opus", "--effort", "high", "--permission-mode", "acceptEdits", request.Prompt}
	if !reflect.DeepEqual(command.Args, want) {
		t.Fatalf("argv = %#v, want %#v", command.Args, want)
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if command.Path != "claude" || command.Dir != resolvedDir {
		t.Fatalf("command = %#v", command)
	}
	if strings.Contains(strings.Join(command.Args[:len(command.Args)-1], " "), request.Prompt) {
		t.Fatal("prompt was interpolated into a command token")
	}
	for _, entry := range command.Env {
		if strings.HasPrefix(entry, "AWS_") || strings.HasPrefix(entry, "GITHUB_") {
			t.Fatalf("secret-bearing environment leaked: %q", entry)
		}
	}
}

func TestBuildRejectsUnsafeAndCrossVendorInputs(t *testing.T) {
	dir := t.TempDir()
	tests := []Request{
		{Vendor: "sh", Mode: Start, CWD: dir},
		{Vendor: Claude, Mode: Start, CWD: dir, SessionID: "$(touch pwned)"},
		{Vendor: Codex, Mode: Fork, CWD: dir, ParentVendor: Claude, ParentSessionID: testUUID},
		{Vendor: Codex, Mode: Start, CWD: dir, Model: "gpt-5.6-sol;sh"},
		{Vendor: Codex, Mode: Start, CWD: dir, Model: "gpt-5.6-luna", Effort: "ultra"},
		{Vendor: Claude, Mode: Start, CWD: dir, SessionID: testUUID, Environment: map[string]string{"LD_PRELOAD": "evil"}},
	}
	for index, request := range tests {
		if _, err := Build(request); err == nil {
			t.Fatalf("case %d unexpectedly succeeded", index)
		}
	}
}

func TestBuildHeadlessVendorArgv(t *testing.T) {
	dir := t.TempDir()
	claude, err := Build(Request{Vendor: Claude, Mode: Start, CWD: dir, SessionID: testUUID, Model: "sonnet", Effort: "medium", Permission: "acceptEdits", Prompt: "do it", Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(claude.Args, " ") != "-p --output-format stream-json --verbose --session-id "+testUUID+" --model sonnet --effort medium --permission-mode acceptEdits --max-turns 40" {
		t.Fatalf("Claude argv = %#v", claude.Args)
	}
	if claude.Stdin != "do it" {
		t.Fatal("headless prompt must use stdin")
	}
	codex, err := Build(Request{Vendor: Codex, Mode: Resume, CWD: dir, ParentSessionID: "thread_abc", Model: "gpt-5.6-sol", Effort: "high", Permission: "workspace-write", Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(codex.Args, " ") != `exec resume thread_abc --json --model gpt-5.6-sol --sandbox workspace-write -c model_reasoning_effort="high" -c approval_policy="never"` {
		t.Fatalf("Codex argv = %#v", codex.Args)
	}
}

func TestBuildCodexInteractiveFork(t *testing.T) {
	command, err := Build(Request{Vendor: Codex, Mode: Fork, CWD: t.TempDir(), ParentVendor: Codex, ParentSessionID: "thread_parent", Model: "gpt-5.6-terra", Effort: "ultra", Permission: "workspace-write", Prompt: "try a branch"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"fork", "thread_parent", "--model", "gpt-5.6-terra", "--sandbox", "workspace-write", "-c", `model_reasoning_effort="ultra"`, "try a branch"}
	if !reflect.DeepEqual(command.Args, want) {
		t.Fatalf("argv = %#v, want %#v", command.Args, want)
	}
}

func TestEnvironmentFilterIsExplicit(t *testing.T) {
	environment, err := filteredEnvironment([]string{"PATH=/bin", "AWS_SECRET_ACCESS_KEY=secret", "CODEX_HOME=/safe/codex"}, map[string]string{"TERM": "xterm"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "secret") || !strings.Contains(joined, "PATH=/bin") || !strings.Contains(joined, "CODEX_HOME=/safe/codex") || !strings.Contains(joined, "TERM=xterm") {
		t.Fatalf("environment = %q", joined)
	}
}

func TestCanonicalDirectoryResolvesSymlink(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "cwd")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	command, err := Build(Request{Vendor: Codex, Mode: Start, CWD: link})
	if err != nil {
		t.Fatal(err)
	}
	resolvedReal, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if command.Dir != resolvedReal {
		t.Fatalf("dir = %q, want %q", command.Dir, resolvedReal)
	}
}

type fakeExecutor struct {
	output  []byte
	err     error
	command Command
	block   bool
}

func (fake *fakeExecutor) Run(ctx context.Context, command Command, _ int64) ([]byte, error) {
	fake.command = command
	if fake.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return fake.output, fake.err
}

func TestRunHeadlessCapturesCodexThreadAndHidesProcessErrors(t *testing.T) {
	fake := &fakeExecutor{output: []byte("{\"type\":\"thread.started\",\"thread_id\":\"thread_123\"}\n")}
	result, err := (Runner{Executor: fake}).RunHeadless(context.Background(), Request{Vendor: Codex, Mode: Start, CWD: t.TempDir(), Model: "gpt-5.6-sol", Effort: "high", Permission: "read-only", Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID != "thread_123" || fake.command.Stdin != "hello" {
		t.Fatalf("result=%#v command=%#v", result, fake.command)
	}
	fake.err = errors.New("secret stderr")
	_, err = (Runner{Executor: fake}).RunHeadless(context.Background(), Request{Vendor: Codex, Mode: Start, CWD: t.TempDir()})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe error = %v", err)
	}
}

func TestRunHeadlessReportsMissingBinarySafely(t *testing.T) {
	fake := &fakeExecutor{err: exec.ErrNotFound}
	_, err := (Runner{Executor: fake}).RunHeadless(context.Background(), Request{Vendor: Codex, Mode: Start, CWD: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunHeadlessTimeout(t *testing.T) {
	fake := &fakeExecutor{block: true}
	_, err := (Runner{Executor: fake}).RunHeadless(context.Background(), Request{Vendor: Claude, Mode: Start, CWD: t.TempDir(), SessionID: testUUID, Timeout: 5 * time.Millisecond})
	if !errors.Is(err, ErrTimedOut) {
		t.Fatalf("err = %v", err)
	}
}

func TestCaptureSessionIDIgnoresMalformedAndOversizedEvents(t *testing.T) {
	claude := []byte("bad\n{\"session_id\":\"123e4567-e89b-42d3-a456-426614174000\"}\n")
	if got := CaptureSessionID(Claude, claude); got != testUUID {
		t.Fatalf("Claude id = %q", got)
	}
	codex := []byte("{\"type\":\"other\",\"thread_id\":\"wrong\"}\n{\"type\":\"thread.started\",\"thread_id\":\"thread_ok\"}\n")
	if got := CaptureSessionID(Codex, codex); got != "thread_ok" {
		t.Fatalf("Codex id = %q", got)
	}
}

func TestLimitedBufferBoundsMemoryAndReportsOverflow(t *testing.T) {
	buffer := &limitedBuffer{remaining: 3}
	if count, err := buffer.Write([]byte("abcdef")); err != nil || count != 6 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if !buffer.exceeded || buffer.String() != "abc" {
		t.Fatalf("buffer=%q exceeded=%v", buffer.String(), buffer.exceeded)
	}
}
