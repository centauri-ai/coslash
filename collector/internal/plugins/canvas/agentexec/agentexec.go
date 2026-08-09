package agentexec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	MaxPromptBytes  = 1 << 20
	MaxOutputBytes  = 8 << 20
	DefaultMaxTurns = 40
	DefaultTimeout  = 30 * time.Minute
	MaxTimeout      = 2 * time.Hour
)

type Vendor string

const (
	Claude Vendor = "claude"
	Codex  Vendor = "codex"
)

type Mode string

const (
	Start  Mode = "start"
	Resume Mode = "resume"
	Fork   Mode = "fork"
)

var (
	uuidPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	codexIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	modelPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)
)

var allowedModels = map[Vendor][]string{
	Claude: {"opus", "sonnet", "haiku", "fable"},
	Codex:  {"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"},
}

var allowedEfforts = map[Vendor][]string{
	Claude: {"low", "medium", "high", "xhigh", "max"},
	Codex:  {"low", "medium", "high", "xhigh", "max", "ultra"},
}

var allowedPermissions = map[Vendor][]string{
	Claude: {"acceptEdits", "bypassPermissions"},
	Codex:  {"read-only", "workspace-write"},
}

var inheritedEnvironment = map[string]struct{}{
	"HOME": {}, "PATH": {}, "SHELL": {}, "TMPDIR": {}, "LANG": {}, "LC_ALL": {},
	"LC_CTYPE": {}, "TERM": {}, "COLORTERM": {}, "NO_COLOR": {},
	"USER": {}, "LOGNAME": {}, "XDG_CONFIG_HOME": {}, "SSH_AUTH_SOCK": {},
	"CODEX_HOME": {}, "CLAUDE_CONFIG_DIR": {},
}

var requestEnvironment = map[string]struct{}{
	"LANG": {}, "LC_ALL": {}, "LC_CTYPE": {}, "TERM": {}, "COLORTERM": {}, "NO_COLOR": {},
}

type Request struct {
	Vendor          Vendor
	Mode            Mode
	CWD             string
	SessionID       string
	ParentVendor    Vendor
	ParentSessionID string
	Model           string
	Effort          string
	Permission      string
	Prompt          string
	Headless        bool
	MaxTurns        int
	Timeout         time.Duration
	Environment     map[string]string
}

type Command struct {
	Path              string
	Args              []string
	Dir               string
	Env               []string
	Stdin             string
	ExpectedSessionID string
}

type Result struct {
	SessionID string
	Output    []byte
	Duration  time.Duration
}

type Executor interface {
	Run(context.Context, Command, int64) ([]byte, error)
}

type Runner struct {
	Executor Executor
}

var (
	ErrOutputLimit = errors.New("agent output exceeded the configured limit")
	ErrTimedOut    = errors.New("agent execution timed out")
)

func Build(request Request) (Command, error) {
	if request.Vendor != Claude && request.Vendor != Codex {
		return Command{}, fmt.Errorf("agentexec: unsupported vendor %q", request.Vendor)
	}
	if request.Mode != Start && request.Mode != Resume && request.Mode != Fork {
		return Command{}, fmt.Errorf("agentexec: unsupported mode %q", request.Mode)
	}
	if len(request.Prompt) > MaxPromptBytes || strings.ContainsRune(request.Prompt, 0) {
		return Command{}, errors.New("agentexec: prompt is invalid or too large")
	}
	dir, err := canonicalDirectory(request.CWD)
	if err != nil {
		return Command{}, err
	}
	if err := validateProfile(request); err != nil {
		return Command{}, err
	}
	if request.Mode == Fork && request.ParentVendor != request.Vendor {
		return Command{}, errors.New("agentexec: cross-vendor forks are not supported")
	}
	if request.Mode != Start {
		if err := validateSessionID(request.Vendor, request.ParentSessionID); err != nil {
			return Command{}, fmt.Errorf("agentexec: parent session: %w", err)
		}
	}
	if request.Vendor == Claude && (request.Mode == Start || request.Mode == Fork) {
		if err := validateSessionID(Claude, request.SessionID); err != nil {
			return Command{}, fmt.Errorf("agentexec: session: %w", err)
		}
	}

	command := Command{Path: string(request.Vendor), Dir: dir}
	command.Env, err = filteredEnvironment(os.Environ(), request.Environment)
	if err != nil {
		return Command{}, err
	}
	if request.Headless {
		command.Args, command.ExpectedSessionID, err = headlessArgs(request)
		command.Stdin = request.Prompt
	} else {
		command.Args, command.ExpectedSessionID, err = interactiveArgs(request)
	}
	if err != nil {
		return Command{}, err
	}
	return command, nil
}

