package synthesis

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

type openCodeUsage struct {
	ProviderID string          `json:"providerID"`
	ModelID    string          `json:"modelID"`
	Model      json.RawMessage `json:"model"`
	Cost       json.RawMessage `json:"cost"`
	Tokens     json.RawMessage `json:"tokens"`
}

func (u openCodeUsage) modelTokens() (string, session.ModelTokens, bool) {
	modelID, providerID := u.ModelID, u.ProviderID
	if modelID == "" {
		var model struct {
			ProviderID string `json:"providerID"`
			ID         string `json:"id"`
		}
		if json.Unmarshal(u.Model, &model) != nil {
			return "", session.ModelTokens{}, false
		}
		modelID, providerID = model.ID, model.ProviderID
	}
	var tokens struct {
		Input     *int `json:"input"`
		Output    *int `json:"output"`
		Reasoning *int `json:"reasoning"`
		Cache     struct {
			Read  *int `json:"read"`
			Write *int `json:"write"`
		} `json:"cache"`
	}
	if modelID == "" || json.Unmarshal(u.Tokens, &tokens) != nil || tokens.Input == nil || tokens.Output == nil || tokens.Cache.Read == nil || tokens.Cache.Write == nil {
		return "", session.ModelTokens{}, false
	}
	reasoning := 0
	if tokens.Reasoning != nil {
		reasoning = *tokens.Reasoning
	}
	if *tokens.Input < 0 || *tokens.Output < 0 || reasoning < 0 || *tokens.Cache.Read < 0 || *tokens.Cache.Write < 0 || int64(*tokens.Output)+int64(reasoning) > maxSafeInteger {
		return "", session.ModelTokens{}, false
	}
	model := modelID
	if providerID != "" {
		model = providerID + "/" + model
	}
	return model, session.ModelTokens{InputTokens: *tokens.Input, OutputTokens: *tokens.Output + reasoning, CacheReadInputTokens: *tokens.Cache.Read, CacheCreationInputTokens: *tokens.Cache.Write}, true
}

func openCodePartsReport(parts map[string]openCodeUsage) UsageReport {
	if len(parts) == 0 {
		return unknownUsage()
	}
	tokens := map[string]session.ModelTokens{}
	completeTokens, completeCost, hasCost := true, true, false
	var knownCost int64
	for _, part := range parts {
		model, used, ok := part.modelTokens()
		if !ok {
			completeTokens = false
		} else {
			current := tokens[model]
			if !addTokens(&current, used) {
				completeTokens = false
			} else {
				tokens[model] = current
			}
		}
		if cost, ok := componentMicros(part.Cost); ok && cost <= maxSafeInteger-knownCost {
			knownCost += cost
			hasCost = true
		} else {
			completeCost = false
		}
	}
	var reported *int64
	if hasCost {
		reported = &knownCost
	}
	if len(tokens) == 0 {
		tokens = nil
	}
	report, err := PriceUsage(tokens, reported)
	if err != nil {
		report = priceReportedOnly(reported)
	}
	if (!completeTokens && len(tokens) > 0) || (hasCost && !completeCost) {
		report.Coverage = "partial"
	}
	return report
}

