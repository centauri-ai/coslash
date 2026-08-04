package synthesis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const defaultModel = "claude-haiku-4-5"

const synthesisSchema = `{"type":"object","properties":{"goals":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":4},"outcome":{"type":"string"},"keyDecisions":{"type":"array","items":{"type":"string"},"maxItems":5},"nextStep":{"type":"string"}},"required":["goals","outcome","keyDecisions","nextStep"],"additionalProperties":false}`

type Runner interface {
	Run(context.Context, string) (session.SessionSynthesis, error)
}

type commandSpec struct {
	bin   string
	args  []string
	dir   string
	stdin string
}

type commandExecutor func(context.Context, commandSpec) ([]byte, error)

func executeCommand(ctx context.Context, spec commandSpec) ([]byte, error) {
	cmd := exec.CommandContext(ctx, spec.bin, spec.args...)
	cmd.Dir = spec.dir
	cmd.Stdin = strings.NewReader(spec.stdin)
	return cmd.Output()
}

type CLIRunner struct {
	Backend string
	Bin     string
	Model   string
	Timeout time.Duration
	exec    commandExecutor
}

func NewClaudeRunner(model string) *CLIRunner {
	return &CLIRunner{Backend: settings.BackendClaude, Bin: "claude", Model: model, Timeout: 90 * time.Second}
}

func NewCodexRunner(model string) *CLIRunner {
	return &CLIRunner{Backend: settings.BackendCodex, Bin: "codex", Model: model, Timeout: 90 * time.Second}
}

func NewRunner(config settings.SynthesisSettings) (Runner, error) {
	if !config.Enabled {
		return nil, nil
	}
	switch config.Backend {
	case settings.BackendClaude:
		return NewClaudeRunner(config.Model), nil
	case settings.BackendCodex:
		return NewCodexRunner(config.Model), nil
	default:
		return nil, fmt.Errorf("unsupported synthesis backend %q", config.Backend)
	}
}

func (r *CLIRunner) ModelName() string {
	return r.Model
}

func (r *CLIRunner) Run(ctx context.Context, input string) (session.SessionSynthesis, error) {
	bin := r.Bin
	model := r.Model
	var label string
	var args []string
	var parse func([]byte) (session.SessionSynthesis, error)
	var schemaPath string
	switch r.Backend {
	case settings.BackendClaude:
		label = "Claude Code"
		parse = parseEnvelope
		if bin == "" {
			bin = "claude"
		}
		if model == "" {
			model = defaultModel
		}
		args = []string{
			"-p",
			"--model", model,
			"--output-format", "json",
			"--json-schema", synthesisSchema,
			"--system-prompt", systemPrompt,
			"--tools", "",
			"--safe-mode",
			"--no-session-persistence",
		}
	case settings.BackendCodex:
		label = "Codex"
		parse = parseSynthesis
		if bin == "" {
			bin = "codex"
		}
		if model == "" {
			model = "gpt-5.6-luna"
		}
		var err error
		schemaPath, err = writeSchemaFile()
		if err != nil {
			return session.SessionSynthesis{}, err
		}
		defer os.Remove(schemaPath)
		args = []string{
			"exec",
			"--ephemeral",
			"--ignore-user-config",
			"--ignore-rules",
			"--skip-git-repo-check",
			"--sandbox", "read-only",
			"--model", model,
			"--output-schema", schemaPath,
			"--color", "never",
			systemPrompt,
		}
	default:
		return session.SessionSynthesis{}, fmt.Errorf("unsupported synthesis backend %q", r.Backend)
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	executor := r.exec
	if executor == nil {
		executor = executeCommand
	}
	output, err := executor(runCtx, commandSpec{
		bin:   bin,
		args:  args,
		dir:   SynthesisCwd(),
		stdin: input,
	})
	if runCtx.Err() != nil {
		return session.SessionSynthesis{}, fmt.Errorf("%s synthesis timed out: %w", label, runCtx.Err())
	}
	if err != nil {
		return session.SessionSynthesis{}, safeCommandError(label, err)
	}
	return parse(output)
}

func writeSchemaFile() (string, error) {
	if err := os.MkdirAll(SynthesisCwd(), 0o700); err != nil {
		return "", fmt.Errorf("create synthesis directory: %w", err)
	}
	file, err := os.CreateTemp(SynthesisCwd(), ".schema-*.json")
	if err != nil {
		return "", fmt.Errorf("create synthesis schema: %w", err)
	}
	path := file.Name()
	remove := true
	defer func() {
		file.Close()
		if remove {
			os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return "", fmt.Errorf("secure synthesis schema: %w", err)
	}
	if _, err := io.WriteString(file, synthesisSchema); err != nil {
		return "", fmt.Errorf("write synthesis schema: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close synthesis schema: %w", err)
	}
	remove = false
	return path, nil
}

func safeCommandError(label string, err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s CLI is not installed or not on PATH: %w", label, err)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Errorf("%s synthesis failed; verify CLI authentication and the selected model: %w", label, err)
	}
	return fmt.Errorf("run %s synthesis: %w", label, err)
}

func parseEnvelope(data []byte) (session.SessionSynthesis, error) {
	var envelope struct {
		Type    string `json:"type"`
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return session.SessionSynthesis{}, fmt.Errorf("decode claude envelope: %w", err)
	}
	if envelope.Type != "result" {
		return session.SessionSynthesis{}, fmt.Errorf("unexpected claude envelope type %q", envelope.Type)
	}
	if envelope.IsError {
		return session.SessionSynthesis{}, errors.New("claude returned an error result")
	}
	return parseSynthesis([]byte(envelope.Result))
}

func parseSynthesis(data []byte) (session.SessionSynthesis, error) {
	result := stripJSONFence(string(data))
	var synthesis session.SessionSynthesis
	if err := json.Unmarshal([]byte(result), &synthesis); err != nil {
		return session.SessionSynthesis{}, fmt.Errorf("decode synthesis result: %w", err)
	}
	if err := normalize(&synthesis); err != nil {
		return session.SessionSynthesis{}, err
	}
	return synthesis, nil
}

func stripJSONFence(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "```") {
		return value
	}
	if newline := strings.IndexByte(value, '\n'); newline >= 0 {
		value = value[newline+1:]
	}
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, "```")
	return strings.TrimSpace(value)
}

func normalize(synthesis *session.SessionSynthesis) error {
	synthesis.Goals = dedupedLimited(synthesis.Goals, 4, func(goal string) string {
		return concise(goal, 200)
	})
	synthesis.Outcome = concise(synthesis.Outcome, 200)
	synthesis.NextStep = concise(synthesis.NextStep, 200)
	synthesis.KeyDecisions = dedupedLimited(synthesis.KeyDecisions, 5, func(decision string) string {
		return session.Truncate(strings.TrimSpace(decision), 140)
	})
	if len(synthesis.Goals) == 0 && synthesis.Outcome == "" {
		return errors.New("synthesis has no goal or outcome")
	}
	return nil
}

func dedupedLimited(values []string, limit int, clean func(string) string) []string {
	seen := map[string]struct{}{}
	kept := make([]string, 0, min(len(values), limit))
	for _, value := range values {
		value = clean(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		kept = append(kept, value)
		if len(kept) == limit {
			break
		}
	}
	return kept
}

func concise(value string, maximum int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	for index, current := range runes {
		if current != '.' && current != '!' && current != '?' {
			continue
		}
		if index+1 == len(runes) || unicode.IsSpace(runes[index+1]) {
			value = string(runes[:index+1])
			break
		}
	}
	return session.Truncate(strings.TrimSpace(value), maximum)
}
