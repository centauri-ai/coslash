package syncv4

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

func TestImportProgressProjectsSchedulerSnapshotWithoutIdentity(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	queue := &Queue{}
	plan := &hubclient.V4ImportPlan{Version: 7, Window: "all"}
	queue.state.Config.ImportPlan = plan
	queue.state.Phase = "recent"
	queue.state.LastProgressAt = now.Add(-time.Second).UnixMilli()
	queue.state.HistoryCursorAt = now.Add(-time.Hour).UnixMilli()
	queue.state.Entries = []Entry{{Key: "private-key", Activity: now.UnixMilli(), SyncedActivity: now.UnixMilli(),
		ContentBytes: 100, Listed: true, RevisionID: "rev_accepted"}}
	queue.currentKey, queue.currentBytesDone, queue.currentBytesTotal = "private-key", 40, 100
	for i, value := range []float64{60, 10, 50, 20, 40, 30} {
		queue.state.Rates = append(queue.state.Rates, RateSample{At: now.Add(time.Duration(i-5) * 30 * time.Second).UnixMilli(), BytesPerSecond: value})
	}
	runner := &Runner{Queue: queue, config: queue.state.Config, Now: func() time.Time { return now },
		Conditions: func(_ context.Context) (bool, int, error) { return true, -1, nil }}
	progress := runner.importProgress(t.Context(), now)
	if progress.Phase != "recent" || progress.PlanVersion != 7 || progress.Listed != 1 || progress.ContentSessions != 1 ||
		progress.ContentBytes != 100 || progress.Current == nil || progress.Current.BytesDone != 40 ||
		progress.Rate == nil || progress.Rate.P25 != 22.5 || progress.Rate.P75 != 47.5 || progress.Rate.WindowSec != 150 ||
		progress.Wait == nil || progress.Wait.Reason != "metered_network" || progress.LastProgressAt == nil || progress.HistoryCursorAt == nil {
		t.Fatalf("import progress = %+v", progress)
	}
	encoded, err := json.Marshal(progress)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-key") {
		t.Fatalf("private queue identity escaped: %s", encoded)
	}
}

func TestImportProgressReportsFilesDuringInventoryScan(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	runner := &Runner{Queue: &Queue{}, Now: func() time.Time { return now },
		InventoryProgress: func() (int64, bool) { return 37, true }}
	progress := runner.importProgress(t.Context(), now)
	if progress.Phase != "inventory" || progress.Listed != 37 || progress.PlanVersion != 0 || !runner.ImportActive() {
		t.Fatalf("inventory progress = %+v", progress)
	}
}
