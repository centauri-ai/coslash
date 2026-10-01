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
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
	Model      struct {
		ProviderID string `json:"providerID"`
		ID         string `json:"id"`
	} `json:"model"`
	Cost   *float64 `json:"cost"`
	Tokens *struct {
		Input     *int `json:"input"`
		Output    *int `json:"output"`
		Reasoning *int `json:"reasoning"`
		Cache     struct {
			Read  *int `json:"read"`
			Write *int `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
}

func (u openCodeUsage) modelTokens() (string, session.ModelTokens, bool) {
	modelID, providerID := u.ModelID, u.ProviderID
	if modelID == "" {
		modelID, providerID = u.Model.ID, u.Model.ProviderID
	}
	if modelID == "" || u.Tokens == nil || u.Tokens.Input == nil || u.Tokens.Output == nil || u.Tokens.Cache.Read == nil || u.Tokens.Cache.Write == nil {
		return "", session.ModelTokens{}, false
	}
	reasoning := 0
	if u.Tokens.Reasoning != nil {
		reasoning = *u.Tokens.Reasoning
	}
	if *u.Tokens.Input < 0 || *u.Tokens.Output < 0 || reasoning < 0 || *u.Tokens.Cache.Read < 0 || *u.Tokens.Cache.Write < 0 || int64(*u.Tokens.Output)+int64(reasoning) > maxSafeInteger {
		return "", session.ModelTokens{}, false
	}
	model := modelID
	if providerID != "" {
		model = providerID + "/" + model
	}
	return model, session.ModelTokens{InputTokens: *u.Tokens.Input, OutputTokens: *u.Tokens.Output + reasoning, CacheReadInputTokens: *u.Tokens.Cache.Read, CacheCreationInputTokens: *u.Tokens.Cache.Write}, true
}

func openCodePartsReport(parts map[string]openCodeUsage) UsageReport {
	if len(parts) == 0 {
		return unknownUsage()
	}
	tokens := map[string]session.ModelTokens{}
	completeTokens, completeCost := true, true
	var dollars float64
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
		if part.Cost == nil {
			completeCost = false
		} else {
			dollars += *part.Cost
		}
	}
	if !completeTokens {
		tokens = nil
	}
	var reported *int64
	if completeCost {
		if value, err := microUSD(dollars); err == nil {
			reported = &value
		}
	}
	report, err := PriceUsage(tokens, reported)
	if err != nil {
		return priceReportedOnly(reported)
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
	if err != nil || mixedSessions {
		return unknownUsage()
	}
	stream := openCodePartsReport(parts)
	if stream.Tokens != nil && stream.ReportedCostMicroUSD != nil {
		return stream
	}
	fromDB := readOpenCodeScratchUsage(databasePath, sessionID, v2)
	if fromDB.Tokens == nil {
		report, err := PriceUsage(stream.Tokens, preferCost(stream.ReportedCostMicroUSD, fromDB.ReportedCostMicroUSD))
		if err != nil {
			return priceReportedOnly(preferCost(stream.ReportedCostMicroUSD, fromDB.ReportedCostMicroUSD))
		}
		return report
	}
	if stream.ReportedCostMicroUSD != nil {
		fromDB.ReportedCostMicroUSD = stream.ReportedCostMicroUSD
	}
	report, err := PriceUsage(fromDB.Tokens, fromDB.ReportedCostMicroUSD)
	if err != nil {
		return priceReportedOnly(fromDB.ReportedCostMicroUSD)
	}
	return report
}

func preferCost(first, second *int64) *int64 {
	if first != nil {
		return first
	}
	return second
}

func readOpenCodeScratchUsage(path, sessionID string, v2 bool) UsageReport {
	if sessionID == "" {
		return unknownUsage()
	}
	if _, err := os.Stat(path); err != nil {
		return unknownUsage()
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
		return unknownUsage()
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	query := `SELECT substr(data,1,262145), length(data) FROM message WHERE session_id = ? AND json_extract(substr(data,1,262145),'$.role') = 'assistant' LIMIT 65`
	if v2 {
		query = `SELECT substr(data,1,262145), length(data) FROM session_message WHERE session_id = ? AND type = 'assistant' LIMIT 65`
	}
	rows, err := db.QueryContext(ctx, query, sessionID)
	if err != nil {
		return unknownUsage()
	}
	defer rows.Close()
	parts := map[string]openCodeUsage{}
	for index := 0; rows.Next(); index++ {
		if index >= 64 {
			return unknownUsage()
		}
		var raw string
		var length int
		if rows.Scan(&raw, &length) != nil || length > 262144 {
			return unknownUsage()
		}
		var part openCodeUsage
		if json.Unmarshal([]byte(raw), &part) != nil {
			return unknownUsage()
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
		return unknownUsage()
	}
	return openCodePartsReport(parts)
}