func parseOpenCodeUsage(data []byte, databasePath string, v2 bool) UsageReport {
	parts := map[string]openCodeUsage{}
	var sessionID string
	mixedSessions := false
	err := eachSynthesisEvent(data, func(line []byte) {
		var event struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionID"`
			Part      struct {
				ID string `json:"id"`
				openCodeUsage
			} `json:"part"`
		}
		if json.Unmarshal(line, &event) != nil {
			return
		}
		if event.SessionID != "" && sessionID != "" && event.SessionID != sessionID {
			mixedSessions = true
		}
		if event.SessionID != "" {
			sessionID = event.SessionID
		}
		if event.Type == "step_finish" && event.Part.ID != "" {
			parts[event.Part.ID] = event.Part.openCodeUsage
		}
	})
	if mixedSessions {
		return unknownUsage()
	}
	stream := openCodePartsReport(parts)
	fromDB, dbCount, dbIncomplete := readOpenCodeScratchUsage(databasePath, sessionID, v2)
	selected := stream
	if dbCount > len(parts) || (dbCount == len(parts) && selected.Tokens == nil && fromDB.Tokens != nil) {
		selected = fromDB
	}
	cost := stream.ReportedCostMicroUSD
	if other := fromDB.ReportedCostMicroUSD; other != nil && (cost == nil || *other > *cost) {
		cost = other
	}
	report, priceErr := PriceUsage(selected.Tokens, cost)
	if priceErr != nil {
		report = priceReportedOnly(cost)
	}
	if (len(report.Tokens) > 0 || report.ReportedCostMicroUSD != nil) && (err != nil || dbIncomplete || selected.Coverage == "partial" || (dbCount > 0 && dbCount != len(parts)) || (stream.ReportedCostMicroUSD != nil && fromDB.ReportedCostMicroUSD != nil && *stream.ReportedCostMicroUSD != *fromDB.ReportedCostMicroUSD)) {
		report.Coverage = "partial"
	}
	return report
}

func readOpenCodeScratchUsage(path, sessionID string, v2 bool) (UsageReport, int, bool) {
	if sessionID == "" {
		return unknownUsage(), 0, false
	}
	if _, err := os.Stat(path); err != nil {
		return unknownUsage(), 0, false
	}
	urlPath := path
	if runtime.GOOS == "windows" {
		urlPath = filepath.ToSlash(path)
		if filepath.VolumeName(path) != "" {
			urlPath = "/" + urlPath
		}
	}
	dsn := (&url.URL{Scheme: "file", Path: urlPath, RawQuery: "mode=ro&_query_only=1&_busy_timeout=1000"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return unknownUsage(), 0, false
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	query := `SELECT raw, size FROM (
		SELECT CAST(substr(CAST(data AS BLOB),1,262145) AS TEXT) AS raw,
			length(CAST(data AS BLOB)) AS size
		FROM message WHERE session_id = ?
	) WHERE CASE WHEN size > 262144 THEN 1
		WHEN json_valid(raw) THEN json_extract(raw,'$.role') = 'assistant'
		ELSE 1 END LIMIT 65`
	if v2 {
		query = `SELECT CAST(substr(CAST(data AS BLOB),1,262145) AS TEXT),
			length(CAST(data AS BLOB))
			FROM session_message WHERE session_id = ? AND type = 'assistant' LIMIT 65`
	}
	rows, err := db.QueryContext(ctx, query, sessionID)
	if err != nil {
		return unknownUsage(), 0, false
	}
	defer rows.Close()
	parts := map[string]openCodeUsage{}
	incomplete := false
	for index := 0; rows.Next(); index++ {
		if index >= 64 {
			incomplete = true
			break
		}
		var raw string
		var length int
		if rows.Scan(&raw, &length) != nil {
			incomplete = true
			break
		}
		if length > 262144 || !json.Valid([]byte(raw)) {
			incomplete = true
			continue
		}
		var part openCodeUsage
		if json.Unmarshal([]byte(raw), &part) != nil {
			incomplete = true
			continue
		}
		var completion struct {
			Time struct {
				Completed *int64 `json:"completed"`
			} `json:"time"`
		}
		if json.Unmarshal([]byte(raw), &completion) != nil || completion.Time.Completed == nil {
			continue
		}
		parts[string(rune(index+1))] = part
	}
	if rows.Err() != nil {
		incomplete = true
	}
	report := openCodePartsReport(parts)
	if incomplete && (len(report.Tokens) > 0 || report.ReportedCostMicroUSD != nil) {
		report.Coverage = "partial"
	}
	return report, len(parts), incomplete
}
