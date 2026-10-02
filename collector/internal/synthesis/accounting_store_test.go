package synthesis

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestAccountingPricingCoverage(t *testing.T) {
	known := map[string]session.ModelTokens{"gpt-4o": {InputTokens: 1000}}
	unknown := map[string]session.ModelTokens{"not-a-real-model": {InputTokens: 1000}}
	zero := int64(0)
	for _, tc := range []struct {
		name      string
		tokens    map[string]session.ModelTokens
		reported  *int64
		coverage  string
		estimated *int64
		unpriced  []string
	}{
		{"known", known, nil, "complete", micro(2500), nil},
		{"partial", map[string]session.ModelTokens{"gpt-4o": {InputTokens: 1000}, "not-a-real-model": {InputTokens: 1000}}, nil, "partial", micro(2500), []string{"not-a-real-model"}},
		{"missing", nil, nil, "unknown", nil, nil},
		{"reported zero", nil, &zero, "complete", nil, nil},
		{"unknown model", unknown, nil, "unknown", nil, []string{"not-a-real-model"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PriceUsage(tc.tokens, tc.reported)
			if err != nil {
				t.Fatal(err)
			}
			if got.Coverage != tc.coverage || !reflect.DeepEqual(got.EstimatedCostMicroUSD, tc.estimated) || !reflect.DeepEqual(got.UnpricedModels, tc.unpriced) {
				t.Fatalf("got %+v", got)
			}
		})
	}
	if _, err := PriceUsage(known, micro(-1)); err == nil {
		t.Fatal("negative cost accepted")
	}
	if _, err := PriceUsage(known, micro(maxSafeInteger+1)); err == nil {
		t.Fatal("unsafe positive cost accepted")
	}
	if _, err := PriceUsage(map[string]session.ModelTokens{"gpt-4o": {Cost: math.NaN()}}, nil); err == nil {
		t.Fatal("nonfinite token cost accepted")
	}
	many := map[string]session.ModelTokens{}
	for i := 0; i < 65; i++ {
		many[fmt.Sprint(i)] = session.ModelTokens{}
	}
	if _, err := PriceUsage(many, nil); err == nil {
		t.Fatal("65 model buckets accepted")
	}
	if _, err := PriceUsage(map[string]session.ModelTokens{"gpt-4o": {InputTokens: math.MaxInt}}, nil); err == nil {
		t.Fatal("unsafe estimate accepted")
	}
	if _, err := PriceUsage(map[string]session.ModelTokens{"gpt-4o": {InputTokens: -1}}, nil); err == nil {
		t.Fatal("negative tokens accepted")
	}
}

