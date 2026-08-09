package sessioncanvas

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/sessiondetail"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

type analysisExecutorFunc func(context.Context, AnalysisCommand, int64) ([]byte, error)

func (function analysisExecutorFunc) Run(ctx context.Context, command AnalysisCommand, limit int64) ([]byte, error) {
	return function(ctx, command, limit)
}

func analysisInput(prompt string) TurnAnalysisInput {
	return TurnAnalysisInput{
		Session: contracts.SessionIdentity{Agent: "codex", ID: sharedID},
		Turn:    sessiondetail.Turn{Index: 2, Prompt: prompt, ToolUses: 4, Errors: 1, Todos: []sessiondetail.Todo{{Text: "test it", Done: true}}},
	}
}

func TestCLIAnalyzerDisabledDoesNotExecute(t *testing.T) {
	called := false
	analyzer := CLIAnalyzer{
		Config: func() settings.SynthesisSettings { return settings.SynthesisSettings{Enabled: false} },
		Executor: analysisExecutorFunc(func(context.Context, AnalysisCommand, int64) ([]byte, error) {
			called = true
			return nil, nil
		}),
	}
	if _, err := analyzer.Analyze(context.Background(), analysisInput("prompt")); !errors.Is(err, ErrAnalysisDisabled) || called {
		t.Fatalf("error=%v called=%t", err, called)
	}
}

func TestCLIAnalyzerClaudeUsesFixedStructuredCommandAndBoundedInput(t *testing.T) {
	t.Setenv("CANVAS_TEST_SECRET", "must-not-reach-child")
	var captured AnalysisCommand
	result := TurnAnalysis{Intention: "fix it", PlanSummary: "inspect", Status: "running", Findings: []string{}, Issues: []string{}}
	encoded, _ := json.Marshal(result)
	envelope, _ := json.Marshal(map[string]any{"type": "result", "is_error": false, "result": string(encoded)})
	analyzer := CLIAnalyzer{
		Config: func() settings.SynthesisSettings {
			return settings.SynthesisSettings{Enabled: true, Backend: settings.BackendClaude, Model: "claude-haiku-4-5"}
		},
		WorkDir: t.TempDir(), Timeout: time.Second,
		Executor: analysisExecutorFunc(func(_ context.Context, command AnalysisCommand, limit int64) ([]byte, error) {
			captured = command
			if limit != maxAnalysisOutput {
				t.Fatalf("limit = %d", limit)
			}
			return envelope, nil
		}),
	}
	got, err := analyzer.Analyze(context.Background(), analysisInput(strings.Repeat("x", 20_000)))
	if err != nil || got.Intention != "fix it" {
		t.Fatalf("result=%#v err=%v", got, err)
	}
	if captured.Path != "claude" || !slices.Contains(captured.Args, "--json-schema") || !slices.Contains(captured.Args, "--no-session-persistence") || len(captured.Stdin) > maxAnalysisInput+len("…") {
		t.Fatalf("command = %#v", captured)
	}
	if strings.Contains(strings.Join(captured.Args, " "), strings.Repeat("x", 100)) {
		t.Fatal("user prompt reached argv")
	}
	if strings.Contains(strings.Join(captured.Env, "\n"), "CANVAS_TEST_SECRET") {
		t.Fatal("unapproved environment reached the analysis CLI")
	}
}

func TestCLIAnalyzerCodexUsesTemporarySchemaAndRemovesIt(t *testing.T) {
	var schemaPath string
	result := `{"intention":"fix","planSummary":"test","status":"done","findings":[],"issues":[]}`
	analyzer := CLIAnalyzer{
		Config: func() settings.SynthesisSettings {
			return settings.SynthesisSettings{Enabled: true, Backend: settings.BackendCodex, Model: "gpt-5.6-luna"}
		},
		WorkDir: t.TempDir(), Timeout: time.Second,
		Executor: analysisExecutorFunc(func(_ context.Context, command AnalysisCommand, _ int64) ([]byte, error) {
			if command.Path != "codex" || !slices.Contains(command.Args, "--ephemeral") || !slices.Contains(command.Args, "read-only") {
				t.Fatalf("command = %#v", command)
			}
			for index, arg := range command.Args {
				if arg == "--output-schema" && index+1 < len(command.Args) {
					schemaPath = command.Args[index+1]
				}
			}
			if _, err := os.Stat(schemaPath); err != nil {
				t.Fatalf("schema unavailable during execution: %v", err)
			}
			return []byte(result), nil
		}),
	}
	if _, err := analyzer.Analyze(context.Background(), analysisInput("prompt")); err != nil {
		t.Fatal(err)
	}
	if schemaPath == "" {
		t.Fatal("no schema path")
	}
	if _, err := os.Stat(schemaPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("schema still exists: %v", err)
	}
}

func TestCLIAnalyzerRejectsUnsupportedConfigurationAndMalformedOutput(t *testing.T) {
	for _, analyzer := range []CLIAnalyzer{
		{Config: func() settings.SynthesisSettings {
			return settings.SynthesisSettings{Enabled: true, Backend: settings.BackendCodex, Model: "user-controlled"}
		}},
		{
			Config: func() settings.SynthesisSettings {
				return settings.SynthesisSettings{Enabled: true, Backend: settings.BackendCodex, Model: "gpt-5.6-luna"}
			},
			WorkDir: t.TempDir(), Timeout: time.Second,
			Executor: analysisExecutorFunc(func(context.Context, AnalysisCommand, int64) ([]byte, error) {
				return []byte(`{"intention":""}`), nil
			}),
		},
	} {
		if _, err := analyzer.Analyze(context.Background(), analysisInput("prompt")); !errors.Is(err, ErrAnalysisFailed) {
			t.Fatalf("error = %v", err)
		}
	}
}

func TestAnalysisCacheIsBoundedUnderConcurrentAccess(t *testing.T) {
	cache := newAnalysisCache(8)
	var workers sync.WaitGroup
	for index := range 64 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			key := string(rune('a' + index))
			cache.put(key, TurnAnalysis{Intention: key})
			_, _ = cache.get(key)
		}()
	}
	workers.Wait()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.values) > cache.capacity || len(cache.order) > cache.capacity {
		t.Fatalf("cache grew beyond bound: values=%d order=%d", len(cache.values), len(cache.order))
	}
}
