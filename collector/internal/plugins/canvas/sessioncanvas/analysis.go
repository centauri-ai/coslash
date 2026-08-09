package sessioncanvas

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const (
	analysisSchema       = `{"type":"object","properties":{"intention":{"type":"string"},"planSummary":{"type":"string"},"status":{"type":"string"},"findings":{"type":"array","items":{"type":"string"},"maxItems":4},"issues":{"type":"array","items":{"type":"string"},"maxItems":4}},"required":["intention","planSummary","status","findings","issues"],"additionalProperties":false}`
	analysisSystemPrompt = "Analyze one coding-agent turn for an engineering manager. Return only the requested JSON. Be concrete and concise; report intention, approach, status, findings, and issues."
	maxAnalysisInput     = 8_000
	maxAnalysisOutput    = 1 << 20
)

type AnalysisCommand struct {
	Path  string
	Args  []string
	Dir   string
	Env   []string
	Stdin string
}

type AnalysisExecutor interface {
	Run(context.Context, AnalysisCommand, int64) ([]byte, error)
}

type CLIAnalyzer struct {
	Config   func() settings.SynthesisSettings
	WorkDir  string
	Timeout  time.Duration
	Executor AnalysisExecutor
}

func (analyzer CLIAnalyzer) Analyze(ctx context.Context, input TurnAnalysisInput) (TurnAnalysis, error) {
	if analyzer.Config == nil {
		return TurnAnalysis{}, ErrAnalysisDisabled
	}
	config := analyzer.Config()
	if !config.Enabled {
		return TurnAnalysis{}, ErrAnalysisDisabled
	}
	if !supportedSynthesis(config) {
		return TurnAnalysis{}, ErrAnalysisFailed
	}
	workDir := analyzer.WorkDir
	if workDir == "" {
		workDir = filepath.Join(settings.Home(), "synthesis")
	}
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return TurnAnalysis{}, ErrAnalysisFailed
	}
	if err := os.Chmod(workDir, 0o700); err != nil {
		return TurnAnalysis{}, ErrAnalysisFailed
	}
	command, cleanup, err := analysisCommand(config, workDir, buildAnalysisInput(input))
	if err != nil {
		return TurnAnalysis{}, ErrAnalysisFailed
	}
	defer cleanup()
	timeout := analyzer.Timeout
	if timeout == 0 {
		timeout = 90 * time.Second
	}
	if timeout < time.Second || timeout > 5*time.Minute {
		return TurnAnalysis{}, ErrAnalysisFailed
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	executor := analyzer.Executor
	if executor == nil {
		executor = analysisOSExecutor{}
	}
	output, err := executor.Run(runCtx, command, maxAnalysisOutput)
	if err != nil || runCtx.Err() != nil {
		return TurnAnalysis{}, ErrAnalysisFailed
	}
	if config.Backend == settings.BackendClaude {
		var envelope struct {
			Type    string `json:"type"`
			IsError bool   `json:"is_error"`
			Result  string `json:"result"`
		}
		if json.Unmarshal(output, &envelope) != nil || envelope.Type != "result" || envelope.IsError {
			return TurnAnalysis{}, ErrAnalysisFailed
		}
		output = []byte(envelope.Result)
	}
	var result TurnAnalysis
	if json.Unmarshal([]byte(stripJSONFence(string(output))), &result) != nil || validateAnalysis(result) != nil {
		return TurnAnalysis{}, ErrAnalysisFailed
	}
	return result, nil
}

func supportedSynthesis(config settings.SynthesisSettings) bool {
	for _, backend := range settings.BackendOptions() {
		if backend.ID != config.Backend {
			continue
		}
		for _, model := range backend.Models {
			if model.ID == config.Model {
				return true
			}
		}
	}
	return false
}

