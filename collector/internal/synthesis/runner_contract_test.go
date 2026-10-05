package synthesis

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func TestRunnerVendorNamesFitAccounting(t *testing.T) {
	for _, tc := range []struct{ backend, vendor string }{
		{settings.BackendClaude, "claude"},
		{settings.BackendCodex, "codex"},
		{settings.BackendOpenCode, "opencode"},
		{settings.BackendCursor, "cursor"},
		{settings.BackendGrok, "grok"},
	} {
		runner := &CLIRunner{Backend: tc.backend, Model: "gpt-5"}
		if got := runner.VendorName(); got != tc.vendor {
			t.Fatalf("%s vendor = %q, want %q", tc.backend, got, tc.vendor)
		}
		store, err := OpenAccountingStore(t.TempDir(), 100)
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if err := store.BeginRound(ctx, Round{ID: "r", SourceID: "local", Agent: "claude", SessionID: "s", SourceRevision: 1, StartedAtMs: 100}); err != nil {
			t.Fatal(err)
		}
		if err := store.StartInvocation(ctx, "r", 0, "source", runner.VendorName(), runner.ModelName(), 101); err != nil {
			t.Fatal(err)
		}
		store.Close()
	}
}

func TestFailedRunnerPartialUsageFitsAccounting(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	runner := &CLIRunner{Backend: settings.BackendOpenCode, Model: settings.OpenCodeDefaultModel, Timeout: time.Second}
	runner.exec = func(context.Context, commandSpec) ([]byte, error) {
		return []byte(`{"type":"step_finish","sessionID":"s","part":{"id":"p","cost":0.01}}`), &exec.ExitError{}
	}
	result, err := runner.Run(context.Background(), "facts")
	if err == nil || result.Usage.Coverage != "partial" {
		t.Fatalf("runner usage = %#v, %v", result.Usage, err)
	}
	store, err := OpenAccountingStore(t.TempDir(), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.BeginRound(ctx, Round{ID: "r", SourceID: "local", Agent: "claude", SessionID: "s", SourceRevision: 1, StartedAtMs: 100}); err != nil {
		t.Fatal(err)
	}
	if err := store.StartInvocation(ctx, "r", 0, "source", runner.VendorName(), runner.ModelName(), 101); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishInvocation(ctx, "r", 0, 102, "failed", result.Usage); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishInvocation(ctx, "r", 0, 102, "failed", result.Usage); err != nil {
		t.Fatalf("matching replay: %v", err)
	}
	if err := store.FinishInvocation(ctx, "r", 0, 102, "failed", UsageReport{Coverage: "partial", ReportedCostMicroUSD: new(int64)}); err == nil {
		t.Fatal("conflicting completion accepted")
	}
	if err := store.StartInvocation(ctx, "r", 1, "source", "opencode", "auto", 101); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishInvocation(ctx, "r", 1, 102, "failed", UsageReport{Coverage: "partial"}); err == nil {
		t.Fatal("empty partial report accepted")
	}
	if err := store.FinishInvocation(ctx, "r", 1, 102, "failed", UsageReport{Coverage: "partial", Tokens: map[string]session.ModelTokens{"gpt-5": {InputTokens: -1}}}); err == nil {
		t.Fatal("negative tokens accepted")
	}
}
