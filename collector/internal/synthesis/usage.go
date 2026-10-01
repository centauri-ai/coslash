package synthesis

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

const maxSynthesisOutputBytes = 16 << 20

func eachSynthesisEvent(data []byte, visit func([]byte)) error {
	if len(data) > maxSynthesisOutputBytes {
		return errors.New("synthesis output exceeds limit")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	count := 0
	for scanner.Scan() {
		count++
		if count > 4096 {
			return errors.New("too many synthesis events")
		}
		visit(scanner.Bytes())
	}
	return scanner.Err()
}

func unknownUsage() UsageReport {
	return UsageReport{Coverage: "unknown"}
}

type claudeTokenUsage struct {
	InputTokens              *int                 `json:"inputTokens"`
	OutputTokens             *int                 `json:"outputTokens"`
	CacheReadInputTokens     *int                 `json:"cacheReadInputTokens"`
	CacheCreationInputTokens *int                 `json:"cacheCreationInputTokens"`
	CostUSD                  *float64             `json:"costUSD"`
	CacheCreation            *claudeCacheCreation `json:"cacheCreation"`
}

type claudeCacheCreation struct {
	Ephemeral5m int `json:"ephemeral_5m_input_tokens"`
	Ephemeral1h int `json:"ephemeral_1h_input_tokens"`
}

func (u claudeTokenUsage) tokens(fallback *claudeCacheCreation) (session.ModelTokens, bool) {
	tier := u.CacheCreation
	if tier == nil {
		tier = fallback
	}
	if tier == nil {
		tier = &claudeCacheCreation{}
	}
	if u.InputTokens == nil || u.OutputTokens == nil || u.CacheReadInputTokens == nil || u.CacheCreationInputTokens == nil ||
		(*u.CacheCreationInputTokens > 0 && u.CacheCreation == nil && fallback == nil) ||
		tier.Ephemeral5m < 0 || tier.Ephemeral1h < 0 ||
		int64(*u.CacheCreationInputTokens) < int64(tier.Ephemeral5m)+int64(tier.Ephemeral1h) {
		return session.ModelTokens{}, false
	}
	return session.ModelTokens{
		InputTokens:                *u.InputTokens,
		OutputTokens:               *u.OutputTokens,
		CacheReadInputTokens:       *u.CacheReadInputTokens,
		CacheCreationInputTokens:   *u.CacheCreationInputTokens - tier.Ephemeral1h,
		CacheCreation1hInputTokens: tier.Ephemeral1h,
	}, true
}

func parseClaudeUsage(data []byte, configuredModel string) UsageReport {
	var envelope map[string]json.RawMessage
	if len(data) > maxSynthesisOutputBytes || json.Unmarshal(data, &envelope) != nil {
		return unknownUsage()
	}
	var reported *int64
	if raw := envelope["total_cost_usd"]; len(raw) > 0 && string(raw) != "null" {
		var dollars float64
		if json.Unmarshal(raw, &dollars) != nil {
			return unknownUsage()
		}
		value, err := microUSD(dollars)
		if err != nil {
			return unknownUsage()
		}
		reported = &value
	}
	tokens := map[string]session.ModelTokens(nil)
	var modelUsage map[string]json.RawMessage
	if raw := envelope["modelUsage"]; len(raw) > 0 && json.Unmarshal(raw, &modelUsage) != nil {
		return priceReportedOnly(reported)
	}
	if len(modelUsage) > 0 {
		var topUsage struct {
			CacheCreation            *claudeCacheCreation `json:"cache_creation"`
			CacheCreationInputTokens *int                 `json:"cache_creation_input_tokens"`
		}
		if len(envelope["usage"]) > 0 && json.Unmarshal(envelope["usage"], &topUsage) != nil {
			return priceReportedOnly(reported)
		}
		tokens = make(map[string]session.ModelTokens, len(modelUsage))
		allCosts, validTokens := true, true
		var modelDollars float64
		for model, raw := range modelUsage {
			var usage claudeTokenUsage
			if json.Unmarshal(raw, &usage) != nil {
				return priceReportedOnly(reported)
			}
			if usage.CostUSD == nil {
				allCosts = false
			} else {
				modelDollars += *usage.CostUSD
			}
			var fallback *claudeCacheCreation
			if len(modelUsage) == 1 && topUsage.CacheCreation != nil && topUsage.CacheCreationInputTokens != nil && usage.CacheCreationInputTokens != nil && *topUsage.CacheCreationInputTokens == *usage.CacheCreationInputTokens {
				fallback = topUsage.CacheCreation
			}
			used, ok := usage.tokens(fallback)
			if !ok {
				validTokens = false
				continue
			}
			tokens[model] = used
		}
		if reported == nil && allCosts {
			if value, err := microUSD(modelDollars); err == nil {
				reported = &value
			}
		}
		if !validTokens {
			return priceReportedOnly(reported)
		}
	} else if len(envelope["usage"]) > 0 && configuredModel != "" && configuredModel != "auto" {
		var usage struct {
			InputTokens              *int                 `json:"input_tokens"`
			OutputTokens             *int                 `json:"output_tokens"`
			CacheReadInputTokens     *int                 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens *int                 `json:"cache_creation_input_tokens"`
			CacheCreation            *claudeCacheCreation `json:"cache_creation"`
		}
		if json.Unmarshal(envelope["usage"], &usage) != nil || usage.InputTokens == nil || usage.OutputTokens == nil || usage.CacheReadInputTokens == nil || usage.CacheCreationInputTokens == nil {
			return priceReportedOnly(reported)
		}
		if usage.CacheCreation == nil && *usage.CacheCreationInputTokens > 0 {
			return priceReportedOnly(reported)
		}
		if usage.CacheCreation == nil {
			usage.CacheCreation = &claudeCacheCreation{}
		}
		if usage.CacheCreation.Ephemeral1h < 0 || usage.CacheCreation.Ephemeral5m < 0 || int64(*usage.CacheCreationInputTokens) < int64(usage.CacheCreation.Ephemeral1h)+int64(usage.CacheCreation.Ephemeral5m) {
			return priceReportedOnly(reported)
		}
		tokens = map[string]session.ModelTokens{configuredModel: {
			InputTokens:                *usage.InputTokens,
			OutputTokens:               *usage.OutputTokens,
			CacheReadInputTokens:       *usage.CacheReadInputTokens,
			CacheCreationInputTokens:   *usage.CacheCreationInputTokens - usage.CacheCreation.Ephemeral1h,
			CacheCreation1hInputTokens: usage.CacheCreation.Ephemeral1h,
		}}
	}
	report, err := PriceUsage(tokens, reported)
	if err != nil {
		return priceReportedOnly(reported)
	}
	return report
}

func priceReportedOnly(reported *int64) UsageReport {
	if reported == nil {
		return unknownUsage()
	}
	report, _ := PriceUsage(nil, reported)
	return report
}
