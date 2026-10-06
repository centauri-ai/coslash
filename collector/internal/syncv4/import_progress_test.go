package syncv4

import (
	"context"
	"encoding/json"
	"errors"
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
	queue.state.PlanStartedAt = now.Add(-time.Hour).UnixMilli()
	queue.state.Phase = "recent"
	queue.state.LastProgressAt = now.Add(-time.Second).UnixMilli()
	queue.state.HistoryCursorAt = now.Add(-time.Hour).UnixMilli()
	queue.state.Entries = []Entry{{Key: "private-key", SessionID: "ses_hub_id", Activity: now.UnixMilli(), SyncedActivity: now.UnixMilli(),
		ContentBytes: 100, Listed: true, RevisionID: "rev_accepted"}}
	queue.currentKey, queue.currentBytesDone, queue.currentBytesTotal = "private-key", 40, 100
	for i, value := range []float64{60, 10, 50, 20, 40, 30} {
		queue.state.Rates = append(queue.state.Rates, RateSample{At: now.Add(time.Duration(i-5) * 30 * time.Second).UnixMilli(), BytesPerSecond: value})
	}
	runner := &Runner{Queue: queue, config: queue.state.Config, Now: func() time.Time { return now },
		Conditions: func(_ context.Context) (bool, int, error) { return true, -1, nil }}
	progress := runner.importProgress(t.Context(), now)
	if progress.Phase != "recent" || progress.PlanVersion != 7 || progress.Listed != 1 || progress.ContentSessions != 1 ||
		progress.ContentBytes != 100 || progress.Current == nil || progress.Current.BytesDone != 40 || progress.Current.SessionID != "ses_hub_id" ||
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

func TestImportProgressReportsSchedulerQueuePositions(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	queue := &Queue{}
	queue.state.Config.ImportPlan = &hubclient.V4ImportPlan{Version: 7, Window: "all"}
	queue.state.PlanStartedAt = now.Add(-2 * time.Hour).UnixMilli()
	queue.state.Entries = []Entry{
		{Key: "older-private-key", SessionID: "ses_older", Activity: now.Add(-time.Hour).UnixMilli(), Listed: true, Priority: true},
		{Key: "newer-private-key", SessionID: "ses_newer", Activity: now.UnixMilli(), Listed: true},
		{Key: "parked-private-key", SessionID: "ses_parked", Activity: now.Add(time.Hour).UnixMilli(), Listed: true, ParkedVersion: "0.0.5"},
	}
	runner := &Runner{Queue: queue, config: queue.state.Config, Now: func() time.Time { return now }}
	progress := runner.importProgress(t.Context(), now)
	if len(progress.QueuePositions) != 2 || progress.QueuePositions["ses_older"] != 1 || progress.QueuePositions["ses_newer"] != 2 {
		t.Fatalf("queue positions = %+v", progress.QueuePositions)
	}
	encoded, err := json.Marshal(progress)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-key") {
		t.Fatalf("private queue key escaped: %s", encoded)
	}
}

func TestProgressCheckInCoalescesDuringActiveImport(t *testing.T) {
	t.Setenv("COSLASH_SCALE_IMPORT", "1")
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "all"}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1,
		Config: hubclient.V4Config{ImportPlan: plan}, Capabilities: []string{hubclient.CapabilityScaleImport}}, now); err != nil {
		t.Fatal(err)
	}
	if err := queue.SetPhase("recent"); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.StartCommand("cmd_progress"); err != nil {
		t.Fatal(err)
	}
	if err := queue.SetCommandProgress("cmd_progress", hubclient.V4CommandProgress{Stage: "uploading", BytesDone: 4, BytesTotal: 8}); err != nil {
		t.Fatal(err)
	}
	hub := &planHub{plan: plan}
	runner := &Runner{Queue: queue, Hub: hub, Now: func() time.Time { return now }}
	for _, test := range []struct {
		advance time.Duration
		want    int
	}{{0, 1}, {5 * time.Second, 1}, {3 * time.Second, 2}} {
		now = now.Add(test.advance)
		changed, retry, err := runner.ProgressCheckIn(t.Context())
		if err != nil || changed || retry != 0 || hub.checks != test.want {
			t.Fatalf("after %s: changed=%v retry=%s err=%v checks=%d", test.advance, changed, retry, err, hub.checks)
		}
		if hub.checks > 0 && (len(hub.results) != 1 || hub.results[0].Progress == nil || hub.results[0].Progress.Stage != "uploading") {
			t.Fatalf("heartbeat command results = %+v", hub.results)
		}
	}
}

func TestProgressCheckInSharesHubRateLimitWithConsent(t *testing.T) {
	t.Setenv("COSLASH_SCALE_IMPORT", "1")
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "all"}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1,
		Config: hubclient.V4Config{ImportPlan: plan}, Capabilities: []string{hubclient.CapabilityScaleImport}}, now); err != nil {
		t.Fatal(err)
	}
	if err := queue.SetPhase("recent"); err != nil {
		t.Fatal(err)
	}
	hub := &planHub{plan: plan, checkInErr: hubclient.V4Problem{Code: "rate_limited", RetryAfter: time.Minute}}
	runner := &Runner{Queue: queue, Hub: hub, Now: func() time.Time { return now }}
	_, retry, err := runner.ProgressCheckIn(t.Context())
	if err == nil || retry != time.Minute || hub.checks != 1 {
		t.Fatalf("first rate limit: retry=%s err=%v checks=%d", retry, err, hub.checks)
	}
	hub.checkInErr = nil
	now = now.Add(10 * time.Second)
	_, retry, err = runner.ProgressCheckIn(t.Context())
	if err == nil || retry != 50*time.Second || hub.checks != 1 {
		t.Fatalf("early heartbeat: retry=%s err=%v checks=%d", retry, err, hub.checks)
	}
	if err := runner.refreshConsent(t.Context()); !errors.Is(err, ErrStaleConsent) || hub.checks != 1 {
		t.Fatalf("early consent: err=%v checks=%d", err, hub.checks)
	}
	now = now.Add(50 * time.Second)
	if _, _, err := runner.ProgressCheckIn(t.Context()); err != nil || hub.checks != 2 {
		t.Fatalf("after rate limit: err=%v checks=%d", err, hub.checks)
	}
}
