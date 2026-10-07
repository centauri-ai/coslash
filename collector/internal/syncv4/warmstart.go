package syncv4

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

const initialBytesPerSecond = 20_000_000 / 8
const firstWarmMaxBytes = 25 << 20
const plannedContentBudget = 2 * time.Minute
const sourcePreparationBudget = 15 * time.Second

type listingTransport interface {
	V4ListBatch(context.Context, []hubclient.V4ListItem) ([]hubclient.V4ListResult, error)
}

func warmFits(entry Entry, remaining time.Duration, rate float64, first bool) bool {
	if entry.ContentBytes <= 0 || remaining <= 0 || first && entry.ContentBytes > firstWarmMaxBytes {
		return false
	}
	if rate <= 0 {
		rate = initialBytesPerSecond
	}
	return float64(entry.ContentBytes)/rate+1 < remaining.Seconds()
}

func contentOrder(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Priority != entries[j].Priority {
			return entries[i].Priority
		}
		return entries[i].Session.Agent != "cursor" && entries[j].Session.Agent == "cursor"
	})
}

func (r *Runner) runPlanned(ctx context.Context) error {
	plan := r.config.ImportPlan
	if plan == nil || plan.Version < 1 {
		return r.Queue.SetPhase("awaiting_plan")
	}
	if _, ok := windowDuration(plan.Window); !ok {
		return errors.New("invalid import plan window")
	}
	phase, started, rate := r.Queue.Phase()
	if phase == "" || phase == "awaiting_plan" {
		phase = "warm_start"
		if err := r.Queue.SetPhase(phase); err != nil {
			return err
		}
	}
	if phase == "warm_start" {
		budget := time.Duration(plan.WarmStartSeconds) * time.Second
		if budget > 0 && r.now().Sub(started) < budget {
			if err := r.warmStart(ctx, *plan, started, budget, rate); err != nil {
				return err
			}
		}
		if r.now().Sub(started) < budget {
			for _, entry := range r.Queue.PlannedEntries(*plan, r.now()) {
				if inWindow(entry, *plan, r.now()) && entry.UploadID != "" && entry.RevisionID == "" {
					return nil
				}
			}
		}
		if err := r.Queue.SetPhase("listing"); err != nil {
			return err
		}
	}
	if err := r.listAll(ctx, *plan); err != nil {
		return err
	}
	return r.syncPlannedContent(ctx, *plan)
}

func (r *Runner) warmStart(ctx context.Context, plan hubclient.V4ImportPlan, started time.Time, budget time.Duration, rate float64) error {
	first := r.Queue.ImportSnapshot(r.now()).ContentSessions == 0
	attemptedUnknown := map[string]bool{}
	for {
		remaining := budget - r.now().Sub(started)
		if remaining <= 0 {
			return nil
		}
		var pick *Entry
		entries := r.Queue.PlannedEntries(plan, r.now())
		contentOrder(entries)
		for _, entry := range entries {
			if !inWindow(entry, plan, r.now()) || !pending(entry) || entry.ParkedVersion != "" || entry.ListRejected || !retryDue(entry, r.now()) || !readyLive(entry, r.now()) {
				continue
			}
			if entry.ContentBytes <= 0 && !attemptedUnknown[entry.Key] {
				attemptedUnknown[entry.Key] = true
				estimateCtx, cancel := context.WithTimeout(ctx, budget-r.now().Sub(started))
				err := r.estimateContentBytes(estimateCtx, &entry)
				cancel()
				if err != nil {
					if stopSync(err) {
						return err
					}
					if updateErr := r.recordFailure(&entry, err); updateErr != nil {
						return updateErr
					}
					continue
				}
			}
			if warmFits(entry, budget-r.now().Sub(started), rate, first) {
				pick = &entry
				break
			}
		}
		if pick == nil {
			return nil
		}
		start := r.now()
		pickCtx, cancel := context.WithTimeout(ctx, budget-r.now().Sub(started))
		if err := r.ensureCreated(pickCtx, pick); err != nil {
			cancel()
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				return nil
			}
			if stopSync(err) || Busy(err) {
				return err
			}
			if updateErr := r.recordFailure(pick, err); updateErr != nil {
				return updateErr
			}
			continue
		}
		if pick.UploadID != "" {
			if err := r.transfer(pickCtx, pick); err != nil {
				cancel()
				if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
					return nil
				}
				if stopSync(err) || Busy(err) {
					return err
				}
				if updateErr := r.recordFailure(pick, err); updateErr != nil {
					return updateErr
				}
				continue
			}
		}
		cancel()
		if pick.RevisionID == "" {
			return nil
		}
		if err := r.Queue.RecordRateAt(pick.ContentBytes, r.now().Sub(start), r.now()); err != nil {
			return err
		}
		_, _, rate = r.Queue.Phase()
		first = false
	}
}

