package syncv4

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/pi"
	backup "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

// piRevisionCandidates applies the existing planner to a private metadata view.
// A failed source check must not replace the real queue's last verified capture.
func (q *Queue) piRevisionCandidates(found []Entry) []Entry {
	q.mu.Lock()
	defer q.mu.Unlock()
	view := Queue{state: q.state, activeSince: q.activeSince}
	view.state.Entries = slices.Clone(q.state.Entries)
	index := map[string]int{}
	for i, entry := range view.state.Entries {
		index[entry.Key] = i
	}
	for _, entry := range found {
		if i, ok := index[entry.Key]; ok {
			stored := &view.state.Entries[i]
			stored.Session, stored.Live = entry.Session, entry.Live
			stored.Activity = max(stored.Activity, entry.Activity)
		} else {
			index[entry.Key] = len(view.state.Entries)
			view.state.Entries = append(view.state.Entries, entry)
		}
	}
	for i := range view.state.Entries {
		entry := &view.state.Entries[i]
		entry.Excluded = entry.ServerLeftOut || leftOut(entry.Session, view.state.Config.LeaveOut)
	}
	entries := view.state.Entries
	if configured := view.state.Config.ImportPlan; configured != nil {
		plan := *configured
		view.state.Config.ImportPlan = &plan
		if view.state.CatchUpFrozenVersion == plan.Version {
			// A new admission must not displace a source the real queue can resume.
			for _, entry := range view.plannedEntriesLocked(plan) {
				view.state.Entries[index[entry.Key]].ChangedPlanVersion = plan.Version
			}
		}
		if view.state.LiveOnlyPlanVersion == plan.Version && !plan.History && !plan.Backfill {
			active := view.activeSinceLocked(time.UnixMilli(view.state.PlanStartedAt)).UnixMilli()
			for i := range view.state.Entries {
				entry := &view.state.Entries[i]
				if entry.Activity >= active || entry.RevisionID != "" || entry.SyncedSourceRevision != "" {
					entry.ChangedPlanVersion = plan.Version
				}
			}
		} else {
			if err := view.freezeCatchUpLocked(plan, false); err != nil {
				return nil
			}
		}
		entries = view.plannedEntriesLocked(plan)
	}
	var selected []Entry
	for _, entry := range entries {
		if entry.Selection.Agent == vendors.AgentPi && !entry.Excluded {
			selected = append(selected, entry)
		}
	}
	return selected
}

func piSourceProblem(id, code string) error {
	return &sessionbackupproducer.PreparationError{Coverage: sessionbackupproducer.Coverage{Problems: []backup.CaptureProblem{{Code: code, MemberID: id, Kind: backup.KindRawTranscript, Retryable: true}}}}
}