func analysisCommand(config settings.SynthesisSettings, workDir, input string) (AnalysisCommand, func(), error) {
	cleanup := func() {}
	environment := safeAnalysisEnvironment(os.Environ())
	switch config.Backend {
	case settings.BackendClaude:
		return AnalysisCommand{
			Path: "claude", Dir: workDir, Env: environment, Stdin: input,
			Args: []string{"-p", "--model", config.Model, "--output-format", "json", "--json-schema", analysisSchema,
				"--system-prompt", analysisSystemPrompt, "--tools", "", "--safe-mode", "--no-session-persistence"},
		}, cleanup, nil
	case settings.BackendCodex:
		schema, err := os.CreateTemp(workDir, ".turn-analysis-schema-*.json")
		if err != nil {
			return AnalysisCommand{}, cleanup, err
		}
		path := schema.Name()
		cleanup = func() { _ = os.Remove(path) }
		if err := schema.Chmod(0o600); err != nil {
			schema.Close()
			cleanup()
			return AnalysisCommand{}, func() {}, err
		}
		if _, err := io.WriteString(schema, analysisSchema); err != nil {
			schema.Close()
			cleanup()
			return AnalysisCommand{}, func() {}, err
		}
		if err := schema.Close(); err != nil {
			cleanup()
			return AnalysisCommand{}, func() {}, err
		}
		return AnalysisCommand{
			Path: "codex", Dir: workDir, Env: environment, Stdin: input,
			Args: []string{"exec", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--skip-git-repo-check",
				"--sandbox", "read-only", "--model", config.Model, "--output-schema", path, "--color", "never", analysisSystemPrompt},
		}, cleanup, nil
	default:
		return AnalysisCommand{}, cleanup, errors.New("unsupported backend")
	}
}

func buildAnalysisInput(input TurnAnalysisInput) string {
	turn := input.Turn
	lines := []string{
		"Session agent: " + input.Session.Agent,
		fmt.Sprintf("Turn: %d", turn.Index),
		"Request: " + clampAnalysis(turn.Prompt, 4_000),
		"Plan: " + clampAnalysis(valueOrEmpty(turn.PlanText), 2_000),
		fmt.Sprintf("Activity: %d tool uses, %d errors", turn.ToolUses, turn.Errors),
	}
	if len(turn.FileEdits) > 0 {
		files := turn.FileEdits
		if len(files) > 12 {
			files = files[:12]
		}
		lines = append(lines, "Files: "+strings.Join(files, ", "))
	}
	if len(turn.Todos) > 0 {
		lines = append(lines, "Checklist:")
		for _, todo := range turn.Todos {
			mark := " "
			if todo.Done {
				mark = "x"
			}
			lines = append(lines, "- ["+mark+"] "+clampAnalysis(todo.Text, 300))
		}
	}
	text := strings.Join(lines, "\n")
	return clampAnalysis(text, maxAnalysisInput)
}

func clampAnalysis(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stripJSONFence(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "```") {
		return value
	}
	if newline := strings.IndexByte(value, '\n'); newline >= 0 {
		value = value[newline+1:]
	}
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "```"))
	return value
}

type analysisOSExecutor struct{}

func (analysisOSExecutor) Run(ctx context.Context, command AnalysisCommand, limit int64) ([]byte, error) {
	process := exec.CommandContext(ctx, command.Path, command.Args...)
	process.Dir = command.Dir
	process.Env = command.Env
	process.Stdin = strings.NewReader(command.Stdin)
	buffer := &boundedAnalysisBuffer{remaining: limit}
	process.Stdout = buffer
	process.Stderr = io.Discard
	if err := process.Run(); err != nil {
		return nil, err
	}
	if buffer.exceeded {
		return nil, ErrAnalysisFailed
	}
	return buffer.Bytes(), nil
}

func safeAnalysisEnvironment(entries []string) []string {
	allowed := map[string]struct{}{
		"HOME": {}, "PATH": {}, "SHELL": {}, "TMPDIR": {}, "LANG": {}, "LC_ALL": {}, "LC_CTYPE": {},
		"TERM": {}, "COLORTERM": {}, "NO_COLOR": {}, "USER": {}, "LOGNAME": {}, "XDG_CONFIG_HOME": {},
		"SSH_AUTH_SOCK": {}, "CODEX_HOME": {}, "CLAUDE_CONFIG_DIR": {},
	}
	filtered := make([]string, 0, len(allowed))
	for _, entry := range entries {
		key, _, found := strings.Cut(entry, "=")
		if _, ok := allowed[key]; found && ok {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

type boundedAnalysisBuffer struct {
	bytes.Buffer
	remaining int64
	exceeded  bool
}

func (buffer *boundedAnalysisBuffer) Write(data []byte) (int, error) {
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