func (r *Runner) estimateContentBytes(ctx context.Context, entry *Entry) error {
	if entry.BundleID == "" {
		prepared, err := r.Backup.Prepare(ctx, entry.Selection)
		if err != nil {
			return err
		}
		entry.BundleID = prepared.BundleID
	}
	prepared, err := r.Backup.Open(entry.BundleID)
	if err != nil {
		return err
	}
	for _, artifact := range prepared.Manifest.Artifacts {
		entry.ContentBytes += artifact.ByteLength
	}
	return r.Queue.Update(*entry)
}

func readyLive(entry Entry, now time.Time) bool {
	return !entry.Live || now.Sub(time.UnixMilli(entry.ChangedAt)) >= 2*time.Minute
}

// listBatchIsolating sends one listing batch with the retry policy for
// transient Hub problems. When the Hub rejects the whole batch as invalid
// (400 invalid_query), it bisects so one bad item cannot block the other
// items or make the next pass resend the same rejected batch; a single item
// the Hub still refuses is reported back as rejected with code invalid_item.
func listBatchIsolating(ctx context.Context, lister listingTransport, batch []hubclient.V4ListItem) ([]hubclient.V4ListResult, error) {
	var results []hubclient.V4ListResult
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		results, err = lister.V4ListBatch(ctx, batch)
		if err == nil || !retryableChunk(err) {
			break
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * 200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if err == nil {
		return results, nil
	}
	if !invalidListBatch(err) {
		return nil, err
	}
	if len(batch) == 1 {
		return []hubclient.V4ListResult{{LocalKeyHash: batch[0].LocalKeyHash, State: "rejected", Code: "invalid_item"}}, nil
	}
	half := len(batch) / 2
	left, err := listBatchIsolating(ctx, lister, batch[:half])
	if err != nil {
		return nil, err
	}
	right, err := listBatchIsolating(ctx, lister, batch[half:])
	if err != nil {
		return nil, err
	}
	return append(left, right...), nil
}

func invalidListBatch(err error) bool {
	var problem hubclient.V4Problem
	return errors.As(err, &problem) && (problem.Code == "invalid_query" || problem.HTTPStatus == 400)
}

func (r *Runner) listAll(ctx context.Context, plan hubclient.V4ImportPlan) error {
	lister, ok := r.Hub.(listingTransport)
	if !ok {
		return errors.New("v4 listing transport unavailable")
	}
	entries := r.Queue.PlannedEntries(plan, r.now())
	batch := make([]hubclient.V4ListItem, 0, 50)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := r.ensureConsent(ctx); err != nil {
			return err
		}
		if r.config.ImportPlan == nil {
			return ErrPaused
		}
		allowed := make(map[string]bool)
		for _, entry := range r.Queue.PlannedEntries(*r.config.ImportPlan, r.now()) {
			allowed[entry.Key] = true
		}
		kept := batch[:0]
		for _, item := range batch {
			if allowed[item.LocalKeyHash] && !leftOut(item.V4Session, r.config.LeaveOut) {
				kept = append(kept, item)
			}
		}
		batch = kept
		if len(batch) == 0 {
			return nil
		}
		results, err := listBatchIsolating(ctx, lister, batch)
		if err != nil {
			return err
		}
		if len(results) != len(batch) {
			return errors.New("v4 listing returned incomplete results")
		}
		expected := make(map[string]bool, len(batch))
		for _, item := range batch {
			expected[item.LocalKeyHash] = true
		}
		for _, item := range results {
			if !expected[item.LocalKeyHash] {
				return errors.New("v4 listing changed item identity")
			}
			delete(expected, item.LocalKeyHash)
		}
		if len(expected) != 0 {
			return errors.New("v4 listing omitted item identity")
		}
		batch = batch[:0]
		return r.Queue.MarkListedAt(results, r.now())
	}
	for _, entry := range entries {
		if entry.Listed || entry.RevisionID != "" || entry.Excluded || entry.ListRejected {
			continue
		}
		activity := time.UnixMilli(entry.Activity).UTC()
		batch = append(batch, hubclient.V4ListItem{V4Session: entry.Session, ActivityAt: activity, ContentBytes: max(0, entry.ContentBytes)})
		if len(batch) == cap(batch) {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

func (r *Runner) syncPlannedContent(ctx context.Context, plan hubclient.V4ImportPlan) error {
	started := r.now()
	workCtx, cancel := context.WithTimeout(ctx, plannedContentBudget)
	defer cancel()
	entries := r.Queue.PlannedEntries(plan, r.now())
	var firstErr error
	// Reconcile uploads already accepted by the Hub before opening more. A
	// finalize can complete between passes even when new creates are refused.
	for _, entry := range entries {
		if r.now().Sub(started) >= plannedContentBudget || workCtx.Err() != nil {
			return firstErr
		}
		if entry.UploadID == "" || !pending(entry) || entry.ParkedVersion != "" || entry.ListRejected || !retryDue(entry, r.now()) || !readyLive(entry, r.now()) {
			continue
		}
		if err := r.transfer(workCtx, &entry); err != nil {
			if workCtx.Err() != nil && ctx.Err() == nil {
				entry.FailureCode = "server_error"
				entry.BackoffAttempt++
				entry.RetryAt = r.now().Add(retryBackoff(entry.BackoffAttempt))
				if updateErr := r.failed(&entry, err); updateErr != nil {
					return updateErr
				}
				return firstErr
			}
			if stopSync(err) {
				return err
			}
			if firstErr == nil {
				firstErr = err
			}
			if updateErr := r.recordFailure(&entry, err); updateErr != nil {
				return updateErr
			}
		}
	}
	entries = r.Queue.PlannedEntries(plan, r.now())
	contentOrder(entries)
	for _, entry := range entries {
		if r.now().Sub(started) >= plannedContentBudget || workCtx.Err() != nil {
			return firstErr
		}
		if entry.UploadID != "" || !pending(entry) || entry.ParkedVersion != "" || entry.ListRejected || !retryDue(entry, r.now()) || !readyLive(entry, r.now()) || !entry.Priority && !inWindow(entry, plan, r.now()) && plan.HistoryPaused {
			continue
		}
		phase := "recent"
		if !inWindow(entry, plan, r.now()) {
			phase = "history"
		}
		if err := r.Queue.SetPhase(phase); err != nil {
			return err
		}
		err := r.ensureConsent(workCtx)
		if err == nil {
			err = r.entryAllowed(entry)
		}
		if err == nil && entry.UploadID == "" && entry.RevisionID == "" {
			prepareCtx, stopPrepare := context.WithTimeout(workCtx, sourcePreparationBudget)
			err = r.prepareEntry(prepareCtx, &entry)
			sourceTimedOut := prepareCtx.Err() == context.DeadlineExceeded
			stopPrepare()
			if sourceTimedOut && workCtx.Err() == nil && ctx.Err() == nil {
				entry.FailureCode = "unreadable_source"
				entry.BackoffAttempt++
				entry.RetryAt = r.now().Add(retryBackoff(entry.BackoffAttempt))
				if updateErr := r.failed(&entry, err); updateErr != nil {
					return updateErr
				}
				continue
			}
			if err == nil {
				err = r.createUpload(workCtx, &entry)
			}
		}
		if err == nil && entry.UploadID != "" {
			err = r.transfer(workCtx, &entry)
			if err == nil && entry.RevisionID != "" {
				entry.Priority = false
				if err = r.Queue.Update(entry); err != nil {
					return err
				}
			}
		} else if err == nil {
			continue
		}
		if err != nil {
			if workCtx.Err() != nil && ctx.Err() == nil {
				entry.FailureCode = "server_error"
				entry.BackoffAttempt++
				entry.RetryAt = r.now().Add(retryBackoff(entry.BackoffAttempt))
				if updateErr := r.failed(&entry, err); updateErr != nil {
					return updateErr
				}
				return firstErr
			}
			remoteTimeout := errors.Is(err, context.DeadlineExceeded) && workCtx.Err() == nil && ctx.Err() == nil
			if (stopSync(err) && !remoteTimeout) || Busy(err) {
				return err
			}
			if firstErr == nil {
				firstErr = err
			}
			if updateErr := r.recordFailure(&entry, err); updateErr != nil {
				return updateErr
			}
		}
	}
	if firstErr != nil {
		return firstErr
	}
	snapshot := r.Queue.ImportSnapshot(r.now())
	if snapshot.ContentSessions == snapshot.TotalSessions {
		return r.Queue.SetPhase("complete")
	}
	if plan.HistoryPaused {
		return r.Queue.SetPhase("paused")
	}
	return nil
}
