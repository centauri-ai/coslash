package synthesis

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

type accountingRunner struct {
	vendor string
	model  string
	run    func(context.Context, string) (RunResult, error)
}

func (r *accountingRunner) Run(ctx context.Context, input string) (RunResult, error) {
	return r.run(ctx, input)
}
func (r *accountingRunner) VendorName() string { return r.vendor }
func (r *accountingRunner) ModelName() string  { return r.model }

func TestManagerRecordsRoundsInvocationsAndRetry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	store, err := OpenAccountingStore(home, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	calls := 0
	failing := false
	runner := &accountingRunner{vendor: "codex", model: "gpt-4o"}
	runner.run = func(_ context.Context, input string) (RunResult, error) {
		calls++
		cost := int64(7)
		result := RunResult{Synthesis: session.SessionSynthesis{Outcome: fmt.Sprintf("part-%d", calls)}, Usage: UsageReport{ReportedCostMicroUSD: &cost, Coverage: "complete"}}
		if failing {
			return result, errors.New("paid failure")
		}
		if strings.Contains(input, "PARTIAL SYNTHESES") {
			result.Synthesis.Outcome = "merged"
		}
		return result, nil
	}
	manager := NewManager(runner, store)
	manager.now = func() time.Time { return time.UnixMilli(1000) }
	s := overflowSession()
	s.ID = "accounted"
	s.Turns = 6
	sourceCalls := len(BuildInputs(s))
	if sourceCalls < 2 {
		t.Fatal("fixture did not chunk")
	}
	for index, revision := range []int64{100, 101} {
		if !manager.Ensure(s, revision) {
			t.Fatalf("revision %d not started", revision)
		}
		manager.workers.Wait()
		if manager.AccountingVersion() != fmt.Sprint(index+1) {
			t.Fatalf("revision %d version = %s", revision, manager.AccountingVersion())
		}
	}
	failing = true
	if !manager.Ensure(s, 102) {
		t.Fatal("failure round not started")
	}
	manager.workers.Wait()
	if manager.AccountingVersion() != "3" {
		t.Fatalf("paid failure version = %s", manager.AccountingVersion())
	}
	failing = false
	manager.failures.Delete(failureKey{agent: s.Agent, id: s.ID, revision: 102})
	if !manager.Ensure(s, 102) {
		t.Fatal("retry round not started")
	}
	manager.workers.Wait()
	if manager.AccountingVersion() != "4" {
		t.Fatalf("identical-summary retry version = %s", manager.AccountingVersion())
	}
	got, err := store.ReadCosts(context.Background(), CostQuery{SourceID: "local", Agent: s.Agent, SessionID: s.ID})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := int64(3*(sourceCalls+1) + 1)
	if got.Totals.RoundCount != 4 || got.Totals.InvocationCount != wantCalls || got.Totals.KnownCostMicroUSD == nil || *got.Totals.KnownCostMicroUSD != 7*wantCalls {
		t.Fatalf("totals = %+v, want 4 rounds %d calls %d microUSD", got.Totals, wantCalls, 7*wantCalls)
	}
	if len(got.Rounds) != 4 {
		t.Fatalf("rounds = %+v", got.Rounds)
	}
	revisions := map[int64]int{}
	failed := 0
	for _, round := range got.Rounds {
		revisions[round.SourceRevision]++
		if round.Outcome == "failed" {
			failed++
			if round.Totals.InvocationCount != 1 || round.Totals.KnownCostMicroUSD == nil || *round.Totals.KnownCostMicroUSD != 7 {
				t.Fatalf("failed paid round = %+v", round)
			}
		} else if round.Outcome != "success" || round.Totals.InvocationCount != int64(sourceCalls+1) || !reflect.DeepEqual(round.VendorModels, []VendorModel{{Vendor: "codex", Model: "gpt-4o"}}) {
			t.Fatalf("successful round = %+v", round)
		}
	}
	if failed != 1 || !reflect.DeepEqual(revisions, map[int64]int{100: 1, 101: 1, 102: 2}) {
		t.Fatalf("outcomes/revisions = %+v", got.Rounds)
	}
	if manager.AccountingVersion() != "4" {
		t.Fatalf("version = %q", manager.AccountingVersion())
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err := store.ReadCosts(context.Background(), CostQuery{SourceID: "local", Agent: s.Agent, SessionID: s.ID, Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, round := range page.Rounds {
			if len(round.ID) != 32 || seen[round.ID] {
				t.Fatalf("generated round ID = %q", round.ID)
			}
			if raw, err := hex.DecodeString(round.ID); err != nil || len(raw) != 16 {
				t.Fatalf("invalid generated ID %q: %v", round.ID, err)
			}
			seen[round.ID] = true
			rows, err := store.db.Query("SELECT ordinal,phase FROM invocations WHERE round_id=? ORDER BY ordinal", round.ID)
			if err != nil {
				t.Fatal(err)
			}
			ordinal := 0
			for rows.Next() {
				var gotOrdinal int
				var phase string
				if err := rows.Scan(&gotOrdinal, &phase); err != nil {
					t.Fatal(err)
				}
				wantPhase := "source"
				if round.Outcome == "success" && ordinal == sourceCalls {
					wantPhase = "merge"
				}
				if gotOrdinal != ordinal || phase != wantPhase {
					t.Fatalf("round %s invocation %d = %d %s", round.ID, ordinal, gotOrdinal, phase)
				}
				ordinal++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 4 {
		t.Fatalf("pagination returned %d generated IDs", len(seen))
	}
}

func TestManagerSnapshotsInputAndRunnerBeforeQueuedWork(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	store, err := OpenAccountingStore(home, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	started := make(chan string, 1)
	release := make(chan struct{})
	runner := &accountingRunner{vendor: "claude", model: "gpt-4o"}
	runner.run = func(_ context.Context, input string) (RunResult, error) {
		started <- input
		<-release
		return RunResult{Synthesis: session.SessionSynthesis{Outcome: "done"}, Usage: UsageReport{ReportedCostMicroUSD: int64Pointer(9), Coverage: "complete"}}, nil
	}
	manager := NewManager(runner, store)
	s := &session.Session{Agent: "claude", ID: "original", SessionDetails: session.SessionDetails{Turns: 6, Digest: []session.DigestEntry{{Turn: 1, Description: "original-digest"}}}}
	if !manager.Ensure(s, 42) {
		t.Fatal("not started")
	}
	s.ID = "changed"
	s.Digest[0].Description = "changed-digest"
	runner.vendor, runner.model = "cursor", "changed-model"
	manager.SetRunner(nil)
	input := <-started
	close(release)
	manager.workers.Wait()
	if !strings.Contains(input, "original-digest") || strings.Contains(input, "changed-digest") {
		t.Fatalf("input = %q", input)
	}
	got, err := store.ReadCosts(context.Background(), CostQuery{SourceID: "local", Agent: "claude", SessionID: "original"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Totals.RoundCount != 1 || len(got.Rounds) != 1 || !reflect.DeepEqual(got.Rounds[0].VendorModels, []VendorModel{{Vendor: "claude", Model: "gpt-4o"}}) {
		t.Fatalf("snapshot = %+v", got)
	}
}

func int64Pointer(n int64) *int64 { return &n }

func TestManagerShutdownKeepsPaidCancellationWithoutPublishingSummary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	store, err := OpenAccountingStore(home, 1)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	runner := &accountingRunner{vendor: "codex", model: "gpt-4o"}
	runner.run = func(ctx context.Context, _ string) (RunResult, error) {
		close(started)
		<-ctx.Done()
		return RunResult{Usage: UsageReport{ReportedCostMicroUSD: int64Pointer(11), Coverage: "complete"}}, ctx.Err()
	}
	manager := NewManager(runner, store)
	if !manager.Ensure(&session.Session{Agent: "codex", ID: "paid", SessionDetails: session.SessionDetails{Turns: 6}}, 42) {
		t.Fatal("not started")
	}
	<-started
	manager.Shutdown()
	got, err := store.ReadCosts(context.Background(), CostQuery{SourceID: "local", Agent: "codex", SessionID: "paid"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rounds) != 1 || got.Rounds[0].Outcome != "interrupted" || got.Totals.KnownCostMicroUSD == nil || *got.Totals.KnownCostMicroUSD != 11 || manager.AccountingVersion() != "1" || manager.Lookup("codex", "paid", 42) != nil {
		t.Fatalf("cancelled paid round = %+v, version=%s", got, manager.AccountingVersion())
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := OpenAccountingStore(home, 2000)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	after, err := recovered.ReadCosts(context.Background(), CostQuery{SourceID: "local", Agent: "codex", SessionID: "paid"})
	if err != nil {
		t.Fatal(err)
	}
	if after.Totals.KnownCostMicroUSD == nil || *after.Totals.KnownCostMicroUSD != 11 || after.Totals.RoundCount != 1 {
		t.Fatalf("recovered paid round = %+v", after)
	}
}

func TestManagerMalformedUsageDoesNotDropSummaryOrHideAccountingFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	store, err := OpenAccountingStore(home, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner := &accountingRunner{vendor: "codex", model: "gpt-4o", run: func(context.Context, string) (RunResult, error) {
		return RunResult{Synthesis: session.SessionSynthesis{Outcome: "valid"}, Usage: UsageReport{ReportedCostMicroUSD: int64Pointer(-1)}}, nil
	}}
	manager := NewManager(runner, store)
	if !manager.Ensure(&session.Session{Agent: "codex", ID: "malformed", SessionDetails: session.SessionDetails{Turns: 6}}, 42) {
		t.Fatal("not started")
	}
	manager.workers.Wait()
	if got := manager.Lookup("codex", "malformed", 42); got == nil || got.Outcome != "valid" {
		t.Fatalf("valid summary lost: %+v", got)
	}
	if !manager.AccountingUnavailable() || manager.AccountingVersion() != "1" {
		t.Fatalf("accounting state: unavailable=%t version=%s", manager.AccountingUnavailable(), manager.AccountingVersion())
	}
	got, err := store.ReadCosts(context.Background(), CostQuery{SourceID: "local", Agent: "codex", SessionID: "malformed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rounds) != 1 || got.Rounds[0].Outcome != "running" || got.Totals.UnknownInvocationCount != 1 {
		t.Fatalf("unresolved accounting = %+v", got)
	}
}

func TestManagerCacheFailureDoesNotFinalizeRoundAsSuccess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	if err := os.WriteFile(SummariesDir(), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenAccountingStore(home, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner := &accountingRunner{vendor: "codex", model: "gpt-4o", run: func(context.Context, string) (RunResult, error) {
		return RunResult{Synthesis: session.SessionSynthesis{Outcome: "valid"}, Usage: UsageReport{ReportedCostMicroUSD: int64Pointer(5), Coverage: "complete"}}, nil
	}}
	manager := NewManager(runner, store)
	if !manager.Ensure(&session.Session{Agent: "codex", ID: "cache-fail", SessionDetails: session.SessionDetails{Turns: 6}}, 42) {
		t.Fatal("not started")
	}
	manager.workers.Wait()
	got, err := store.ReadCosts(context.Background(), CostQuery{SourceID: "local", Agent: "codex", SessionID: "cache-fail"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rounds) != 1 || got.Rounds[0].Outcome != "failed" || *got.Totals.KnownCostMicroUSD != 5 || manager.Lookup("codex", "cache-fail", 42) != nil {
		t.Fatalf("unpublished result = %+v", got)
	}
}

func TestManagerQueuedCancellationDoesNotCreateRoundOrCallRunner(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	store, err := OpenAccountingStore(home, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	called := make(chan struct{}, 1)
	runner := &accountingRunner{vendor: "codex", model: "gpt-4o", run: func(context.Context, string) (RunResult, error) {
		called <- struct{}{}
		return RunResult{}, nil
	}}
	manager := NewManager(runner, store)
	for range cap(manager.slots) {
		manager.slots <- struct{}{}
	}
	if !manager.Ensure(&session.Session{Agent: "codex", ID: "queued", SessionDetails: session.SessionDetails{Turns: 6}}, 42) {
		t.Fatal("work not queued")
	}
	before, err := store.ReadCosts(context.Background(), CostQuery{SourceID: "local", Agent: "codex", SessionID: "queued"})
	if err != nil {
		t.Fatal(err)
	}
	if before.Totals.RoundCount != 0 {
		t.Fatalf("queued work created round: %+v", before)
	}
	manager.Shutdown()
	select {
	case <-called:
		t.Fatal("runner called for canceled queued work")
	default:
	}
	got, err := store.ReadCosts(context.Background(), CostQuery{SourceID: "local", Agent: "codex", SessionID: "queued"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Totals.RoundCount != 0 || manager.AccountingUnavailable() || manager.AccountingVersion() != "0" {
		t.Fatalf("queued cancellation = %+v, unavailable=%t version=%s", got, manager.AccountingUnavailable(), manager.AccountingVersion())
	}
}

func TestManagerCanceledAfterSlotDoesNotBeginAccounting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	store, err := OpenAccountingStore(home, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	called := false
	runner := &accountingRunner{vendor: "codex", model: "gpt-4o", run: func(context.Context, string) (RunResult, error) { called = true; return RunResult{}, nil }}
	manager := NewManager(runner, store)
	manager.cancelWork()
	manager.execute(&session.Session{Agent: "codex", ID: "canceled", SessionDetails: session.SessionDetails{Turns: 6}}, 42, runner, "codex", "gpt-4o", "codex", "canceled")
	got, err := store.ReadCosts(context.Background(), CostQuery{SourceID: "local", Agent: "codex", SessionID: "canceled"})
	if err != nil {
		t.Fatal(err)
	}
	if called || got.Totals.RoundCount != 0 || manager.AccountingUnavailable() || manager.AccountingVersion() != "0" {
		t.Fatalf("post-slot cancellation: called=%t costs=%+v unavailable=%t version=%s", called, got, manager.AccountingUnavailable(), manager.AccountingVersion())
	}
}

func TestManagerShutdownWaitsForSweep(t *testing.T) {
	manager := NewManager(&accountingRunner{vendor: "codex", model: "gpt-4o"}, nil)
	entered := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		manager.Run(context.Background(), func(ctx context.Context) ([]*session.Session, error) {
			close(entered)
			<-ctx.Done()
			close(exited)
			return nil, ctx.Err()
		})
	}()
	<-entered
	manager.Shutdown()
	select {
	case <-exited:
	default:
		t.Fatal("shutdown returned before sweep exited")
	}
}

func TestManagerRunSkipsListUntilRunnerConfigured(t *testing.T) {
	manager := NewManager(nil, nil)
	listCalls := 0
	list := func(context.Context) ([]*session.Session, error) {
		listCalls++
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	manager.Run(ctx, list)
	if listCalls != 0 {
		t.Fatalf("list called %d times without a runner", listCalls)
	}

	manager.SetRunner(runnerFunc(func(context.Context, string) (session.SessionSynthesis, error) {
		return session.SessionSynthesis{}, nil
	}))
	manager.sweep(context.Background(), list)
	if listCalls != 1 {
		t.Fatalf("list called %d times after configuring a runner", listCalls)
	}
}

func overflowSession() *session.Session {
	return overflowSessionWithTurns(30)
}

func overflowSessionWithTurns(turns int) *session.Session {
	digest := make([]session.DigestEntry, turns)
	for index := range digest {
		digest[index] = session.DigestEntry{
			Turn:        index + 1,
			Category:    session.DigestRecap,
			Description: fmt.Sprintf("history-%02d %s", index+1, strings.Repeat("x", 500)),
		}
	}
	return &session.Session{ID: "long", Agent: "codex", SessionDetails: session.SessionDetails{Digest: digest}}
}

func TestRunSynthesisMergesEveryOverflowChunk(t *testing.T) {
	var chunks []string
	runner := runnerFunc(func(_ context.Context, input string) (session.SessionSynthesis, error) {
		if strings.Contains(input, "PARTIAL SYNTHESES") {
			for _, chunk := range chunks {
				if !strings.Contains(input, chunk) {
					t.Fatalf("merge input omitted %q", chunk)
				}
			}
			return session.SessionSynthesis{Outcome: "merged history"}, nil
		}
		marker := fmt.Sprintf("chunk-%d", len(chunks)+1)
		chunks = append(chunks, marker)
		return session.SessionSynthesis{Outcome: marker}, nil
	})

	got, err := runSynthesis(context.Background(), runner, overflowSession())
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("runner saw %d source chunk, want overflow", len(chunks))
	}
	if got.Outcome != "merged history" {
		t.Fatalf("runSynthesis() = %#v", got)
	}
}

func TestRunSynthesisStopsOnChunkFailure(t *testing.T) {
	want := errors.New("chunk failed")
	calls := 0
	runner := runnerFunc(func(context.Context, string) (session.SessionSynthesis, error) {
		calls++
		if calls == 2 {
			return session.SessionSynthesis{}, want
		}
		return session.SessionSynthesis{Outcome: "partial"}, nil
	})

	_, err := runSynthesis(context.Background(), runner, overflowSession())
	if !errors.Is(err, want) {
		t.Fatalf("runSynthesis() error = %v, want %v", err, want)
	}
	if calls != 2 {
		t.Fatalf("runner called %d times after failure, want 2", calls)
	}
}

func TestRunSynthesisRejectsExcessiveWorkBeforeCallingRunner(t *testing.T) {
	digest := make([]session.DigestEntry, 400)
	for index := range digest {
		digest[index] = session.DigestEntry{
			Turn:        index + 1,
			Category:    session.DigestRecap,
			Description: strings.Repeat("界", 500),
		}
	}
	calls := 0
	runner := runnerFunc(func(context.Context, string) (session.SessionSynthesis, error) {
		calls++
		return session.SessionSynthesis{Outcome: "partial"}, nil
	})

	_, err := runSynthesis(context.Background(), runner, &session.Session{SessionDetails: session.SessionDetails{Digest: digest}})
	if err == nil || !strings.Contains(err.Error(), "work limit") {
		t.Fatalf("runSynthesis() error = %v, want work limit", err)
	}
	if calls != 0 {
		t.Fatalf("runner called %d times before rejecting excessive work", calls)
	}
}

func TestRunSynthesisKeepsBoundedContextWithinSourceRunLimit(t *testing.T) {
	goal := "goal-marker " + strings.Repeat("g", 988)
	repository := strings.Repeat("r", 300)
	branch := strings.Repeat("b", 300)
	digest := make([]session.DigestEntry, 100)
	for index := range digest {
		digest[index] = session.DigestEntry{
			Turn:        index + 1,
			Category:    session.DigestRecap,
			Description: fmt.Sprintf("history-%03d %s", index, strings.Repeat("d", 488)),
		}
	}
	edits := make([]session.FileEdit, 30)
	for index := range edits {
		edits[index].Path = strings.Repeat("artifact", 63)
	}
	s := &session.Session{
		ID:               strings.Repeat("i", 200),
		Agent:            strings.Repeat("a", 100),
		Repository:       &repository,
		Branch:           &branch,
		WorkingDirectory: strings.Repeat("w", 500),
		SessionDetails: session.SessionDetails{
			DeclaredGoal:   &goal,
			FirstPrompt:    &goal,
			CompactionSeed: "seed-marker " + strings.Repeat("s", 3_988),
			Digest:         digest,
			FileEdits:      edits,
		},
	}
	calls := 0
	var inputs []string
	runner := runnerFunc(func(_ context.Context, input string) (session.SessionSynthesis, error) {
		calls++
		inputs = append(inputs, input)
		return session.SessionSynthesis{Outcome: "partial"}, nil
	})

	if _, err := runSynthesis(context.Background(), runner, s); err != nil {
		t.Fatalf("runSynthesis() error = %v, want bounded synthesis", err)
	}
	if calls == 0 {
		t.Fatal("runner was not called")
	}
	joined := strings.Join(inputs, "\n")
	for _, marker := range []string{"goal-marker", "seed-marker", "history-000", "history-099"} {
		if !strings.Contains(joined, marker) {
			t.Fatalf("synthesis inputs omitted %q", marker)
		}
	}
}

func TestRunSynthesisRejectsImpossibleDigestBeforeBuildingPrompts(t *testing.T) {
	calls := 0
	runner := runnerFunc(func(context.Context, string) (session.SessionSynthesis, error) {
		calls++
		return session.SessionSynthesis{}, nil
	})

	_, err := runSynthesis(context.Background(), runner, &session.Session{SessionDetails: session.SessionDetails{
		Digest: make([]session.DigestEntry, 27_429),
	}})
	if err == nil || !strings.Contains(err.Error(), "digest item limit") {
		t.Fatalf("runSynthesis() error = %v, want digest item limit", err)
	}
	if calls != 0 {
		t.Fatalf("runner called %d times before rejecting impossible digest", calls)
	}
}

func TestRunSynthesisCarriesEarlyAndLateDecisionsAcrossMergeRounds(t *testing.T) {
	s := overflowSessionWithTurns(120)
	sourceCount := len(BuildInputs(s))
	if sourceCount < 3 {
		t.Fatalf("test session produced %d source chunks, want at least 3", sourceCount)
	}
	sourceCalls := 0
	mergeCalls := 0
	sawCombinedMarkers := false
	runner := runnerFunc(func(_ context.Context, input string) (session.SessionSynthesis, error) {
		if !strings.Contains(input, "PARTIAL SYNTHESES") {
			index := sourceCalls
			sourceCalls++
			decisions := make([]string, maxIntermediateKeyDecisions)
			for decisionIndex := range decisions {
				decisions[decisionIndex] = fmt.Sprintf("source-%02d-%02d", index, decisionIndex)
			}
			if index == 0 {
				decisions[0] = "early-decision-marker"
			}
			if index == sourceCount-1 {
				decisions[len(decisions)-1] = "late-decision-marker"
			}
			return session.SessionSynthesis{Outcome: strings.Repeat("o", 2_000), KeyDecisions: decisions}, nil
		}

		mergeCalls++
		var decisions []string
		for _, line := range strings.Split(input, "\n") {
			if decision, ok := strings.CutPrefix(line, "Decision: "); ok {
				decisions = append(decisions, decision)
			}
		}
		if strings.Contains(input, "early-decision-marker") && strings.Contains(input, "late-decision-marker") {
			sawCombinedMarkers = true
		}
		ranked := make([]string, 0, len(decisions))
		for _, marker := range []string{"early-decision-marker", "late-decision-marker"} {
			if slices.Contains(decisions, marker) {
				ranked = append(ranked, marker)
			}
		}
		for _, decision := range decisions {
			if decision != "early-decision-marker" && decision != "late-decision-marker" {
				ranked = append(ranked, decision)
			}
		}
		decisions = ranked[:min(len(ranked), maxIntermediateKeyDecisions)]
		return session.SessionSynthesis{Outcome: strings.Repeat("m", 2_000), KeyDecisions: decisions}, nil
	})

	got, err := runSynthesis(context.Background(), runner, s)
	if err != nil {
		t.Fatal(err)
	}
	if mergeCalls < 3 || !sawCombinedMarkers {
		t.Fatalf("merge calls = %d, combined markers = %v; want recursive merge carrying both", mergeCalls, sawCombinedMarkers)
	}
	if len(got.KeyDecisions) != maxFinalKeyDecisions ||
		!slices.Contains(got.KeyDecisions, "early-decision-marker") ||
		!slices.Contains(got.KeyDecisions, "late-decision-marker") {
		t.Fatalf("final key decisions = %#v, want 8 including early and late markers", got.KeyDecisions)
	}
}

func TestSweepUsesListRevisionForComposedNonPiSession(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	value := &session.Session{Agent: "claude", ID: "composed", LastActivityTime: 200, SynthesisRevision: 100, SessionDetails: session.SessionDetails{Turns: 6}}
	release := make(chan struct{})
	defer close(release)
	manager := NewManager(runnerFunc(func(context.Context, string) (session.SessionSynthesis, error) {
		<-release
		return session.SessionSynthesis{}, errors.New("unexpected resynthesis")
	}))
	manager.now = func() time.Time { return time.UnixMilli(200) }
	if err := manager.cache.Store(value.Agent, value.ID, Record{Revision: value.LastActivityTime, Synthesis: session.SessionSynthesis{Outcome: "ready"}}); err != nil {
		t.Fatal(err)
	}
	manager.sweep(func() ([]*session.Session, error) { return []*session.Session{value}, nil })
	if _, pending := manager.inFlight.Load(cacheKey{agent: value.Agent, id: value.ID}); pending {
		t.Fatal("sweep missed the completed synthesis stored under the list revision")
	}
	if got := manager.Lookup(value.Agent, value.ID, value.LastActivityTime); got == nil || got.Outcome != "ready" {
		t.Fatalf("list synthesis = %#v", got)
	}
}
