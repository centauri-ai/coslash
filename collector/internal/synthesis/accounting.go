package synthesis

import (
	"fmt"
	"math"
	"sort"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

const maxSafeInteger int64 = 1<<53 - 1

type UsageReport struct {
	Tokens                map[string]session.ModelTokens `json:"tokens"`
	ReportedCostMicroUSD  *int64                         `json:"reportedCostMicroUsd"`
	EstimatedCostMicroUSD *int64                         `json:"estimatedCostMicroUsd"`
	Coverage              string                         `json:"coverage"`
	UnpricedModels        []string                       `json:"unpricedModels"`
}

type RunResult struct {
	Synthesis session.SessionSynthesis
	Usage     UsageReport
}

type VendorModel struct {
	Vendor string `json:"vendor"`
	Model  string `json:"model"`
}

type Round struct {
	ID             string                         `json:"id"`
	SourceID       string                         `json:"sourceId"`
	Agent          string                         `json:"agent"`
	SessionID      string                         `json:"sessionId"`
	SourceRevision int64                          `json:"sourceRevision"`
	StartedAtMs    int64                          `json:"startedAtMs"`
	FinishedAtMs   *int64                         `json:"finishedAtMs"`
	Outcome        string                         `json:"outcome"`
	VendorModels   []VendorModel                  `json:"vendorModels"`
	Totals         CostTotals                     `json:"totals"`
	Tokens         map[string]session.ModelTokens `json:"tokens"`
}

type CostQuery struct {
	SourceID          string
	Agent             string
	SessionID         string
	SinceMs           *int64
	UntilMs           *int64
	Limit             int
	Cursor            string
	HistoricalUnknown bool
}

type CostResponse struct {
	SourceID            string        `json:"sourceId"`
	TrackingStartedAtMs int64         `json:"trackingStartedAtMs"`
	HistoricalUnknown   bool          `json:"historicalUnknown"`
	Totals              CostTotals    `json:"totals"`
	ByVendor            []VendorCosts `json:"byVendor"`
	Rounds              []Round       `json:"rounds"`
	NextCursor          *string       `json:"nextCursor"`
}

type CostTotals struct {
	KnownCostMicroUSD      *int64 `json:"knownCostMicroUsd"`
	RoundCount             int64  `json:"roundCount"`
	InvocationCount        int64  `json:"invocationCount"`
	UnknownInvocationCount int64  `json:"unknownInvocationCount"`
	IncompleteRoundCount   int64  `json:"incompleteRoundCount"`
}

type VendorCosts struct {
	Vendor string     `json:"vendor"`
	Totals CostTotals `json:"totals"`
}

// PriceUsage prices actual model buckets. A reported amount takes precedence,
// including zero, while the estimate remains available for comparison.
func PriceUsage(tokens map[string]session.ModelTokens, reported *int64) (UsageReport, error) {
	if len(tokens) > 64 {
		return UsageReport{}, fmt.Errorf("too many models")
	}
	if reported != nil && (*reported < 0 || *reported > maxSafeInteger) {
		return UsageReport{}, fmt.Errorf("unsafe reported cost")
	}
	result := UsageReport{Tokens: tokens, ReportedCostMicroUSD: reported}
	var estimate float64
	priced := 0
	models := make([]string, 0, len(tokens))
	for model := range tokens {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		used := tokens[model]
		if model == "" || len(model) > 512 {
			return UsageReport{}, fmt.Errorf("invalid model")
		}
		if used.InputTokens < 0 || used.OutputTokens < 0 || used.CacheCreationInputTokens < 0 || used.CacheCreation1hInputTokens < 0 || used.CacheReadInputTokens < 0 || math.IsNaN(used.Cost) || math.IsInf(used.Cost, 0) || used.Cost < 0 {
			return UsageReport{}, fmt.Errorf("invalid usage")
		}
		if _, ok := safeTokenSum(used); !ok {
			return UsageReport{}, fmt.Errorf("unsafe token count")
		}
		s := session.Session{Tokens: map[string]session.ModelTokens{model: used}}
		session.AttachCost(&s, nil)
		if len(s.UnpricedModels) != 0 {
			result.UnpricedModels = append(result.UnpricedModels, model)
			continue
		}
		priced++
		estimate += *s.Cost
	}
	sort.Strings(result.UnpricedModels)
	if priced > 0 {
		micros, err := microUSD(estimate)
		if err != nil {
			return UsageReport{}, err
		}
		result.EstimatedCostMicroUSD = &micros
	}
	switch {
	case reported != nil:
		result.Coverage = "complete"
	case tokens == nil || (len(tokens) > 0 && priced == 0):
		result.Coverage = "unknown"
	case len(result.UnpricedModels) > 0:
		result.Coverage = "partial"
	default:
		result.Coverage = "complete"
		if len(tokens) == 0 {
			result.EstimatedCostMicroUSD = new(int64)
		}
	}
	return result, nil
}

func safeTokenSum(t session.ModelTokens) (int64, bool) {
	var sum int64
	for _, n := range []int{t.InputTokens, t.OutputTokens, t.CacheCreationInputTokens, t.CacheCreation1hInputTokens, t.CacheReadInputTokens} {
		if n < 0 || int64(n) > maxSafeInteger-sum {
			return 0, false
		}
		sum += int64(n)
	}
	return sum, true
}

func microUSD(dollars float64) (int64, error) {
	value := dollars * 1_000_000
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > float64(maxSafeInteger) {
		return 0, fmt.Errorf("unsafe estimated cost")
	}
	return int64(math.Round(value)), nil
}
