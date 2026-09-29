package syncv4

import (
	"context"
	"errors"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

// importProgress projects the scheduler's private snapshot onto the content-free
// check-in contract. In particular, CurrentKey is only a local queue identity.
func (r *Runner) importProgress(ctx context.Context, now time.Time) *hubclient.V4Import {
	snapshot := r.Queue.ImportSnapshot(now)
	progress := &hubclient.V4Import{
		PlanVersion: snapshot.PlanVersion, Phase: snapshot.Phase,
		Listed: snapshot.Listed, ContentSessions: snapshot.ContentSessions,
		ContentBytes: snapshot.ContentBytes, TotalSessions: snapshot.TotalSessions,
		TotalBytes: snapshot.TotalBytes, LastProgressAt: snapshot.LastProgressAt,
		HistoryCursorAt: snapshot.HistoryCursorAt, Wait: r.currentWait(ctx),
	}
	if progress.Phase == "" {
		progress.Phase = "awaiting_plan"
	}
	if r.InventoryProgress != nil {
		files, running := r.InventoryProgress()
		if running && progress.PlanVersion == 0 {
			progress.Phase = "inventory"
			progress.Listed = max(0, files)
		}
	}
	if snapshot.CurrentKey != "" {
		progress.Current = &hubclient.V4ImportCurrent{
			BytesDone:  max(0, snapshot.CurrentBytesDone),
			BytesTotal: max(0, snapshot.CurrentBytesTotal),
		}
	}
	if len(snapshot.Rates) >= 6 {
		last := snapshot.Rates[len(snapshot.Rates)-1].At
		if last > 0 && !time.UnixMilli(last).After(now) && now.Sub(time.UnixMilli(last)) <= 2*time.Minute {
			values := make([]float64, 0, len(snapshot.Rates))
			for _, sample := range snapshot.Rates {
				if sample.At > 0 && sample.At <= last {
					values = append(values, sample.BytesPerSecond)
				}
			}
			progress.Rate = importRate(values, time.Duration(last-snapshot.Rates[0].At)*time.Millisecond)
		}
	}
	return progress
}

func activeImportPhase(phase string) bool {
	switch phase {
	case "inventory", "warm_start", "listing", "recent", "history":
		return true
	default:
		return false
	}
}

// ImportActive is safe for the outer loop to call while a sync pass runs.
func (r *Runner) ImportActive() bool {
	if r.InventoryProgress != nil {
		_, running := r.InventoryProgress()
		if running {
			return true
		}
	}
	return r.Queue != nil && activeImportPhase(r.Queue.ImportSnapshot(r.now()).Phase)
}

func (r *Runner) runPlannedAndReport(ctx context.Context) error {
	err := r.runPlanned(ctx)
	if (err == nil || errors.Is(err, ErrPaused)) && ctx.Err() == nil &&
		r.Queue.ImportSnapshot(r.now()).Phase != r.lastReportedPhase {
		return errors.Join(err, r.refreshConsent(ctx))
	}
	return err
}
