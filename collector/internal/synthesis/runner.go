package synthesis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

const defaultModel = "claude-haiku-4-5"

const synthesisSchema = `{"type":"object","properties":{"goals":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":4},"outcome":{"type":"string"},"keyDecisions":{"type":"array","items":{"type":"string"},"maxItems":5},"nextStep":{"type":"string"}},"required":["goals","outcome","keyDecisions","nextStep"],"additionalProperties":false}`

type Runner interface {
	Run(context.Context, string) (session.SessionSynthesis, error)
}

type CLIRunner struct {
	Bin     string
	Model   string
	Timeout time.Duration
}

func NewCLIRunner() *CLIRunner {
	model := os.Getenv("COSLASH_SYNTHESIS_MODEL")
	if model == "" {
		model = defaultModel
	}
	return &CLIRunner{Bin: "claude", Model: model, Timeout: 90 * time.Second}
}

func (r *CLIRunner) ModelName() string {
	return r.Model
}

func (r *CLIRunner) Run(ctx context.Context, input string) (session.SessionSynthesis, error) {
	bin := r.Bin
	if bin == "" {
		bin = "claude"
	}
	model := r.Model
	if model == "" {
		model = defaultModel
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin,
		"-p",
		"--model", model,
		"--output-format", "json",
		"--json-schema", synthesisSchema,
		"--system-prompt", systemPrompt,
		"--tools", "",
		"--safe-mode",
		"--no-session-persistence",
	)
	cmd.Dir = SynthesisCwd()
	cmd.Stdin = strings.NewReader(input)
	output, err := cmd.Output()
	if runCtx.Err() != nil {
		return session.SessionSynthesis{}, runCtx.Err()
	}
	if err != nil {
		var exitErr *exec.ExitError
		switch {
		case errors.Is(err, exec.ErrNotFound):
			return session.SessionSynthesis{}, fmt.Errorf("claude CLI not found: %w", err)
		case errors.As(err, &exitErr):
			return session.SessionSynthesis{}, fmt.Errorf("claude CLI exited: %w: %s", err,
				strings.TrimSpace(string(exitErr.Stderr)))
		default:
			return session.SessionSynthesis{}, fmt.Errorf("run claude CLI: %w", err)
		}
	}
	return parseEnvelope(output)
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
	result := stripJSONFence(envelope.Result)
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
