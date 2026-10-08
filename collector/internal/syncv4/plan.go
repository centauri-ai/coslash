package syncv4

import (
	"errors"
	"slices"
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

func inWindow(entry Entry, plan hubclient.V4ImportPlan, now time.Time) bool {
	duration, ok := windowDuration(plan.Window)
	return ok && (duration == 0 || entry.Activity >= now.Add(-duration).UnixMilli())
}

func inScope(entry Entry, plan hubclient.V4ImportPlan, now time.Time) bool {
	if entry.Excluded || now.IsZero() {
		return false
	}
	if _, ok := windowDuration(plan.Window); !ok {
		return false
	}
	if plan.MaxSessionsPerAgent < 0 || plan.MaxSessionsPerAgent > 500 {
		return false
	}
	return isCatchUpEntry(entry, plan) || entry.ChangedPlanVersion == plan.Version ||
		entry.Activity >= now.UnixMilli() || plan.History || entry.Priority
}

func isCatchUpEntry(entry Entry, plan hubclient.V4ImportPlan) bool {
	return entry.CatchUpPlanVersion == plan.Version
}

func planRecent(entry Entry, plan hubclient.V4ImportPlan, startedAt time.Time) bool {
	return !startedAt.IsZero() && (isCatchUpEntry(entry, plan) || entry.ChangedPlanVersion == plan.Version || entry.Activity >= startedAt.UnixMilli())
}

func (q *Queue) PlannedEntries(plan hubclient.V4ImportPlan, _ time.Time) []Entry {
	q.mu.Lock()
	defer q.mu.Unlock()
	startedAt := time.Time{}
	if q.state.PlanStartedAt > 0 {
		startedAt = time.UnixMilli(q.state.PlanStartedAt)
	}
	if q.state.PlanStartedAt <= 0 || q.state.Config.ImportPlan == nil || q.state.Config.ImportPlan.Version != plan.Version {
		return nil
	}
	var entries []Entry
	for _, entry := range q.state.Entries {
		if inScope(entry, plan, startedAt) {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Priority != b.Priority {
			return a.Priority
		}
		aw, bw := planRecent(a, plan, startedAt), planRecent(b, plan, startedAt)
		if aw != bw {
			return aw
		}
		if a.Activity != b.Activity {
			return a.Activity > b.Activity
		}
		return a.Key < b.Key
	})
	return entries
}

// FreezeCatchUp persists the bounded set selected for one plan version. The
// timestamp is the plan's start time, so discoveries and live updates that
// arrive later cannot displace a session already selected for catch-up.
func (q *Queue) FreezeCatchUp(plan hubclient.V4ImportPlan) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.state.PlanStartedAt <= 0 || q.state.Config.ImportPlan == nil || q.state.Config.ImportPlan.Version != plan.Version {
		return errors.New("cannot freeze catch-up without the current import plan")
	}
	if _, ok := windowDuration(plan.Window); !ok {
		return errors.New("invalid import plan window")
	}
	limit := plan.MaxSessionsPerAgent
	if limit == 0 {
		limit = 30
	}
	if limit < 1 || limit > 500 {
		return errors.New("invalid max sessions per agent")
	}
	priorEntries := slices.Clone(q.state.Entries)
	priorFrozen := q.state.CatchUpFrozenVersion
	firstSelection := priorFrozen != plan.Version
	changed := firstSelection
	startedAt := time.Time{}
	if q.state.PlanStartedAt > 0 {
		startedAt = time.UnixMilli(q.state.PlanStartedAt)
	}
	duration, _ := windowDuration(plan.Window)
	cutoff := int64(0)
	if duration > 0 {
		cutoff = startedAt.Add(-duration).UnixMilli()
	}
	byAgent := make(map[string][]int)
	selected := make(map[string]int)
	for i := range q.state.Entries {
		entry := &q.state.Entries[i]
		if firstSelection || entry.Excluded {
			changed = changed || entry.CatchUpPlanVersion != 0
			entry.CatchUpPlanVersion = 0
		}
		if entry.Excluded || entry.Activity >= startedAt.UnixMilli() || duration > 0 && entry.Activity < cutoff {
			continue
		}
		agent := entry.Session.Agent
		if agent == "" {
			continue
		}
		if entry.CatchUpPlanVersion == plan.Version {
			selected[agent]++
			continue
		}
		byAgent[agent] = append(byAgent[agent], i)
	}
	for agent, indexes := range byAgent {
		sort.Slice(indexes, func(i, j int) bool {
			a, b := q.state.Entries[indexes[i]], q.state.Entries[indexes[j]]
			if a.Activity != b.Activity {
				return a.Activity > b.Activity
			}
			return a.Key < b.Key
		})
		for _, index := range indexes[:min(max(0, limit-selected[agent]), len(indexes))] {
			q.state.Entries[index].CatchUpPlanVersion = plan.Version
			changed = true
		}
	}
	if !changed {
		return nil
	}
	q.state.CatchUpFrozenVersion = plan.Version
	if err := q.save(); err != nil {
		q.state.Entries = priorEntries
		q.state.CatchUpFrozenVersion = priorFrozen
		return err
	}
	return nil
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
	if q.state.PlanStartedAt <= 0 {
		return snapshot
	}
	startedAt := time.UnixMilli(q.state.PlanStartedAt)
	for _, entry := range q.state.Entries {
		if entry.Key == q.currentKey {
			snapshot.CurrentSessionID = entry.SessionID
		}
		if !inScope(entry, *plan, startedAt) {
			continue
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
