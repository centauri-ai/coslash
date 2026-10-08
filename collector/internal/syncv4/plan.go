package syncv4

import (
	"errors"
	"sort"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

type ImportSnapshot struct {
	PlanVersion       int64
	Phase             string
	Listed            int64
	ContentSessions   int64
	ContentBytes      int64
	TotalSessions     int64
	TotalBytes        int64
	RateBytesSec      float64
	RateSamples       int
	Rates             []RateSample
	CurrentKey        string
	CurrentSessionID  string
	CurrentBytesDone  int64
	CurrentBytesTotal int64
	LastProgressAt    *time.Time
	HistoryCursorAt   *time.Time
}

type RateSample struct {
	At             int64   `json:"at"`
	BytesPerSecond float64 `json:"bytesPerSecond"`
}

func windowDuration(window string) (time.Duration, bool) {
	switch window {
	case "24h":
		return 24 * time.Hour, true
	case "3d":
		return 72 * time.Hour, true
	case "7d":
		return 7 * 24 * time.Hour, true
	case "10d":
		return 10 * 24 * time.Hour, true
	case "30d":
		return 30 * 24 * time.Hour, true
	case "all":
		return 0, true
	default:
		return 0, false
	}
}

// DiscoveryMinActivity returns the requested cutoff for timestamp-indexed
// discovery sources. Transcript families still need parsing to determine activity.
func DiscoveryMinActivity(plan *hubclient.V4ImportPlan, now time.Time) int64 {
	if plan == nil || plan.History {
		return 0
	}
	duration, ok := windowDuration(plan.Window)
	if !ok || duration == 0 {
		return 0
	}
	return now.Add(-duration).UnixMilli()
}

func inWindow(entry Entry, plan hubclient.V4ImportPlan, now time.Time) bool {
	duration, ok := windowDuration(plan.Window)
	return ok && (duration == 0 || entry.Activity >= now.Add(-duration).UnixMilli())
}

func inScope(entry Entry, plan hubclient.V4ImportPlan, now time.Time) bool {
	return !entry.Excluded && (inWindow(entry, plan, now) || plan.History || entry.Priority)
}

func (q *Queue) PlannedEntries(plan hubclient.V4ImportPlan, now time.Time) []Entry {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.plannedEntriesLocked(plan, now)
}

func (q *Queue) plannedEntriesLocked(plan hubclient.V4ImportPlan, now time.Time) []Entry {
	var entries []Entry
	for _, entry := range q.state.Entries {
		if inScope(entry, plan, now) {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Priority != b.Priority {
			return a.Priority
		}
		aw, bw := inWindow(a, plan, now), inWindow(b, plan, now)
		if aw != bw {
			return aw
		}
		if a.Activity != b.Activity {
			return a.Activity > b.Activity
		}
		return a.Key < b.Key
	})
	if !plan.History && (plan.MaxSessions > 0 || plan.MaxSessionsPerAgent > 0) {
		selected := make([]Entry, 0, len(entries))
		byAgent := make(map[string]int)
		for _, entry := range entries {
			if plan.MaxSessionsPerAgent > 0 && byAgent[entry.Session.Agent] >= int(plan.MaxSessionsPerAgent) {
				continue
			}
			selected = append(selected, entry)
			byAgent[entry.Session.Agent]++
			if plan.MaxSessions > 0 && len(selected) >= int(plan.MaxSessions) {
				break
			}
		}
		entries = selected
	}
	return entries
}

func (q *Queue) SetPhase(phase string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.state.Phase == phase {
		return nil
	}
	prior := q.state.Phase
	q.state.Phase = phase
	if err := q.save(); err != nil {
		q.state.Phase = prior
		return err
	}
	return nil
}

func (q *Queue) Phase() (string, time.Time, float64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.state.Phase, time.UnixMilli(q.state.PlanStartedAt), q.state.RateBytesSec
}

func (q *Queue) MarkListed(results []hubclient.V4ListResult) error {
	return q.MarkListedAt(results, time.Now())
}

func (q *Queue) MarkListedAt(results []hubclient.V4ListResult, now time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	byKey := make(map[string]hubclient.V4ListResult, len(results))
	for _, result := range results {
		switch result.State {
		case "listed", "existing", "left_out", "rejected":
		default:
			return errors.New("unknown v4 listing state")
		}
		byKey[result.LocalKeyHash] = result
	}
	prior := q.state
	prior.Entries = append([]Entry(nil), q.state.Entries...)
	prior.DiscardBundles = append([]string(nil), q.state.DiscardBundles...)
	for i := range q.state.Entries {
		entry := &q.state.Entries[i]
		result, ok := byKey[entry.Key]
		if !ok {
			continue
		}
		switch result.State {
		case "listed", "existing":
			entry.Listed = true
			entry.ListRejected, entry.FailureCode = false, ""
			if result.SessionID != "" {
				entry.SessionID = result.SessionID
			}
		case "left_out":
			entry.Excluded, entry.ServerLeftOut, entry.Priority = true, true, false
			q.exclude(entry)
		case "rejected":
			entry.FailureCode = result.Code
			if entry.FailureCode == "" {
				entry.FailureCode = "server_error"
			}
			entry.ListRejected = true
		}
	}
	q.state.LastProgressAt = now.UnixMilli()
	if err := q.save(); err != nil {
		q.state = prior
		return err
	}
	return nil
}

func (q *Queue) Prioritize(sessionID string) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.state.Entries {
		entry := &q.state.Entries[i]
		if entry.SessionID != sessionID || entry.Excluded {
			continue
		}
		prior := entry.Priority
		entry.Priority = true
		if err := q.save(); err != nil {
			entry.Priority = prior
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func (q *Queue) ImportSnapshot(now time.Time) ImportSnapshot {
	q.mu.Lock()
	defer q.mu.Unlock()
	snapshot := ImportSnapshot{Phase: q.state.Phase, RateBytesSec: q.state.RateBytesSec, RateSamples: q.state.RateSamples,
		Rates:      append([]RateSample(nil), q.state.Rates...),
		CurrentKey: q.currentKey, CurrentBytesDone: q.currentBytesDone, CurrentBytesTotal: q.currentBytesTotal}
	if q.state.LastProgressAt > 0 {
		at := time.UnixMilli(q.state.LastProgressAt).UTC()
		snapshot.LastProgressAt = &at
	}
	if q.state.HistoryCursorAt > 0 {
		at := time.UnixMilli(q.state.HistoryCursorAt).UTC()
		snapshot.HistoryCursorAt = &at
	}
	plan := q.state.Config.ImportPlan
	if plan == nil {
		snapshot.Phase = "awaiting_plan"
		return snapshot
	}
	snapshot.PlanVersion = plan.Version
	for _, entry := range q.plannedEntriesLocked(*plan, now) {
		if entry.Key == q.currentKey {
			snapshot.CurrentSessionID = entry.SessionID
		}
		snapshot.TotalSessions++
		snapshot.TotalBytes += max(0, entry.ContentBytes)
		if entry.Listed || entry.RevisionID != "" {
			snapshot.Listed++
		}
		if !pending(entry) {
			snapshot.ContentSessions++
			snapshot.ContentBytes += max(0, entry.ContentBytes)
		}
	}
	return snapshot
}

func (q *Queue) SetCurrent(key string, done, total int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.currentKey, q.currentBytesDone, q.currentBytesTotal = key, done, total
}

func (q *Queue) RecordRate(bytes int64, elapsed time.Duration) error {
	return q.RecordRateAt(bytes, elapsed, time.Now())
}

func (q *Queue) RecordRateAt(bytes int64, elapsed time.Duration, now time.Time) error {
	if bytes <= 0 || elapsed <= 0 {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	priorRate, priorSamples, priorRates := q.state.RateBytesSec, q.state.RateSamples, append([]RateSample(nil), q.state.Rates...)
	rate := float64(bytes) / elapsed.Seconds()
	if priorSamples == 0 {
		q.state.RateBytesSec = rate
	} else {
		q.state.RateBytesSec = 0.7*priorRate + 0.3*rate
	}
	q.state.RateSamples++
	q.state.Rates = append(q.state.Rates, RateSample{At: now.UnixMilli(), BytesPerSecond: rate})
	if len(q.state.Rates) > 12 {
		q.state.Rates = q.state.Rates[len(q.state.Rates)-12:]
	}
	if err := q.save(); err != nil {
		q.state.RateBytesSec, q.state.RateSamples = priorRate, priorSamples
		q.state.Rates = priorRates
		return err
	}
	return nil
}
