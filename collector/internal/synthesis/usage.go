package synthesis

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"sort"

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
	CacheCreation            *claudeCacheCreation `json:"cacheCreation"`
}

func componentMicros(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var dollars float64
	if json.Unmarshal(raw, &dollars) != nil {
		return 0, false
	}
	micros, err := microUSD(dollars)
	return micros, err == nil
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
	if value, ok := componentMicros(envelope["total_cost_usd"]); ok {
		reported = &value
	}
	componentPartial := false
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
		_ = json.Unmarshal(envelope["usage"], &topUsage)
		tokens = make(map[string]session.ModelTokens, len(modelUsage))
		allCosts, validTokens, hasCost := true, true, false
		var knownCost int64
		models := make([]string, 0, len(modelUsage))
		for model := range modelUsage {
			models = append(models, model)
		}
		sort.Strings(models)
		for _, model := range models {
			raw := modelUsage[model]
			var costField struct {
				CostUSD json.RawMessage `json:"costUSD"`
			}
			if json.Unmarshal(raw, &costField) != nil {
				allCosts, validTokens = false, false
				continue
			}
			if cost, ok := componentMicros(costField.CostUSD); ok && cost <= maxSafeInteger-knownCost {
				knownCost += cost
				hasCost = true
			} else {
				allCosts = false
			}
			var usage claudeTokenUsage
			if json.Unmarshal(raw, &usage) != nil {
				validTokens = false
				continue
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
		if reported == nil && hasCost {
			reported = &knownCost
			componentPartial = !allCosts
		}
		componentPartial = componentPartial || (!validTokens && len(tokens) > 0)
		if len(tokens) == 0 {
			tokens = nil
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
	if componentPartial {
		report.Coverage = "partial"
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

func parseCodexSynthesis(data []byte) (session.SessionSynthesis, error) {
	var message string
	err := eachSynthesisEvent(data, func(line []byte) {
		var event struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if json.Unmarshal(line, &event) == nil && event.Type == "item.completed" && event.Item.Type == "agent_message" {
			message = event.Item.Text
		}
	})
	if err != nil {
		return session.SessionSynthesis{}, err
	}
	if message == "" {
		return session.SessionSynthesis{}, errors.New("Codex produced no completed agent message")
	}
	if err := requireSynthesisFields([]byte(stripJSONFence(message))); err != nil {
		return session.SessionSynthesis{}, err
	}
	return parseSynthesis([]byte(message))
}

func parseCodexUsage(data []byte, model string) UsageReport {
	if model == "" || model == "auto" {
		return unknownUsage()
	}
	var total session.ModelTokens
	completed, invalid := false, false
	scanErr := eachSynthesisEvent(data, func(line []byte) {
		var header struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &header) != nil || header.Type != "turn.completed" {
			return
		}
		var event struct {
			Usage struct {
				Input      *int `json:"input_tokens"`
				Cached     *int `json:"cached_input_tokens"`
				CacheWrite *int `json:"cache_write_input_tokens"`
				Output     *int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(line, &event) != nil {
			invalid = true
			return
		}
		if event.Usage.Input == nil || event.Usage.Cached == nil || event.Usage.Output == nil {
			invalid = true
			return
		}
		u := event.Usage
		write := 0
		if u.CacheWrite != nil {
			write = *u.CacheWrite
		}
		if *u.Input < 0 || *u.Cached < 0 || write < 0 || *u.Output < 0 || *u.Cached > *u.Input || write > *u.Input-*u.Cached {
			invalid = true
			return
		}
		completed = true
		total = session.ModelTokens{
			InputTokens:              *u.Input - *u.Cached - write,
			CacheReadInputTokens:     *u.Cached,
			CacheCreationInputTokens: write,
			OutputTokens:             *u.Output,
		}
	})
	// Codex emits zero-value usage when no token notification arrived.
	if !completed || invalid || total == (session.ModelTokens{}) {
		return unknownUsage()
	}
	report, err := PriceUsage(map[string]session.ModelTokens{model: total}, nil)
	if err != nil {
		return unknownUsage()
	}
	if scanErr != nil {
		report.Coverage = "partial"
	}
	return report
}

func parseCursorSynthesis(data []byte) (session.SessionSynthesis, error) {
	var result []byte
	err := eachSynthesisEvent(data, func(line []byte) {
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &event) == nil && event.Type == "result" {
			result = append(result[:0], line...)
		}
	})
	if err != nil {
		return session.SessionSynthesis{}, err
	}
	if len(result) == 0 {
		return session.SessionSynthesis{}, errors.New("Cursor produced no final result")
	}
	return parseResultEnvelope(result)
}

func parseCursorUsage(data []byte) UsageReport {
	const unknownModel = "cursor/unknown-model"
	var model string
	var tokens *session.ModelTokens
	scanErr := eachSynthesisEvent(data, func(line []byte) {
		var event struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			Model   string `json:"model"`
			Usage   *struct {
				Input      *int `json:"inputTokens"`
				Output     *int `json:"outputTokens"`
				CacheRead  *int `json:"cacheReadTokens"`
				CacheWrite *int `json:"cacheWriteTokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(line, &event) != nil {
			return
		}
		if event.Type == "system" && event.Subtype == "init" {
			model = event.Model
		}
		if event.Type != "result" {
			return
		}
		tokens = nil
		if u := event.Usage; u != nil && u.Input != nil && u.Output != nil && u.CacheRead != nil && u.CacheWrite != nil {
			tokens = &session.ModelTokens{InputTokens: *u.Input, OutputTokens: *u.Output, CacheReadInputTokens: *u.CacheRead, CacheCreationInputTokens: *u.CacheWrite}
		}
	})
	if tokens == nil {
		return unknownUsage()
	}
	if model == "" || model == "auto" {
		model = unknownModel
	}
	report, err := PriceUsage(map[string]session.ModelTokens{model: *tokens}, nil)
	if err != nil {
		return unknownUsage()
	}
	if scanErr != nil {
		report.Coverage = "partial"
	}
	return report
}
