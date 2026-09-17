package session

import (
	_ "embed"
	"encoding/json"
	"log"
	"regexp"
	"slices"
	"strings"
)

// No vendor reports list prices, and Claude does not report its context
// window either, so coslash reads both from a table. models.json is a pruned
// copy of LiteLLM's model_prices_and_context_window.json (MIT licensed), which
// tracks new releases within days. Refresh it with `make models`.
//
//go:embed models.json
var modelsJSON []byte

// Costs are dollars per token, as LiteLLM publishes them.
type modelInfo struct {
	Input         float64 `json:"input_cost_per_token"`
	Output        float64 `json:"output_cost_per_token"`
	CacheWrite    float64 `json:"cache_creation_input_token_cost"`
	CacheWrite1h  float64 `json:"cache_creation_input_token_cost_above_1hr"`
	CacheRead     float64 `json:"cache_read_input_token_cost"`
	ContextWindow int     `json:"max_input_tokens"`
}

var models = loadModels()

func loadModels() map[string]modelInfo {
	loaded := map[string]modelInfo{}
	if err := json.Unmarshal(modelsJSON, &loaded); err != nil {
		// A type error zeroes one field but still fills the map, so report the count.
		log.Printf("model table: %v; %d models loaded", err, len(loaded))
	}
	// A model that publishes no cache price bills cached tokens as input, and
	// one that publishes no 1-hour price bills a long write like a short one.
	for model, info := range loaded {
		if info.CacheWrite == 0 {
			info.CacheWrite = info.Input
		}
		if info.CacheWrite1h == 0 {
			info.CacheWrite1h = info.CacheWrite
		}
		if info.CacheRead == 0 {
			info.CacheRead = info.Input
		}
		loaded[model] = info
	}
	return loaded
}

// Anthropic dates a model id as -20251001, OpenAI as -2025-04-16.
var releaseDateSuffix = regexp.MustCompile(`-\d{4}-?\d{2}-?\d{2}$`)

func modelInfoFor(model string) (modelInfo, bool) {
	key := strings.TrimSuffix(model, "[1m]")
	key = strings.TrimPrefix(key, "openai.")

	if info, ok := models[key]; ok {
		return info, true
	}
	if _, unqualified, ok := strings.Cut(key, "/"); ok {
		key = unqualified
		if info, ok := models[key]; ok {
			return info, true
		}
	}
	info, ok := models[releaseDateSuffix.ReplaceAllString(key, "")]
	return info, ok
}

func ContextWindowFor(model string) *int {
	if strings.Contains(model, "[1m]") {
		window := 1_000_000
		return &window
	}
	info, ok := modelInfoFor(model)
	if !ok || info.ContextWindow == 0 {
		return nil
	}
	return &info.ContextWindow
}

func ContextTokens(inputTokens, cacheReadTokens, cacheWriteTokens int) int {
	return inputTokens + cacheReadTokens + cacheWriteTokens
}

func EstimatedCost(tokens map[string]ModelTokens) float64 {
	total := 0.0
	for model, used := range tokens {
		cost, ok := estimatedModelCost(model, used)
		if !ok {
			continue
		}
		total += cost
	}
	return total
}

func estimatedModelCost(model string, used ModelTokens) (float64, bool) {
	info, ok := modelInfoFor(model)
	if !ok {
		return 0, false
	}
	return float64(used.InputTokens)*info.Input +
		float64(used.OutputTokens)*info.Output +
		float64(used.CacheCreationInputTokens)*info.CacheWrite +
		float64(used.CacheCreation1hInputTokens)*info.CacheWrite1h +
		float64(used.CacheReadInputTokens)*info.CacheRead, true
}

func UnpricedModels(tokens map[string]ModelTokens) []string {
	unpriced := []string{}
	for model := range tokens {
		if _, ok := modelInfoFor(model); !ok {
			unpriced = append(unpriced, model)
		}
	}
	slices.Sort(unpriced)
	return unpriced
}

func AttachCost(s *Session, recorded *float64) {
	for model, used := range s.Tokens {
		if cost, ok := estimatedModelCost(model, used); ok {
			used.Cost = cost
			s.Tokens[model] = used
		}
	}
	if recorded != nil {
		s.UnpricedModels = []string{}
		s.Cost = recorded
		return
	}
	s.UnpricedModels = UnpricedModels(s.Tokens)
	if len(s.Tokens) == 0 {
		s.Cost = nil
		return
	}
	estimate := EstimatedCost(s.Tokens)
	s.Cost = &estimate
}