func (r *Runner) syncPiSources(ctx context.Context, plan *pi.SourcePlan, found []Entry, verified map[string]string, batchInputs []pi.SessionInputs) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stored := map[string]Entry{}
	current := make([]Entry, 0, len(found))
	for _, entry := range r.Queue.Entries() {
		stored[entry.Key] = entry
		if entry.Selection.Agent == vendors.AgentPi {
			entry.Live = false
			if plan != nil {
				if metadata := plan.Metadata.Lookup(entry.Selection.SessionID); metadata != nil {
					entry.Live = metadata.Live == "busy" || metadata.Live == "idle" || metadata.Live == "waiting"
				}
			}
			current = append(current, entry)
		}
	}
	current = append(current, found...)
	foundIDs := map[string]bool{}
	inputsByID := map[string]pi.SessionInputs{}
	for _, inputs := range batchInputs {
		inputsByID[inputs.ID] = inputs
	}
	for _, entry := range found {
		foundIDs[entry.Selection.SessionID] = true
	}
	var updates []Entry
	var firstErr error
	for _, entry := range r.Queue.piRevisionCandidates(current) {
		id := entry.Selection.SessionID
		revision, seen := verified[id]
		if seen && (!foundIDs[id] || revision == "") {
			continue
		}
		if err := r.allowed(ctx, entry.Session); err != nil {
			return err
		}
		var inputs pi.SessionInputs
		var err error
		if supplied, ok := inputsByID[id]; ok {
			inputs = supplied
		} else if plan == nil {
			err = piSourceProblem(id, backup.ProblemUnreadable)
		} else {
			inputs, err = plan.SessionInputsContext(ctx, id)
			if err != nil && ctx.Err() == nil {
				err = piSourceProblem(id, backup.ProblemUnreadable)
			}
		}
		_, supplied := inputsByID[id]
		if err == nil && (!foundIDs[id] || !supplied) && !seen {
			files := make(map[string]string, len(inputs.Files))
			for _, path := range inputs.Files {
				files[path] = path
			}
			item, _, parseErr := pi.ParseFrozenSessionContext(ctx, inputs, files)
			err = parseErr
			if err != nil && ctx.Err() == nil {
				_, err = sessionbackupproducer.PiSourceRevision(ctx, inputs)
				if err == nil {
					err = piSourceProblem(id, backup.ProblemUnstable)
				}
			}
			if err == nil {
				cards, composeErr := collector.FinalizeBatchContext(ctx, []*vendors.ParsedSession{item}, map[string]*vendors.SessionMetadata{vendors.AgentPi: plan.Metadata})
				err = composeErr
				if err == nil && len(cards) == 1 {
					candidate := discoveredEntries(cards, r.Queue.InstallID(), nil, nil, map[string]string{id: "pending"})
					if len(candidate) == 1 {
						entry.Session, entry.Live = candidate[0].Session, candidate[0].Live
						entry.Activity = candidate[0].Activity
					}
				} else if err == nil {
					err = piSourceProblem(id, backup.ProblemUnattributable)
				}
			}
		}
		if err == nil && leftOut(entry.Session, r.config.LeaveOut) {
			if old, known := stored[entry.Key]; known {
				old.Session, old.Excluded = entry.Session, true
				if err := r.Queue.Update(old); err != nil {
					return err
				}
			}
			continue
		}
		if err == nil {
			eligible := false
			for _, selected := range r.Queue.piRevisionCandidates([]Entry{entry}) {
				eligible = eligible || selected.Key == entry.Key
			}
			if !eligible {
				continue
			}
		}
		if err == nil && !seen {
			revision, err = sessionbackupproducer.PiSourceRevision(ctx, inputs)
		}
		if err != nil || revision == "" {
			if stopSync(err) || ctx.Err() != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return err
			}
			if err == nil {
				err = piSourceProblem(id, backup.ProblemUnreadable)
			}
			verified[id] = ""
			if old, known := stored[entry.Key]; known {
				if err := r.recordFailureState(&old, err, true); err != nil {
					return err
				}
			}
			firstErr = errors.Join(firstErr, err)
			continue
		}
		verified[id] = revision
		if old, known := stored[entry.Key]; known {
			if old.SourceRevision == revision && (old.SourceBlocked || !reflect.DeepEqual(old.Session, entry.Session) || old.Live != entry.Live) {
				old.Session, old.Live = entry.Session, entry.Live
				old.Excluded = old.ServerLeftOut || leftOut(old.Session, r.config.LeaveOut)
				if old.SourceBlocked {
					old.SourceBlocked = false
					old.ParkedVersion, old.FailureCode = "", ""
					old.BackoffAttempt, old.RetryAt = 0, time.Time{}
				}
				old.Listed = false
				if err := r.Queue.Update(old); err != nil {
					return err
				}
			}
			entry.SourceRevision, entry.SourceBlocked = revision, false
			updates = append(updates, entry)
		}
	}
	if len(updates) > 0 {
		if err := r.Queue.Merge(updates, r.now()); err != nil {
			return err
		}
	}
	return firstErr
}

func planPiSources(ctx context.Context, queue *Queue) (*pi.SourcePlan, error) {
	if !vendors.PiCollectionSupported() {
		return nil, nil
	}
	needed := false
	for _, entry := range queue.Entries() {
		needed = needed || entry.Selection.Agent == vendors.AgentPi && !entry.Excluded
	}
	if !needed {
		return nil, nil
	}
	plan, err := pi.PlanSourcesContext(ctx)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		// An incomplete plan permits healthy vendors without proving Pi absence.
		plan = &pi.SourcePlan{Scan: &vendors.SourceScan{SkippedTotal: 1}, Metadata: vendors.EmptySessionMetadata()}
	}
	return plan, nil
}

func piDiscoveryEntries(sessions []*session.Session, installID string) []Entry {
	refs := map[string]string{}
	for _, item := range sessions {
		if item != nil && item.Agent == vendors.AgentPi {
			refs[item.ID] = "pending"
		}
	}
	var entries []Entry
	for _, entry := range discoveredEntries(sessions, installID, nil, nil, refs) {
		if entry.Selection.Agent == vendors.AgentPi {
			entries = append(entries, entry)
		}
	}
	return entries
}
