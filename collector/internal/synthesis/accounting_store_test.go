package synthesis

import (
	"context"
	"math"
	"os"
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
	if _, err := PriceUsage(map[string]session.ModelTokens{"gpt-4o": {InputTokens: math.MaxInt}}, nil); err == nil {
		t.Fatal("unsafe estimate accepted")
	}
	if _, err := PriceUsage(map[string]session.ModelTokens{"gpt-4o": {InputTokens: -1}}, nil); err == nil {
		t.Fatal("negative tokens accepted")
	}
}

func micro(n int64) *int64 { return &n }

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
