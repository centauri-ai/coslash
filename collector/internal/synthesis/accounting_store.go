package synthesis

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/session"
	_ "modernc.org/sqlite"
)

type AccountingStore struct {
	db         *sql.DB
	lock       *os.File
	incomplete atomic.Bool
}

var ErrAccountingIncomplete = errors.New("synthesis accounting incomplete")

type roundCursor struct {
	Time int64  `json:"t"`
	ID   string `json:"i"`
}

const monthlyCostsSQL = `SELECT i.round_id,i.vendor,i.selected_cost_micro_usd,i.coverage,r.outcome
 FROM invocations i INDEXED BY invocations_started JOIN rounds r ON r.round_id=i.round_id
 WHERE i.started_at_ms>=? AND i.started_at_ms<? AND r.source_id='local'`

const accountingSchema = `
CREATE TABLE IF NOT EXISTS metadata (schema_version INTEGER NOT NULL, tracking_started_at_ms INTEGER NOT NULL, accounting_incomplete INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS rounds (
 round_id TEXT PRIMARY KEY, source_id TEXT NOT NULL, agent TEXT NOT NULL, session_id TEXT NOT NULL,
 source_revision INTEGER NOT NULL, started_at_ms INTEGER NOT NULL, finished_at_ms INTEGER,
 outcome TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS rounds_session ON rounds(source_id,agent,session_id,started_at_ms DESC,round_id DESC);
CREATE TABLE IF NOT EXISTS invocations (
 round_id TEXT NOT NULL REFERENCES rounds(round_id), ordinal INTEGER NOT NULL,
 phase TEXT NOT NULL, vendor TEXT NOT NULL, configured_model TEXT NOT NULL,
 started_at_ms INTEGER NOT NULL, finished_at_ms INTEGER, outcome TEXT NOT NULL,
 usage_json BLOB, reported_cost_micro_usd INTEGER, estimated_cost_micro_usd INTEGER,
 selected_cost_micro_usd INTEGER, coverage TEXT NOT NULL,
 PRIMARY KEY(round_id,ordinal));
CREATE INDEX IF NOT EXISTS invocations_started ON invocations(started_at_ms);`

func OpenAccountingStore(home string, nowMs int64) (*AccountingStore, error) {
	if home == "" || !safeMs(nowMs) {
		return nil, fmt.Errorf("invalid accounting home or time")
	}
	dir := filepath.Join(home, "synthesis-accounting")
	for _, path := range []string{home, dir} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("accounting path is not a directory")
		}
		if err := protectSynthesisDirectory(path); err != nil {
			return nil, err
		}
	}
	lockPath := filepath.Join(dir, "costs.lock")
	lockInfo, err := os.Lstat(lockPath)
	var lock *os.File
	if errors.Is(err, os.ErrNotExist) {
		lock, err = os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	} else if err == nil && lockInfo.Mode().IsRegular() {
		lock, err = os.OpenFile(lockPath, os.O_RDWR, 0)
	} else if err == nil {
		return nil, fmt.Errorf("accounting lock is not a regular file")
	}
	if err != nil {
		return nil, err
	}
	if err := protectSynthesisFile(lockPath, lock); err != nil {
		lock.Close()
		return nil, err
	}
	if err := lockAccountingFile(lock); err != nil {
		lock.Close()
		return nil, fmt.Errorf("accounting store already open: %w", err)
	}
	opened := false
	defer func() {
		if !opened {
			lock.Close()
		}
	}()
	path := filepath.Join(dir, "costs.sqlite")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		file, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if createErr != nil {
			return nil, createErr
		}
		if err = protectSynthesisFile(path, file); err != nil {
			file.Close()
			return nil, err
		}
		err = file.Close()
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("accounting database is not a regular file")
	} else {
		file, openErr := os.OpenFile(path, os.O_RDWR, 0)
		if openErr != nil {
			return nil, openErr
		}
		if err = protectSynthesisFile(path, file); err != nil {
			file.Close()
			return nil, err
		}
		if err = file.Close(); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &AccountingStore{db: db, lock: lock}
	if err := s.initialize(context.Background(), nowMs); err != nil {
		db.Close()
		return nil, err
	}
	opened = true
	return s, nil
}

