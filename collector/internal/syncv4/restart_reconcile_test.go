package syncv4

import (
	"fmt"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

func TestRestartReconcilesPriorSelectionWithoutImportingOfflineSessions(t *testing.T) {
	started := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	restarted := started.Add(24 * time.Hour)
	plan := hubclient.V4ImportPlan{Version: 2, Window: "3d", MaxSessions: 30, MaxSessionsPerAgent: 30}
	root := t.TempDir()
	queue, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	catchUp := make([]Entry, 0, 35)
	for i := 0; i < 35; i++ {
		catchUp = append(catchUp, catchUpEntry(queue, fmt.Sprintf("catchup-%03d", i), "codex", started.Add(-time.Duration(i+1)*time.Minute)))
	}
	if err := queue.Merge(catchUp, started.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 2, Config: hubclient.V4Config{ImportPlan: &plan}}, started); err != nil {
		t.Fatal(err)
	}
	if err := queue.FreezeCatchUp(plan); err != nil {
		t.Fatal(err)
	}
	live := make([]Entry, 0, 3)
	for i := 0; i < 3; i++ {
		live = append(live, catchUpEntry(queue, fmt.Sprintf("live-%03d", i), "codex", started.Add(time.Duration(i+1)*time.Second)))
	}
	if _, err := queue.MergePlannedDiscovery(live, started.Add(time.Minute), plan, started); err != nil {
		t.Fatal(err)
	}
	selected := queue.PlannedEntries(plan, started.Add(time.Minute))
	if len(selected) != 33 {
		t.Fatalf("initial selection = %d, want 30 capped catch-up plus 3 live", len(selected))
	}
	catchUpCount := 0
	for i, entry := range selected {
		if isCatchUpEntry(entry, plan) {
			catchUpCount++
		}
		entry.Listed = true
		if i == 0 {
			entry.RevisionID = "synthetic-stale-revision"
		} else {
			entry.RevisionID = fmt.Sprintf("synthetic-accepted-%03d", i)
			entry.SyncedActivity = entry.Activity
			entry.SyncedSourceRevision = entry.SourceRevision
		}
		if err := queue.Update(entry); err != nil {
			t.Fatal(err)
		}
	}
	if catchUpCount != 30 {
		t.Fatalf("initial catch-up count = %d, want the 30-session cap", catchUpCount)
	}
	results := make([]hubclient.V4ListResult, 0, len(selected))
	for i, entry := range selected {
		state := "existing"
		if i == 0 {
			state = "listed"
		}
		results = append(results, hubclient.V4ListResult{LocalKeyHash: entry.Key, SessionID: "synthetic-session", State: state})
	}
	if err := queue.MarkListedAt(results, started.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := queue.SetPhase("complete"); err != nil {
		t.Fatal(err)
	}
	queue, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	queue.SetActiveSince(restarted)
	offline := make([]Entry, 0, 653)
	for i := 0; i < 653; i++ {
		offline = append(offline, catchUpEntry(queue, fmt.Sprintf("offline-%03d", i), "codex", started.Add(time.Duration(i+2)*time.Minute)))
	}
	if _, err := queue.MergePlannedDiscovery(offline, restarted, plan, restarted); err != nil {
		t.Fatal(err)
	}
	planned := queue.PlannedEntries(plan, restarted)
	var plannedCatchUp, offlineSelected, listRequests, accepted int
	for _, entry := range planned {
		if isCatchUpEntry(entry, plan) {
			plannedCatchUp++
		}
		if len(entry.Key) >= 8 && entry.Key[:8] == "offline-" {
			offlineSelected++
		}
		if !entry.Listed {
			listRequests++
		}
		if entry.RevisionID != "" {
			accepted++
		}
	}
	if len(planned) != 33 || plannedCatchUp != 30 || offlineSelected != 0 || listRequests != 33 || accepted != 32 {
		t.Fatalf("restart plan: selected=%d catch-up=%d offline=%d list-requests=%d accepted=%d; want 33/30/0/33/32",
			len(planned), plannedCatchUp, offlineSelected, listRequests, accepted)
	}

	nextPlan := plan
	nextPlan.Version++
	queue.SetActiveSince(restarted.Add(time.Minute))
	if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 3, Config: hubclient.V4Config{ImportPlan: &nextPlan}}, restarted.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := queue.FreezeCatchUp(nextPlan); err != nil {
		t.Fatal(err)
	}
	versionReplay := queue.PlannedEntries(nextPlan, restarted.Add(time.Minute))
	if len(versionReplay) != 30 {
		t.Fatalf("new plan version replay selected %d sessions, want the 30-session cap", len(versionReplay))
	}
	for _, entry := range versionReplay {
		if !isCatchUpEntry(entry, nextPlan) {
			t.Fatalf("new plan version included a non-catch-up session: %s", entry.Key)
		}
	}
}
