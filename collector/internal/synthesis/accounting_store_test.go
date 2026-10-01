package synthesis

import (
	"math"
	"reflect"
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