func (s *AccountingStore) initialize(ctx context.Context, nowMs int64) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA busy_timeout=1000"); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, accountingSchema); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO metadata(schema_version,tracking_started_at_ms) SELECT 2,? WHERE NOT EXISTS(SELECT 1 FROM metadata)", nowMs); err != nil {
		return err
	}
	var version, count int64
	if err := tx.QueryRowContext(ctx, "SELECT MIN(schema_version), COUNT(*) FROM metadata").Scan(&version, &count); err != nil {
		return err
	}
	if count != 1 || (version != 1 && version != 2) {
		return fmt.Errorf("unsupported accounting schema")
	}
	if version == 1 {
		if _, err := tx.ExecContext(ctx, "ALTER TABLE metadata ADD COLUMN accounting_incomplete INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE metadata SET schema_version=2"); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE invocations SET finished_at_ms=?,outcome='interrupted',coverage='unknown' WHERE finished_at_ms IS NULL", nowMs); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE rounds SET finished_at_ms=?,outcome='interrupted' WHERE finished_at_ms IS NULL", nowMs); err != nil {
		return err
	}
	var incomplete int64
	if err := tx.QueryRowContext(ctx, "SELECT accounting_incomplete FROM metadata").Scan(&incomplete); err != nil {
		return err
	}
	if incomplete != 0 && incomplete != 1 {
		return fmt.Errorf("invalid accounting completeness marker")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.incomplete.Store(incomplete == 1)
	return nil
}

func (s *AccountingStore) Close() error { return errors.Join(s.db.Close(), s.lock.Close()) }

func (s *AccountingStore) Incomplete() bool { return s.incomplete.Load() }

func (s *AccountingStore) MarkIncomplete(ctx context.Context) error {
	result, err := s.db.ExecContext(ctx, "UPDATE metadata SET accounting_incomplete=1")
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return fmt.Errorf("could not mark accounting incomplete: %v", err)
	}
	s.incomplete.Store(true)
	return nil
}

func (s *AccountingStore) BeginRound(ctx context.Context, round Round) error {
	if !validID(round.ID) || round.SourceID != "local" || !validAgentName(round.Agent) || !validAccountingSessionID(round.Agent, round.SessionID) || !safeMs(round.SourceRevision) || !safeMs(round.StartedAtMs) {
		return fmt.Errorf("invalid round")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO rounds(round_id,source_id,agent,session_id,source_revision,started_at_ms,outcome) VALUES(?,?,?,?,?,?,'running')`, round.ID, round.SourceID, round.Agent, round.SessionID, round.SourceRevision, round.StartedAtMs)
	return err
}

func (s *AccountingStore) StartInvocation(ctx context.Context, roundID string, ordinal int, phase, vendor, configuredModel string, startedAtMs int64) error {
	if !validID(roundID) || ordinal < 0 || ordinal >= 64 || (phase != "source" && phase != "merge") || !validAgentName(vendor) || configuredModel == "" || len(configuredModel) > 512 || !safeMs(startedAtMs) {
		return fmt.Errorf("invalid invocation")
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO invocations(round_id,ordinal,phase,vendor,configured_model,started_at_ms,outcome,coverage)
 SELECT round_id,?,?,?,?,?,'running','unknown' FROM rounds WHERE round_id=? AND finished_at_ms IS NULL AND started_at_ms<=?`, ordinal, phase, vendor, configuredModel, startedAtMs, roundID, startedAtMs)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("round is missing or finished")
	}
	return nil
}

