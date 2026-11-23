package remote

import (
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"testing"
)

func TestSearchRecordsRequiresCompleteLiveView(t *testing.T) {
	manager := NewManager(Options{Cache: NewCache(t.TempDir())})
	t.Cleanup(manager.Shutdown)
	snapshot := completeCodexSnapshot(t, "generation", "body\n")
	snapshot.CoverageSinceMs = 0
	manager.cfg = &settings.RemoteSettings{ID: snapshot.FullRecords[0].Record.SourceID, Enabled: true}
	manager.snapshot = &snapshot
	for _, state := range []State{StateStale, StateLimited, StateConnecting, StateError} {
		manager.state = state
		if view := manager.SearchRecords(); view.Complete || len(view.Records) != 0 {
			t.Fatalf("%s exposed records: %+v", state, view)
		}
	}
	manager.state = StateOK
	view := manager.SearchRecords()
	if !view.Complete || len(view.Records) == 0 || view.SourceID != manager.cfg.ID {
		t.Fatalf("complete view: %+v", view)
	}
	record := view.Records[0].Record
	manager.familyStale = map[remoteSessionKey]bool{{Agent: record.Agent, ID: record.SessionID}: true}
	if stale := manager.SearchRecords(); stale.Complete || len(stale.Records) != 0 {
		t.Fatal("stale family must not become authoritative absence")
	}
	manager.familyStale = nil
	manager.snapshot.CoverageSinceMs = 1
	manager.lastRequestedMs = 7
	if ranged := manager.SearchRecords(); !ranged.Complete || ranged.SinceMs != 1 {
		t.Fatalf("range-limited view must bound deletion, not block indexing: %+v", ranged)
	}
	if manager.lastRequestedMs != 7 || manager.refreshing {
		t.Fatal("search widened the requested history window")
	}
}