func TestAccountingSnapshotAcrossReadStages(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	reader, err := OpenAccountingStore(home, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	reader.db.SetMaxOpenConns(2)
	if _, err := reader.db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	if err := reader.BeginRound(ctx, Round{ID: "r", SourceID: "local", Agent: "claude", SessionID: "s", SourceRevision: 1, StartedAtMs: 110}); err != nil {
		t.Fatal(err)
	}
	if err := reader.StartInvocation(ctx, "r", 0, "source", "codex", "gpt-4o", 111); err != nil {
		t.Fatal(err)
	}
	tx, err := reader.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var started int64
	if err := tx.QueryRowContext(ctx, "SELECT tracking_started_at_ms FROM metadata").Scan(&started); err != nil {
		t.Fatal(err)
	}
	if err := reader.FinishInvocation(ctx, "r", 0, 112, "success", UsageReport{ReportedCostMicroUSD: micro(17)}); err != nil {
		t.Fatal(err)
	}
	before, err := reader.readCosts(ctx, tx, CostQuery{SourceID: "local", Agent: "claude", SessionID: "s"}, roundCursor{})
	if err != nil {
		t.Fatal(err)
	}
	if before.Totals.KnownCostMicroUSD != nil || before.Rounds[0].Totals.KnownCostMicroUSD != nil {
		t.Fatalf("mixed snapshot: %+v", before)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	after, err := reader.ReadCosts(ctx, CostQuery{SourceID: "local", Agent: "claude", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if *after.Totals.KnownCostMicroUSD != 17 || *after.Rounds[0].Totals.KnownCostMicroUSD != 17 {
		t.Fatalf("committed snapshot: %+v", after)
	}
}

func TestAccountingMonthUsesInvocationRangeIndex(t *testing.T) {
	store, err := OpenAccountingStore(t.TempDir(), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rows, err := store.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+monthlyCostsSQL, 100, 200)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "invocations_started") && strings.Contains(detail, "started_at_ms>?") {
			found = true
			t.Log(detail)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("month query did not use invocation start range index")
	}
}

func TestAccountingRunningCoverageUsesSelectedInvocations(t *testing.T) {
	ctx := context.Background()
	store, err := OpenAccountingStore(t.TempDir(), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.BeginRound(ctx, Round{ID: "running", SourceID: "local", Agent: "claude", SessionID: "s", SourceRevision: 1, StartedAtMs: 110}); err != nil {
		t.Fatal(err)
	}
	for ordinal, at := range []int64{199, 201} {
		if err := store.StartInvocation(ctx, "running", ordinal, "source", "codex", "gpt-4o", at); err != nil {
			t.Fatal(err)
		}
		if err := store.FinishInvocation(ctx, "running", ordinal, at+1, "success", UsageReport{ReportedCostMicroUSD: micro(17)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.StartInvocation(ctx, "running", 2, "source", "codex", "auto", 301); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginRound(ctx, Round{ID: "failed", SourceID: "local", Agent: "claude", SessionID: "s", SourceRevision: 1, StartedAtMs: 200}); err != nil {
		t.Fatal(err)
	}
	if err := store.StartInvocation(ctx, "failed", 0, "source", "cursor", "gpt-4o", 202); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishInvocation(ctx, "failed", 0, 203, "failed", UsageReport{ReportedCostMicroUSD: micro(5)}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRound(ctx, "failed", 204, "failed"); err != nil {
		t.Fatal(err)
	}
	month, err := store.ReadCosts(ctx, CostQuery{SourceID: "local", SinceMs: micro(200), UntilMs: micro(300)})
	if err != nil {
		t.Fatal(err)
	}
	if month.Totals.InvocationCount != 2 || *month.Totals.KnownCostMicroUSD != 22 || month.Totals.IncompleteRoundCount != 1 || month.ByVendor[0].Totals.IncompleteRoundCount != 1 || month.ByVendor[1].Totals.IncompleteRoundCount != 0 {
		t.Fatalf("month: %+v", month)
	}
	inspector, err := store.ReadCosts(ctx, CostQuery{SourceID: "local", Agent: "claude", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if inspector.Totals.IncompleteRoundCount != 1 || inspector.ByVendor[0].Totals.IncompleteRoundCount != 1 || inspector.Rounds[0].Totals.IncompleteRoundCount != 0 || inspector.Rounds[1].Totals.IncompleteRoundCount != 1 {
		t.Fatalf("inspector: %+v", inspector)
	}
}

func TestAccountingRejectsCursorAndCorruptUsageBeforeCostlyReads(t *testing.T) {
	ctx := context.Background()
	store, err := OpenAccountingStore(t.TempDir(), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.db.ExecContext(ctx, "DELETE FROM metadata"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadCosts(ctx, CostQuery{SourceID: "local", Agent: "claude", SessionID: "s", Cursor: strings.Repeat("a", 1<<20)}); err == nil || !strings.Contains(err.Error(), "cursor") {
		t.Fatalf("cursor error: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, "INSERT INTO metadata VALUES(1,100)"); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginRound(ctx, Round{ID: "r", SourceID: "local", Agent: "claude", SessionID: "s", SourceRevision: 1, StartedAtMs: 110}); err != nil {
		t.Fatal(err)
	}
	if err := store.StartInvocation(ctx, "r", 0, "source", "codex", "gpt-4o", 111); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE invocations SET usage_json=zeroblob(?)", (256<<10)+1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadCosts(ctx, CostQuery{SourceID: "local", Agent: "claude", SessionID: "s"}); err == nil {
		t.Fatal("oversized stored blob accepted")
	}
	data, _ := json.Marshal(map[string]session.ModelTokens{strings.Repeat("x", 513): {InputTokens: 1}})
	if _, err := store.db.ExecContext(ctx, "UPDATE invocations SET usage_json=?", data); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadCosts(ctx, CostQuery{SourceID: "local", Agent: "claude", SessionID: "s"}); err == nil {
		t.Fatal("corrupt model name accepted")
	}
}

func micro(n int64) *int64 { return &n }

func TestAccountingLiveOwnerKeepsInvocation(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	owner, err := OpenAccountingStore(home, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.BeginRound(ctx, Round{ID: "live", SourceID: "local", Agent: "claude", SessionID: "s", SourceRevision: 1, StartedAtMs: 110}); err != nil {
		t.Fatal(err)
	}
	if err := owner.StartInvocation(ctx, "live", 0, "source", "codex", "gpt-4o", 111); err != nil {
		t.Fatal(err)
	}
	other, err := OpenAccountingStore(home, 200)
	if err == nil {
		other.Close()
		t.Fatal("second live store opened and recovered active work")
	}
	if err := owner.FinishInvocation(ctx, "live", 0, 120, "success", UsageReport{ReportedCostMicroUSD: micro(17)}); err != nil {
		t.Fatalf("live usage lost: %v", err)
	}
	if err := owner.FinishRound(ctx, "live", 120, "success"); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenAccountingStore(home, 300)
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	defer reopened.Close()
	got, err := reopened.ReadCosts(ctx, CostQuery{SourceID: "local", Agent: "claude", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Rounds[0].Outcome != "success" || *got.Totals.KnownCostMicroUSD != 17 || got.Totals.UnknownInvocationCount != 0 {
		t.Fatalf("live completion: %+v", got)
	}
}

func TestAccountingCrashChild(t *testing.T) {
	if os.Getenv("COSLASH_ACCOUNTING_CRASH_CHILD") != "1" {
		return
	}
	ctx := context.Background()
	store, err := OpenAccountingStore(os.Getenv("COSLASH_ACCOUNTING_TEST_HOME"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginRound(ctx, Round{ID: "crash", SourceID: "local", Agent: "claude", SessionID: "s", SourceRevision: 1, StartedAtMs: 110}); err != nil {
		t.Fatal(err)
	}
	if err := store.StartInvocation(ctx, "crash", 0, "source", "codex", "gpt-4o", 111); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishInvocation(ctx, "crash", 0, 120, "success", UsageReport{ReportedCostMicroUSD: micro(17)}); err != nil {
		t.Fatal(err)
	}
	if err := store.StartInvocation(ctx, "crash", 1, "merge", "cursor", "auto", 121); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func TestAccountingCrashRecoveryAndInterruptedCoverage(t *testing.T) {
	home := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestAccountingCrashChild$")
	command.Env = append(os.Environ(), "COSLASH_ACCOUNTING_CRASH_CHILD=1", "COSLASH_ACCOUNTING_TEST_HOME="+home)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child: %v: %s", err, output)
	}
	store, err := OpenAccountingStore(home, 200)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	inspector, err := store.ReadCosts(context.Background(), CostQuery{SourceID: "local", Agent: "claude", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if inspector.Rounds[0].Outcome != "interrupted" || inspector.Totals.InvocationCount != 2 || inspector.Totals.UnknownInvocationCount != 1 || inspector.Totals.IncompleteRoundCount != 1 || *inspector.Totals.KnownCostMicroUSD != 17 || inspector.Rounds[0].Totals.IncompleteRoundCount != 1 {
		t.Fatalf("recovered: %+v", inspector)
	}
	if len(inspector.ByVendor) != 2 || inspector.ByVendor[0].Totals.IncompleteRoundCount != 1 || inspector.ByVendor[1].Totals.IncompleteRoundCount != 1 {
		t.Fatalf("vendors: %+v", inspector.ByVendor)
	}
	month, err := store.ReadCosts(context.Background(), CostQuery{SourceID: "local", SinceMs: micro(111), UntilMs: micro(122)})
	if err != nil {
		t.Fatal(err)
	}
	if month.Totals.IncompleteRoundCount != 1 || month.ByVendor[0].Totals.IncompleteRoundCount != 1 || month.ByVendor[1].Totals.IncompleteRoundCount != 1 {
		t.Fatalf("month: %+v", month)
	}
}

func TestAccountingInterruptedAfterPaidCompletionCountsIncomplete(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	store, err := OpenAccountingStore(home, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginRound(ctx, Round{ID: "paid", SourceID: "local", Agent: "claude", SessionID: "s", SourceRevision: 1, StartedAtMs: 110}); err != nil {
		t.Fatal(err)
	}
	if err := store.StartInvocation(ctx, "paid", 0, "source", "codex", "gpt-4o", 111); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishInvocation(ctx, "paid", 0, 120, "success", UsageReport{ReportedCostMicroUSD: micro(17)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenAccountingStore(home, 200)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	inspector, err := store.ReadCosts(ctx, CostQuery{SourceID: "local", Agent: "claude", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if inspector.Totals.IncompleteRoundCount != 1 || inspector.ByVendor[0].Totals.IncompleteRoundCount != 1 || inspector.Rounds[0].Totals.IncompleteRoundCount != 1 || inspector.Totals.UnknownInvocationCount != 0 {
		t.Fatalf("inspector: %+v", inspector)
	}
	month, err := store.ReadCosts(ctx, CostQuery{SourceID: "local", SinceMs: micro(111), UntilMs: micro(112)})
	if err != nil {
		t.Fatal(err)
	}
	if month.Totals.IncompleteRoundCount != 1 || month.ByVendor[0].Totals.IncompleteRoundCount != 1 {
		t.Fatalf("month: %+v", month)
	}
}

func TestAccountingFinishRoundRespectsInvocationTimes(t *testing.T) {
	ctx := context.Background()
	store, err := OpenAccountingStore(t.TempDir(), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.BeginRound(ctx, Round{ID: "times", SourceID: "local", Agent: "claude", SessionID: "s", SourceRevision: 1, StartedAtMs: 110}); err != nil {
		t.Fatal(err)
	}
	for ordinal, finished := range []int64{120, 125} {
		if err := store.StartInvocation(ctx, "times", ordinal, "source", "codex", "gpt-4o", 111); err != nil {
			t.Fatal(err)
		}
		if err := store.FinishInvocation(ctx, "times", ordinal, finished, "success", UsageReport{ReportedCostMicroUSD: micro(1)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.FinishRound(ctx, "times", 124, "success"); err == nil {
		t.Fatal("round finished before latest invocation")
	}
	if err := store.FinishRound(ctx, "times", 125, "success"); err != nil {
		t.Fatalf("equal boundary: %v", err)
	}
	got, err := store.ReadCosts(ctx, CostQuery{SourceID: "local", Agent: "claude", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if *got.Rounds[0].FinishedAtMs != 125 || got.Rounds[0].Outcome != "success" {
		t.Fatalf("round: %+v", got.Rounds[0])
	}
}

func TestAccountingStoreLifecycleAndIdentity(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	store, err := OpenAccountingStore(home, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "codex"} {
		id := "round-" + agent
		if err := store.BeginRound(ctx, Round{ID: id, SourceID: "local", Agent: agent, SessionID: "shared", SourceRevision: 7, StartedAtMs: 200}); err != nil {
			t.Fatal(err)
		}
		if err := store.StartInvocation(ctx, id, 0, "source", "cursor", "gpt-4o", 201); err != nil {
			t.Fatal(err)
		}
		usage := UsageReport{ReportedCostMicroUSD: micro(17), Coverage: "complete", Tokens: map[string]session.ModelTokens{}}
		if err := store.FinishInvocation(ctx, id, 0, 202, "success", usage); err != nil {
			t.Fatal(err)
		}
		if err := store.FinishInvocation(ctx, id, 0, 202, "success", usage); err != nil {
			t.Fatalf("replay: %v", err)
		}
		usage.ReportedCostMicroUSD = micro(18)
		if err := store.FinishInvocation(ctx, id, 0, 202, "success", usage); err == nil {
			t.Fatal("conflicting replay accepted")
		}
		if err := store.FinishRound(ctx, id, 203, "success"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenAccountingStore(home, 300)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, agent := range []string{"claude", "codex"} {
		got, err := store.ReadCosts(ctx, CostQuery{SourceID: "local", Agent: agent, SessionID: "shared"})
		if err != nil {
			t.Fatal(err)
		}
		if got.Totals.KnownCostMicroUSD == nil || *got.Totals.KnownCostMicroUSD != 17 || got.Totals.RoundCount != 1 || got.Totals.InvocationCount != 1 || len(got.Rounds) != 1 || got.Rounds[0].Agent != agent {
			t.Fatalf("%s: %+v", agent, got)
		}
		if got.Totals.IncompleteRoundCount != 0 || got.Totals.UnknownInvocationCount != 0 {
			t.Fatalf("complete round: %+v", got.Totals)
		}
	}
}

func TestAccountingMonthAndPagination(t *testing.T) {
	ctx := context.Background()
	store, err := OpenAccountingStore(t.TempDir(), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []string{"a", "b", "c"} {
		if err := store.BeginRound(ctx, Round{ID: id, SourceID: "local", Agent: "claude", SessionID: "s", SourceRevision: 1, StartedAtMs: 1000}); err != nil {
			t.Fatal(err)
		}
		for ordinal, at := range []int64{1999, 2000} {
			if err := store.StartInvocation(ctx, id, ordinal, "source", "claude", "gpt-4o", at); err != nil {
				t.Fatal(err)
			}
			if err := store.FinishInvocation(ctx, id, ordinal, at+1, "success", UsageReport{ReportedCostMicroUSD: micro(10), Coverage: "complete", Tokens: map[string]session.ModelTokens{}}); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.FinishRound(ctx, id, 2002, "success"); err != nil {
			t.Fatal(err)
		}
	}
	monthly, err := store.ReadCosts(ctx, CostQuery{SourceID: "local", SinceMs: micro(2000), UntilMs: micro(3000)})
	if err != nil {
		t.Fatal(err)
	}
	if *monthly.Totals.KnownCostMicroUSD != 30 || monthly.Totals.InvocationCount != 3 || len(monthly.Rounds) != 0 {
		t.Fatalf("monthly: %+v", monthly)
	}
	if len(monthly.ByVendor) != 1 || monthly.ByVendor[0].Totals.RoundCount != 3 {
		t.Fatalf("monthly vendors: %+v", monthly.ByVendor)
	}
	var ids []string
	cursor := ""
	for {
		page, err := store.ReadCosts(ctx, CostQuery{SourceID: "local", Agent: "claude", SessionID: "s", Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if *page.Totals.KnownCostMicroUSD != 60 {
			t.Fatalf("page total: %+v", page.Totals)
		}
		for _, round := range page.Rounds {
			ids = append(ids, round.ID)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if !reflect.DeepEqual(ids, []string{"c", "b", "a"}) {
		t.Fatalf("ids: %v", ids)
	}
}

func TestAccountingVendorRoundCoverage(t *testing.T) {
	ctx := context.Background()
	store, err := OpenAccountingStore(t.TempDir(), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []string{"first", "second"} {
		if err := store.BeginRound(ctx, Round{ID: id, SourceID: "local", Agent: "claude", SessionID: "s", SourceRevision: 1, StartedAtMs: 110}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.StartInvocation(ctx, "first", 0, "source", "codex", "gpt-4o", 111); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishInvocation(ctx, "first", 0, 112, "failed", UsageReport{Tokens: map[string]session.ModelTokens{"gpt-4o": {InputTokens: 1000}, "unknown-model": {InputTokens: 1}}, Coverage: "partial"}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRound(ctx, "first", 113, "failed"); err != nil {
		t.Fatal(err)
	}
	if err := store.StartInvocation(ctx, "second", 0, "source", "cursor", "auto", 111); err != nil {
		t.Fatal(err)
	}
	response, err := store.ReadCosts(ctx, CostQuery{SourceID: "local", Agent: "claude", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Totals.RoundCount != 2 || response.Totals.IncompleteRoundCount != 2 || response.Totals.UnknownInvocationCount != 2 || *response.Totals.KnownCostMicroUSD != 2500 {
		t.Fatalf("totals: %+v", response.Totals)
	}
	if len(response.ByVendor) != 2 || response.ByVendor[0].Totals.RoundCount != 1 || response.ByVendor[0].Totals.IncompleteRoundCount != 1 || response.ByVendor[1].Totals.RoundCount != 1 || response.ByVendor[1].Totals.IncompleteRoundCount != 1 {
		t.Fatalf("vendors: %+v", response.ByVendor)
	}
}

func TestAccountingRecoveryAndValidation(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	store, err := OpenAccountingStore(home, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginRound(ctx, Round{ID: "unfinished", SourceID: "local", Agent: "claude", SessionID: "s", SourceRevision: 1, StartedAtMs: 110}); err != nil {
		t.Fatal(err)
	}
	if err := store.StartInvocation(ctx, "unfinished", 0, "source", "codex", "auto", 111); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenAccountingStore(home, 200)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.ReadCosts(ctx, CostQuery{SourceID: "local", Agent: "claude", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Rounds[0].Outcome != "interrupted" || got.Totals.UnknownInvocationCount != 1 || got.Totals.IncompleteRoundCount != 1 || got.Totals.KnownCostMicroUSD != nil {
		t.Fatalf("recovered: %+v", got)
	}
	if err := store.BeginRound(ctx, Round{ID: "bad", SourceID: "remote", Agent: "claude", SessionID: "s", SourceRevision: 1, StartedAtMs: 1}); err == nil {
		t.Fatal("remote source accepted")
	}
	if err := store.StartInvocation(ctx, "unfinished", 64, "source", "codex", "auto", 1); err == nil {
		t.Fatal("ordinal 64 accepted")
	}
	if err := store.FinishInvocation(ctx, "unfinished", 0, 210, "success", UsageReport{Tokens: map[string]session.ModelTokens{strings.Repeat("x", 513): {}}, Coverage: "unknown"}); err == nil {
		t.Fatal("long model accepted")
	}
	if _, err := store.ReadCosts(ctx, CostQuery{SourceID: "local", Agent: "claude", SessionID: "s", Cursor: "bad"}); err == nil {
		t.Fatal("bad cursor accepted")
	}
	if err := store.FinishInvocation(ctx, "unfinished", 0, 210, "success", UsageReport{Tokens: map[string]session.ModelTokens{}, ReportedCostMicroUSD: micro(1)}); err == nil {
		t.Fatal("interrupted invocation overwritten")
	}
}

func TestAccountingRejectsUnsafeStorage(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "synthesis-accounting")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "costs.sqlite")
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAccountingStore(home, 1); err == nil {
		t.Fatal("corrupt database accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	recovered, err := OpenAccountingStore(home, 2)
	if err != nil {
		t.Fatalf("failed open retained ownership lock: %v", err)
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "target"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAccountingStore(home, 1); err == nil {
		t.Fatal("symlink accepted")
	}
	other := t.TempDir()
	blocked := filepath.Join(other, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAccountingStore(blocked, 1); err == nil {
		t.Fatal("non-directory home accepted")
	}
}

func TestAccountingRejectsLockSymlink(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "synthesis-accounting")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "target")
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "costs.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAccountingStore(home, 100); err == nil {
		t.Fatal("lock symlink accepted")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "untouched" {
		t.Fatalf("symlink target changed: %q", data)
	}
}