func (s *AccountingStore) FinishInvocation(ctx context.Context, roundID string, ordinal int, finishedAtMs int64, outcome string, usage UsageReport) error {
	if !validID(roundID) || ordinal < 0 || ordinal >= 64 || !safeMs(finishedAtMs) || !validInvocationOutcome(outcome) {
		return fmt.Errorf("invalid invocation completion")
	}
	canonical, err := PriceUsage(usage.Tokens, usage.ReportedCostMicroUSD)
	if err != nil {
		return err
	}
	if usage.EstimatedCostMicroUSD != nil && *usage.EstimatedCostMicroUSD != *orZero(canonical.EstimatedCostMicroUSD) {
		return fmt.Errorf("inconsistent estimate")
	}
	if usage.Coverage == "partial" && (canonical.ReportedCostMicroUSD != nil || canonical.EstimatedCostMicroUSD != nil || len(canonical.Tokens) > 0) {
		canonical.Coverage = "partial"
	} else if usage.Coverage != "" && usage.Coverage != canonical.Coverage {
		return fmt.Errorf("inconsistent coverage")
	}
	data, err := json.Marshal(canonical.Tokens)
	if err != nil {
		return err
	}
	if len(data) > 256<<10 {
		return fmt.Errorf("usage too large")
	}
	selected := canonical.EstimatedCostMicroUSD
	if canonical.ReportedCostMicroUSD != nil {
		selected = canonical.ReportedCostMicroUSD
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE invocations SET finished_at_ms=?,outcome=?,usage_json=?,reported_cost_micro_usd=?,estimated_cost_micro_usd=?,selected_cost_micro_usd=?,coverage=?
 WHERE round_id=? AND ordinal=? AND finished_at_ms IS NULL AND started_at_ms<=?`, finishedAtMs, outcome, data, canonical.ReportedCostMicroUSD, canonical.EstimatedCostMicroUSD, selected, canonical.Coverage, roundID, ordinal, finishedAtMs)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var oldTime int64
		var oldOutcome, oldCoverage string
		var oldData []byte
		var oldReported, oldEstimated, oldSelected sql.NullInt64
		err = tx.QueryRowContext(ctx, `SELECT finished_at_ms,outcome,usage_json,reported_cost_micro_usd,estimated_cost_micro_usd,selected_cost_micro_usd,coverage FROM invocations WHERE round_id=? AND ordinal=?`, roundID, ordinal).Scan(&oldTime, &oldOutcome, &oldData, &oldReported, &oldEstimated, &oldSelected, &oldCoverage)
		if err != nil {
			return err
		}
		if oldTime != finishedAtMs || oldOutcome != outcome || oldCoverage != canonical.Coverage || string(oldData) != string(data) || !sameCost(oldReported, canonical.ReportedCostMicroUSD) || !sameCost(oldEstimated, canonical.EstimatedCostMicroUSD) || !sameCost(oldSelected, selected) {
			return fmt.Errorf("conflicting invocation completion")
		}
	}
	return tx.Commit()
}

func (s *AccountingStore) FinishRound(ctx context.Context, roundID string, finishedAtMs int64, outcome string) error {
	if !validID(roundID) || !safeMs(finishedAtMs) || (outcome != "success" && outcome != "failed" && outcome != "interrupted") {
		return fmt.Errorf("invalid round completion")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var invalid int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM invocations WHERE round_id=? AND (finished_at_ms IS NULL OR finished_at_ms>?)", roundID, finishedAtMs).Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return fmt.Errorf("invocation unfinished or later than round completion")
	}
	result, err := tx.ExecContext(ctx, "UPDATE rounds SET finished_at_ms=?,outcome=? WHERE round_id=? AND finished_at_ms IS NULL AND started_at_ms<=?", finishedAtMs, outcome, roundID, finishedAtMs)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var oldTime int64
		var oldOutcome string
		if err := tx.QueryRowContext(ctx, "SELECT finished_at_ms,outcome FROM rounds WHERE round_id=?", roundID).Scan(&oldTime, &oldOutcome); err != nil {
			return err
		}
		if oldTime != finishedAtMs || oldOutcome != outcome {
			return fmt.Errorf("conflicting round completion")
		}
	}
	return tx.Commit()
}

func (s *AccountingStore) ReadCosts(ctx context.Context, query CostQuery) (CostResponse, error) {
	inspector := query.Agent != "" || query.SessionID != ""
	if query.SourceID != "local" || (inspector && (!validAgentName(query.Agent) || !validAccountingSessionID(query.Agent, query.SessionID) || query.SinceMs != nil || query.UntilMs != nil)) || (!inspector && (query.SinceMs == nil || query.UntilMs == nil || query.Cursor != "")) || query.Limit < 0 || query.Limit > 50 {
		return CostResponse{}, fmt.Errorf("invalid cost query")
	}
	if !inspector && (!safeMs(*query.SinceMs) || !safeMs(*query.UntilMs) || *query.SinceMs >= *query.UntilMs) {
		return CostResponse{}, fmt.Errorf("invalid time range")
	}
	var cursor roundCursor
	if query.Cursor != "" {
		if len(query.Cursor) > 1024 {
			return CostResponse{}, fmt.Errorf("invalid cursor")
		}
		data, err := base64.RawURLEncoding.DecodeString(query.Cursor)
		if err != nil || json.Unmarshal(data, &cursor) != nil || !safeMs(cursor.Time) || !validID(cursor.ID) {
			return CostResponse{}, fmt.Errorf("invalid cursor")
		}
	}
	if s.incomplete.Load() {
		return CostResponse{}, ErrAccountingIncomplete
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return CostResponse{}, err
	}
	defer tx.Rollback()
	return s.readCosts(ctx, tx, query, cursor)
}

func (s *AccountingStore) readCosts(ctx context.Context, tx *sql.Tx, query CostQuery, cursor roundCursor) (CostResponse, error) {
	inspector := query.Agent != "" || query.SessionID != ""
	response := CostResponse{SourceID: "local", Rounds: []Round{}, ByVendor: []VendorCosts{}}
	if err := tx.QueryRowContext(ctx, "SELECT tracking_started_at_ms FROM metadata").Scan(&response.TrackingStartedAtMs); err != nil {
		return CostResponse{}, err
	}
	response.HistoricalUnknown = query.HistoricalUnknown || (!inspector && *query.SinceMs < response.TrackingStartedAtMs)
	statement := monthlyCostsSQL
	args := []any{}
	if inspector {
		statement = `SELECT i.round_id,i.vendor,i.selected_cost_micro_usd,i.coverage,r.outcome FROM invocations i JOIN rounds r ON r.round_id=i.round_id WHERE r.source_id='local' AND r.agent=? AND r.session_id=?`
		args = append(args, query.Agent, query.SessionID)
	} else {
		args = append(args, *query.SinceMs, *query.UntilMs)
	}
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return CostResponse{}, err
	}
	seen := map[string]bool{}
	incomplete := map[string]bool{}
	byVendor := map[string]*CostTotals{}
	vendorRounds := map[string]map[string]bool{}
	vendorIncomplete := map[string]map[string]bool{}
	for rows.Next() {
		var roundID, vendor, coverage, outcome string
		var cost sql.NullInt64
		if err = rows.Scan(&roundID, &vendor, &cost, &coverage, &outcome); err != nil {
			break
		}
		seen[roundID] = true
		if coverage != "complete" || outcome == "running" || outcome == "interrupted" {
			incomplete[roundID] = true
		}
		vendorTotals := byVendor[vendor]
		if vendorTotals == nil {
			vendorTotals = &CostTotals{}
			byVendor[vendor] = vendorTotals
			vendorRounds[vendor] = map[string]bool{}
			vendorIncomplete[vendor] = map[string]bool{}
		}
		vendorRounds[vendor][roundID] = true
		if coverage != "complete" || outcome == "running" || outcome == "interrupted" {
			vendorIncomplete[vendor][roundID] = true
		}
		if err = addInvocation(&response.Totals, cost, coverage); err != nil {
			break
		}
		if err = addInvocation(vendorTotals, cost, coverage); err != nil {
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return CostResponse{}, err
	}
	response.Totals.RoundCount = int64(len(seen))
	response.Totals.IncompleteRoundCount = int64(len(incomplete))
	for vendor, totals := range byVendor {
		totals.RoundCount = int64(len(vendorRounds[vendor]))
		totals.IncompleteRoundCount = int64(len(vendorIncomplete[vendor]))
		response.ByVendor = append(response.ByVendor, VendorCosts{Vendor: vendor, Totals: *totals})
	}
	if inspector {
		var all int64
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM rounds WHERE source_id='local' AND agent=? AND session_id=?", query.Agent, query.SessionID).Scan(&all); err != nil {
			return CostResponse{}, err
		}
		response.Totals.RoundCount = all
		runningRows, err := tx.QueryContext(ctx, "SELECT round_id FROM rounds WHERE source_id='local' AND agent=? AND session_id=? AND outcome IN ('running','interrupted')", query.Agent, query.SessionID)
		if err != nil {
			return CostResponse{}, err
		}
		for runningRows.Next() {
			var id string
			if err = runningRows.Scan(&id); err != nil {
				break
			}
			incomplete[id] = true
		}
		if err == nil {
			err = runningRows.Err()
		}
		runningRows.Close()
		if err != nil {
			return CostResponse{}, err
		}
		response.Totals.IncompleteRoundCount = int64(len(incomplete))
		if err := s.readRoundPage(ctx, tx, query, cursor, &response); err != nil {
			return CostResponse{}, err
		}
	}
	if response.Totals.InvocationCount == 0 {
		response.Totals.KnownCostMicroUSD = new(int64)
	}
	for i := range response.ByVendor {
		if response.ByVendor[i].Totals.InvocationCount == 0 {
			response.ByVendor[i].Totals.KnownCostMicroUSD = new(int64)
		}
	}
	sort.Slice(response.ByVendor, func(i, j int) bool { return response.ByVendor[i].Vendor < response.ByVendor[j].Vendor })
	return response, nil
}

func (s *AccountingStore) readRoundPage(ctx context.Context, tx *sql.Tx, query CostQuery, cursor roundCursor, response *CostResponse) error {
	limit := query.Limit
	if limit == 0 {
		limit = 20
	}
	where := "source_id='local' AND agent=? AND session_id=?"
	args := []any{query.Agent, query.SessionID}
	if query.Cursor != "" {
		where += " AND (started_at_ms<? OR (started_at_ms=? AND round_id<?))"
		args = append(args, cursor.Time, cursor.Time, cursor.ID)
	}
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, "SELECT round_id,source_revision,started_at_ms,finished_at_ms,outcome FROM rounds WHERE "+where+" ORDER BY started_at_ms DESC,round_id DESC LIMIT ?", args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var round Round
		var finished sql.NullInt64
		if err = rows.Scan(&round.ID, &round.SourceRevision, &round.StartedAtMs, &finished, &round.Outcome); err != nil {
			break
		}
		round.SourceID, round.Agent, round.SessionID = "local", query.Agent, query.SessionID
		if finished.Valid {
			round.FinishedAtMs = &finished.Int64
		}
		round.VendorModels = []VendorModel{}
		round.Tokens = map[string]session.ModelTokens{}
		response.Rounds = append(response.Rounds, round)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	more := len(response.Rounds) > limit
	if more {
		response.Rounds = response.Rounds[:limit]
	}
	for i := range response.Rounds {
		if err := s.fillRound(ctx, tx, &response.Rounds[i]); err != nil {
			return err
		}
	}
	if more {
		last := response.Rounds[len(response.Rounds)-1]
		data, _ := json.Marshal(struct {
			Time int64  `json:"t"`
			ID   string `json:"i"`
		}{last.StartedAtMs, last.ID})
		cursor := base64.RawURLEncoding.EncodeToString(data)
		response.NextCursor = &cursor
	}
	return nil
}

func (s *AccountingStore) fillRound(ctx context.Context, tx *sql.Tx, round *Round) error {
	rows, err := tx.QueryContext(ctx, "SELECT vendor,configured_model,substr(usage_json,1,?),selected_cost_micro_usd,coverage FROM invocations WHERE round_id=? ORDER BY ordinal", (256<<10)+1, round.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	models := map[VendorModel]bool{}
	for rows.Next() {
		var vendor, model, coverage string
		var data []byte
		var cost sql.NullInt64
		if err := rows.Scan(&vendor, &model, &data, &cost, &coverage); err != nil {
			return err
		}
		vm := VendorModel{Vendor: vendor, Model: model}
		if !models[vm] {
			round.VendorModels = append(round.VendorModels, vm)
			models[vm] = true
		}
		if err := addInvocation(&round.Totals, cost, coverage); err != nil {
			return err
		}
		if data != nil {
			if len(data) > 256<<10 {
				return fmt.Errorf("stored usage too large")
			}
			var tokens map[string]session.ModelTokens
			if err := json.Unmarshal(data, &tokens); err != nil {
				return err
			}
			if len(tokens) > 64 {
				return fmt.Errorf("stored usage has too many models")
			}
			for name, used := range tokens {
				if name == "" || len(name) > 512 {
					return fmt.Errorf("invalid stored model")
				}
				if _, ok := safeTokenSum(used); !ok || used.Cost < 0 {
					return fmt.Errorf("invalid stored tokens")
				}
				prior := round.Tokens[name]
				if !addTokens(&prior, used) {
					return fmt.Errorf("unsafe token total")
				}
				round.Tokens[name] = prior
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	round.Totals.RoundCount = 1
	if round.Outcome == "running" || round.Outcome == "interrupted" || round.Totals.UnknownInvocationCount > 0 {
		round.Totals.IncompleteRoundCount = 1
	}
	if round.Totals.InvocationCount == 0 {
		round.Totals.KnownCostMicroUSD = new(int64)
	}
	return nil
}

func addInvocation(t *CostTotals, cost sql.NullInt64, coverage string) error {
	if t.InvocationCount >= maxSafeInteger {
		return fmt.Errorf("unsafe invocation count")
	}
	t.InvocationCount++
	if coverage != "complete" {
		t.UnknownInvocationCount++
	}
	if cost.Valid {
		if cost.Int64 < 0 || cost.Int64 > maxSafeInteger {
			return fmt.Errorf("unsafe stored cost")
		}
		if t.KnownCostMicroUSD == nil {
			t.KnownCostMicroUSD = new(int64)
		}
		if *t.KnownCostMicroUSD > maxSafeInteger-cost.Int64 {
			return fmt.Errorf("unsafe cost total")
		}
		*t.KnownCostMicroUSD += cost.Int64
	}
	return nil
}

func addTokens(dst *session.ModelTokens, src session.ModelTokens) bool {
	fields := []*int{&dst.InputTokens, &dst.OutputTokens, &dst.CacheCreationInputTokens, &dst.CacheCreation1hInputTokens, &dst.CacheReadInputTokens}
	values := []int{src.InputTokens, src.OutputTokens, src.CacheCreationInputTokens, src.CacheCreation1hInputTokens, src.CacheReadInputTokens}
	for i, n := range values {
		if n < 0 || int64(n) > maxSafeInteger-int64(*fields[i]) {
			return false
		}
		*fields[i] += n
	}
	return true
}

func sameCost(stored sql.NullInt64, expected *int64) bool {
	return stored.Valid == (expected != nil) && (!stored.Valid || stored.Int64 == *expected)
}
func orZero(value *int64) *int64 {
	if value == nil {
		return new(int64)
	}
	return value
}
func safeMs(n int64) bool { return n >= 0 && n <= maxSafeInteger }
func validInvocationOutcome(s string) bool {
	return s == "success" || s == "failed" || s == "interrupted"
}
func validAgentName(s string) bool {
	return s == "claude" || s == "codex" || s == "opencode" || s == "cursor" || s == "pi" || s == "grok"
}
func validAccountingSessionID(agent, id string) bool {
	if agent != "pi" {
		return validID(id)
	}
	if id == "" || len(id) > 512 || !utf8.ValidString(id) {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validID(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 512 || strings.TrimSpace(s) != s || strings.ContainsAny(s, "/\\") {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}