func (runner Runner) RunHeadless(ctx context.Context, request Request) (Result, error) {
	request.Headless = true
	command, err := Build(request)
	if err != nil {
		return Result{}, err
	}
	timeout := request.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	if timeout <= 0 || timeout > MaxTimeout {
		return Result{}, fmt.Errorf("agentexec: timeout must be positive and at most %s", MaxTimeout)
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	executor := runner.Executor
	if executor == nil {
		executor = osExecutor{}
	}
	output, err := executor.Run(runCtx, command, MaxOutputBytes)
	if runCtx.Err() != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrTimedOut, runCtx.Err())
	}
	if err != nil {
		if errors.Is(err, ErrOutputLimit) {
			return Result{}, err
		}
		if errors.Is(err, exec.ErrNotFound) {
			return Result{}, fmt.Errorf("agentexec: %s CLI is not installed", request.Vendor)
		}
		return Result{}, fmt.Errorf("agentexec: %s process failed", request.Vendor)
	}
	sessionID := command.ExpectedSessionID
	if captured := CaptureSessionID(request.Vendor, output); captured != "" {
		sessionID = captured
	}
	if sessionID == "" {
		return Result{}, errors.New("agentexec: process did not report a session id")
	}
	return Result{SessionID: sessionID, Output: output, Duration: time.Since(started)}, nil
}

func interactiveArgs(request Request) ([]string, string, error) {
	var args []string
	var expected string
	if request.Vendor == Claude {
		switch request.Mode {
		case Start:
			args = append(args, "--session-id", request.SessionID)
			expected = request.SessionID
		case Resume:
			args = append(args, "--resume", request.ParentSessionID)
			expected = request.ParentSessionID
		case Fork:
			args = append(args, "--resume", request.ParentSessionID, "--fork-session", "--session-id", request.SessionID)
			expected = request.SessionID
		}
		args = appendProfile(args, request)
	} else {
		switch request.Mode {
		case Resume:
			args = append(args, "resume", request.ParentSessionID)
			expected = request.ParentSessionID
		case Fork:
			args = append(args, "fork", request.ParentSessionID)
		}
		args = appendProfile(args, request)
	}
	if strings.TrimSpace(request.Prompt) != "" {
		args = append(args, request.Prompt)
	}
	return args, expected, nil
}

func headlessArgs(request Request) ([]string, string, error) {
	maxTurns := request.MaxTurns
	if maxTurns == 0 {
		maxTurns = DefaultMaxTurns
	}
	if maxTurns < 1 || maxTurns > 100 {
		return nil, "", errors.New("agentexec: max turns must be between 1 and 100")
	}
	if request.Vendor == Claude {
		args := []string{"-p", "--output-format", "stream-json", "--verbose"}
		expected := request.SessionID
		switch request.Mode {
		case Start:
			args = append(args, "--session-id", request.SessionID)
		case Resume:
			args = append(args, "--resume", request.ParentSessionID)
			expected = request.ParentSessionID
		case Fork:
			args = append(args, "--resume", request.ParentSessionID, "--fork-session", "--session-id", request.SessionID)
		}
		args = appendProfile(args, request)
		args = append(args, "--max-turns", fmt.Sprint(maxTurns))
		return args, expected, nil
	}
	args := []string{"exec"}
	expected := ""
	if request.Mode == Resume {
		args = append(args, "resume", request.ParentSessionID)
		expected = request.ParentSessionID
	} else if request.Mode == Fork {
		return nil, "", errors.New("agentexec: bounded Codex fork is not supported by the frozen CLI contract")
	}
	args = append(args, "--json")
	args = appendProfile(args, request)
	args = append(args, "-c", `approval_policy="never"`)
	return args, expected, nil
}

