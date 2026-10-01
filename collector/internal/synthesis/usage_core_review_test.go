package synthesis

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func TestClaudeComponentCostsSurviveBadOrMissingSiblings(t *testing.T) {
	for _, tc := range []struct {
		name     string
		report   func() UsageReport
		want     int64
		coverage string
	}{
		{"claude malformed tokens", func() UsageReport {
			return parseClaudeUsage([]byte(`{"modelUsage":{"claude-sonnet-4-5":{"inputTokens":"bad","costUSD":0.25}}}`), "auto")
		}, 250000, "complete"},
		{"claude missing cost", func() UsageReport {
			return parseClaudeUsage([]byte(`{"modelUsage":{"claude-sonnet-4-5":{"costUSD":0.01},"claude-haiku-4-5":{}}}`), "auto")
		}, 10000, "partial"},
		{"claude negative cost", func() UsageReport {
			return parseClaudeUsage([]byte(`{"modelUsage":{"claude-sonnet-4-5":{"costUSD":-0.25},"claude-haiku-4-5":{"costUSD":0.50}}}`), "auto")
		}, 500000, "partial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.report()
			if got.ReportedCostMicroUSD == nil || *got.ReportedCostMicroUSD != tc.want || got.Coverage != tc.coverage {
				t.Fatalf("usage = %#v, want cost %d and coverage %s", got, tc.want, tc.coverage)
			}
		})
	}
}

func TestCursorAutoOrMissingModelRetainsUnpricedCounts(t *testing.T) {
	const unknownModel = "cursor/unknown-model"
	const result = `{"type":"result","is_error":false,"result":"broken","usage":{"inputTokens":7,"outputTokens":2,"cacheReadTokens":900,"cacheWriteTokens":100}}` + "\n"
	for _, init := range []string{"", `{"type":"system","subtype":"init","model":"auto"}` + "\n"} {
		runner := &CLIRunner{Backend: settings.BackendCursor, Model: "gpt-5", Timeout: time.Second}
		runner.exec = func(context.Context, commandSpec) ([]byte, error) { return []byte(init + result), nil }
		got, err := runner.Run(context.Background(), "facts")
		used := got.Usage.Tokens[unknownModel]
		if err == nil || used.InputTokens != 7 || used.OutputTokens != 2 || used.CacheReadInputTokens != 900 || used.CacheCreationInputTokens != 100 || got.Usage.Coverage != "unknown" || got.Usage.EstimatedCostMicroUSD != nil || len(got.Usage.UnpricedModels) != 1 || got.Usage.UnpricedModels[0] != unknownModel {
			t.Fatalf("init %q: usage = %#v, err = %v", init, got.Usage, err)
		}
	}
}

func TestCodexCursorScannerBoundsRetainCompletedUsage(t *testing.T) {
	const codex = `{"type":"item.completed","item":{"type":"agent_message","text":"{\"goals\":[\"ship\"],\"outcome\":\"done\",\"keyDecisions\":[],\"nextStep\":\"review\"}"}}` + "\n" +
		`{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":2,"output_tokens":3}}` + "\n"
	const cursor = `{"type":"system","subtype":"init","model":"gpt-5"}` + "\n" +
		`{"type":"result","is_error":false,"result":"{\"goals\":[\"ship\"],\"outcome\":\"done\",\"keyDecisions\":[],\"nextStep\":\"review\"}","usage":{"inputTokens":7,"outputTokens":2,"cacheReadTokens":9,"cacheWriteTokens":1}}` + "\n"
	for _, tc := range []struct {
		name, backend, output, model string
		input, cost                  int64
	}{
		{"codex", settings.BackendCodex, codex, "gpt-5", 8, 0},
		{"cursor", settings.BackendCursor, cursor, "gpt-5", 7, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &CLIRunner{Backend: tc.backend, Model: "gpt-5", Timeout: time.Second}
			runner.exec = func(context.Context, commandSpec) ([]byte, error) {
				return []byte(tc.output + strings.Repeat("x", 4<<20) + "\n"), nil
			}
			got, err := runner.Run(context.Background(), "facts")
			if err == nil || got.Usage.Coverage != "partial" || int64(got.Usage.Tokens[tc.model].InputTokens) != tc.input {
				t.Fatalf("usage = %#v, err = %v", got.Usage, err)
			}
			if tc.cost != 0 && (got.Usage.ReportedCostMicroUSD == nil || *got.Usage.ReportedCostMicroUSD != tc.cost) {
				t.Fatalf("reported cost = %#v", got.Usage)
			}
		})
	}
}
