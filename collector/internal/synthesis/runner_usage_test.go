package synthesis

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func TestClaudeUsageSurvivesFailedSummaryAndExit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		output  string
		execErr error
	}{
		{"invalid summary", `{"type":"result","result":"broken","usage":{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":80,"cache_creation_input_tokens":9,"cache_creation":{"ephemeral_1h_input_tokens":4,"ephemeral_5m_input_tokens":5}},"modelUsage":{"claude-sonnet-4-5":{"inputTokens":10,"outputTokens":2,"cacheReadInputTokens":80,"cacheCreationInputTokens":9,"costUSD":0.0123}},"total_cost_usd":0.0123}`, nil},
		{"nonzero exit", `{"type":"result","result":"broken","modelUsage":{"claude-sonnet-4-5":{"inputTokens":10,"outputTokens":0,"cacheReadInputTokens":0,"cacheCreationInputTokens":0}},"total_cost_usd":0}`, &exec.ExitError{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &CLIRunner{Backend: settings.BackendClaude, Model: "claude-sonnet-4-5", Timeout: time.Second}
			runner.exec = func(context.Context, commandSpec) ([]byte, error) { return []byte(tc.output), tc.execErr }
			got, err := runner.Run(context.Background(), "facts")
			if err == nil {
				t.Fatal("expected synthesis error")
			}
			used := got.Usage.Tokens["claude-sonnet-4-5"]
			if used.InputTokens != 10 {
				t.Fatalf("model usage lost: %#v", got.Usage)
			}
			if tc.execErr == nil {
				if used.CacheCreationInputTokens != 5 || used.CacheCreation1hInputTokens != 4 || used.CacheReadInputTokens != 80 {
					t.Fatalf("cache tiers overlap: %#v", used)
				}
				if got.Usage.ReportedCostMicroUSD == nil || *got.Usage.ReportedCostMicroUSD != 12300 {
					t.Fatalf("reported cost = %#v", got.Usage.ReportedCostMicroUSD)
				}
			} else if got.Usage.ReportedCostMicroUSD == nil || *got.Usage.ReportedCostMicroUSD != 0 {
				t.Fatalf("explicit zero cost = %#v", got.Usage.ReportedCostMicroUSD)
			}
		})
	}
}

func TestClaudeMalformedAccountingDoesNotDiscardSummary(t *testing.T) {
	runner := &CLIRunner{Backend: settings.BackendClaude, Model: "claude-sonnet-4-5", Timeout: time.Second}
	runner.exec = func(context.Context, commandSpec) ([]byte, error) {
		return []byte(`{"type":"result","result":"{\"goals\":[\"ship\"],\"outcome\":\"done\",\"keyDecisions\":[],\"nextStep\":\"review\"}","usage":{"input_tokens":-1}}`), nil
	}
	got, err := runner.Run(context.Background(), "facts")
	if err != nil || got.Synthesis.Outcome != "done" || got.Usage.Coverage != "unknown" {
		t.Fatalf("Run = %#v, %v", got, err)
	}
}

func TestClaudeReportedCostSurvivesMissingOrInvalidCounters(t *testing.T) {
	for _, usage := range []string{`{"input_tokens":-1}`, `{}`, `{"input_tokens":"bad"}`} {
		runner := &CLIRunner{Backend: settings.BackendClaude, Model: "claude-sonnet-4-5", Timeout: time.Second}
		runner.exec = func(context.Context, commandSpec) ([]byte, error) {
			return []byte(`{"type":"result","result":"broken","usage":` + usage + `,"total_cost_usd":0.25}`), nil
		}
		got, err := runner.Run(context.Background(), "facts")
		if err == nil || got.Usage.ReportedCostMicroUSD == nil || *got.Usage.ReportedCostMicroUSD != 250000 || got.Usage.Tokens != nil {
			t.Fatalf("usage %s: Run = %#v, %v", usage, got, err)
		}
	}
}

func TestClaudeModelCostsAreNotAddedToRunTotal(t *testing.T) {
	runner := &CLIRunner{Backend: settings.BackendClaude, Model: "claude-sonnet-4-5", Timeout: time.Second}
	runner.exec = func(context.Context, commandSpec) ([]byte, error) {
		return []byte(`{"type":"result","result":"broken","modelUsage":{"claude-sonnet-4-5":{"inputTokens":1,"outputTokens":1,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":0.02}},"total_cost_usd":0.03}`), nil
	}
	got, _ := runner.Run(context.Background(), "facts")
	if got.Usage.ReportedCostMicroUSD == nil || *got.Usage.ReportedCostMicroUSD != 30000 {
		t.Fatalf("reported cost = %#v", got.Usage)
	}
}

func TestClaudeUsesPerModelCostWhenRunTotalMissing(t *testing.T) {
	runner := &CLIRunner{Backend: settings.BackendClaude, Model: "claude-sonnet-4-5", Timeout: time.Second}
	runner.exec = func(context.Context, commandSpec) ([]byte, error) {
		return []byte(`{"type":"result","result":"broken","modelUsage":{"claude-sonnet-4-5":{"inputTokens":1,"outputTokens":1,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":0.02}}}`), nil
	}
	got, _ := runner.Run(context.Background(), "facts")
	if got.Usage.ReportedCostMicroUSD == nil || *got.Usage.ReportedCostMicroUSD != 20000 {
		t.Fatalf("reported cost = %#v", got.Usage)
	}
}

func TestClaudeUsageSurvivesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := &CLIRunner{Backend: settings.BackendClaude, Model: "claude-sonnet-4-5", Timeout: time.Second}
	runner.exec = func(context.Context, commandSpec) ([]byte, error) {
		cancel()
		return []byte(`{"type":"result","result":"broken","total_cost_usd":0.01}`), context.Canceled
	}
	got, err := runner.Run(ctx, "facts")
	if !errors.Is(err, context.Canceled) || got.Usage.ReportedCostMicroUSD == nil || *got.Usage.ReportedCostMicroUSD != 10000 {
		t.Fatalf("Run = %#v, %v", got, err)
	}
}

func TestCodexCompletedEventsKeepUsageOnInvalidSummary(t *testing.T) {
	runner := &CLIRunner{Backend: settings.BackendCodex, Model: "gpt-5", Timeout: time.Second}
	var args []string
	runner.exec = func(_ context.Context, spec commandSpec) ([]byte, error) {
		args = spec.args
		return []byte("{\"type\":\"item.completed\",\"item\":{\"id\":\"message\",\"type\":\"agent_message\",\"text\":\"invalid summary\"}}\n" +
			"{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":100,\"cached_input_tokens\":70,\"cache_write_input_tokens\":10,\"output_tokens\":20,\"reasoning_output_tokens\":5}}\n"), nil
	}
	got, err := runner.Run(context.Background(), "facts")
	if err == nil || !slices.Contains(args, "--json") || !slices.Contains(args, "--ephemeral") {
		t.Fatalf("Run = %#v, %v, args %v", got, err, args)
	}
	used := got.Usage.Tokens["gpt-5"]
	if used.InputTokens != 20 || used.CacheReadInputTokens != 70 || used.CacheCreationInputTokens != 10 || used.OutputTokens != 20 {
		t.Fatalf("Codex usage = %#v", used)
	}
}

func TestCodexCompletedMessageParsesSynthesis(t *testing.T) {
	runner := &CLIRunner{Backend: settings.BackendCodex, Model: "gpt-5", Timeout: time.Second}
	runner.exec = func(context.Context, commandSpec) ([]byte, error) {
		return []byte("{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"{\\\"goals\\\":[\\\"ship\\\"],\\\"outcome\\\":\\\"done\\\",\\\"keyDecisions\\\":[],\\\"nextStep\\\":\\\"review\\\"}\"}}\n" +
			"{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":0,\"cached_input_tokens\":0,\"output_tokens\":0}}\n"), nil
	}
	got, err := runner.Run(context.Background(), "facts")
	if err != nil || got.Synthesis.Outcome != "done" || got.Usage.Coverage != "complete" {
		t.Fatalf("Run = %#v, %v", got, err)
	}
}

func TestCodexMissingUsageCountersRemainUnknown(t *testing.T) {
	runner := &CLIRunner{Backend: settings.BackendCodex, Model: "gpt-5", Timeout: time.Second}
	runner.exec = func(context.Context, commandSpec) ([]byte, error) {
		return []byte(`{"type":"turn.completed","usage":{"input_tokens":0,"cached_input_tokens":0}}` + "\n"), &exec.ExitError{}
	}
	got, err := runner.Run(context.Background(), "facts")
	if err == nil || got.Usage.Coverage != "unknown" || got.Usage.Tokens != nil {
		t.Fatalf("Run = %#v, %v", got, err)
	}
}

func TestCodexCompletedSnapshotsAreCumulative(t *testing.T) {
	runner := &CLIRunner{Backend: settings.BackendCodex, Model: "gpt-5", Timeout: time.Second}
	runner.exec = func(context.Context, commandSpec) ([]byte, error) {
		return []byte(`{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":2,"output_tokens":3,"reasoning_output_tokens":1}}` + "\n" +
			`{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":2,"output_tokens":3,"reasoning_output_tokens":1}}` + "\n"), &exec.ExitError{}
	}
	got, err := runner.Run(context.Background(), "facts")
	if err == nil || got.Usage.Tokens["gpt-5"].InputTokens != 8 || got.Usage.Tokens["gpt-5"].OutputTokens != 3 {
		t.Fatalf("cumulative usage = %#v, %v", got.Usage, err)
	}
}

func TestCodexMalformedLatestSnapshotIsUnknown(t *testing.T) {
	runner := &CLIRunner{Backend: settings.BackendCodex, Model: "gpt-5", Timeout: time.Second}
	runner.exec = func(context.Context, commandSpec) ([]byte, error) {
		return []byte(`{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":2,"output_tokens":3}}` + "\n" +
			`{"type":"turn.completed","usage":{"input_tokens":"bad","cached_input_tokens":2,"output_tokens":3}}` + "\n"), &exec.ExitError{}
	}
	got, _ := runner.Run(context.Background(), "facts")
	if got.Usage.Coverage != "unknown" || got.Usage.Tokens != nil {
		t.Fatalf("usage = %#v", got.Usage)
	}
}

func TestCodexOutputLimitKeepsCompletedUsagePartial(t *testing.T) {
	runner := &CLIRunner{Backend: settings.BackendCodex, Model: "gpt-5", Timeout: time.Second}
	runner.exec = func(context.Context, commandSpec) ([]byte, error) {
		return []byte(`{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":2,"output_tokens":3}}` + "\n"), errSynthesisOutputLimit
	}
	got, err := runner.Run(context.Background(), "facts")
	if !errors.Is(err, errSynthesisOutputLimit) || got.Usage.Tokens["gpt-5"].InputTokens != 8 || got.Usage.Coverage != "partial" {
		t.Fatalf("Run = %#v, %v", got, err)
	}
}