func appendProfile(args []string, request Request) []string {
	if request.Model != "" {
		args = append(args, "--model", request.Model)
	}
	if request.Vendor == Claude {
		if request.Effort != "" {
			args = append(args, "--effort", request.Effort)
		}
		if request.Permission != "" {
			args = append(args, "--permission-mode", request.Permission)
		}
		return args
	}
	if request.Permission != "" {
		args = append(args, "--sandbox", request.Permission)
	}
	if request.Effort != "" {
		args = append(args, "-c", fmt.Sprintf(`model_reasoning_effort=%q`, request.Effort))
	}
	return args
}

func validateProfile(request Request) error {
	if request.Model != "" && (!modelPattern.MatchString(request.Model) || !slices.Contains(allowedModels[request.Vendor], request.Model)) {
		return fmt.Errorf("agentexec: model %q is not allowed for %s", request.Model, request.Vendor)
	}
	if request.Effort != "" && !slices.Contains(allowedEfforts[request.Vendor], request.Effort) {
		return fmt.Errorf("agentexec: effort %q is not allowed for %s", request.Effort, request.Vendor)
	}
	if request.Vendor == Codex && request.Effort == "ultra" && request.Model == "gpt-5.6-luna" {
		return errors.New("agentexec: ultra effort is not allowed for gpt-5.6-luna")
	}
	if request.Permission != "" && !slices.Contains(allowedPermissions[request.Vendor], request.Permission) {
		return fmt.Errorf("agentexec: permission %q is not allowed for %s", request.Permission, request.Vendor)
	}
	return nil
}

func validateSessionID(vendor Vendor, id string) error {
	if vendor == Claude && !uuidPattern.MatchString(id) {
		return errors.New("Claude session id must be a UUID")
	}
	if vendor == Codex && !codexIDPattern.MatchString(id) {
		return errors.New("Codex session id is invalid")
	}
	return nil
}

func canonicalDirectory(path string) (string, error) {
	if path == "" {
		return "", errors.New("agentexec: working directory is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("agentexec: resolve working directory")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", errors.New("agentexec: working directory is unavailable")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errors.New("agentexec: working directory is not a directory")
	}
	return resolved, nil
}

func filteredEnvironment(base []string, extra map[string]string) ([]string, error) {
	values := map[string]string{}
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			if _, allowed := inheritedEnvironment[key]; allowed {
				values[key] = value
			}
		}
	}
	for key, value := range extra {
		if _, allowed := requestEnvironment[key]; !allowed {
			return nil, fmt.Errorf("agentexec: environment variable %q is not allowed", key)
		}
		if len(value) > 4096 || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("agentexec: environment variable %q is invalid", key)
		}
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result, nil
}

func CaptureSessionID(vendor Vendor, output []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 1024), 1<<20)
	for scanner.Scan() {
		var event map[string]json.RawMessage
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if vendor == Claude {
			if id := jsonString(event["session_id"]); id != "" && uuidPattern.MatchString(id) {
				return id
			}
			continue
		}
		if jsonString(event["type"]) != "thread.started" {
			continue
		}
		if id := jsonString(event["thread_id"]); codexIDPattern.MatchString(id) {
			return id
		}
	}
	return ""
}

func jsonString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

type osExecutor struct{}

func (osExecutor) Run(ctx context.Context, command Command, limit int64) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir = command.Dir
	cmd.Env = command.Env
	cmd.Stdin = strings.NewReader(command.Stdin)
	buffer := &limitedBuffer{remaining: limit}
	cmd.Stdout = buffer
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if buffer.exceeded {
		return nil, ErrOutputLimit
	}
	return buffer.Bytes(), err
}

type limitedBuffer struct {
	bytes.Buffer
	remaining int64
	exceeded  bool
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	if int64(len(data)) > buffer.remaining {
		if buffer.remaining > 0 {
			_, _ = buffer.Buffer.Write(data[:buffer.remaining])
			buffer.remaining = 0
		}
		buffer.exceeded = true
		return len(data), nil
	}
	buffer.remaining -= int64(len(data))
	return buffer.Buffer.Write(data)
}
