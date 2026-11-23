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
		view, err := manager.SearchRecords()
		if err != nil || view.Complete || len(view.Records) != 0 {
			t.Fatalf("%s exposed records: %+v %v", state, view, err)
		}
	}
	manager.state = StateOK
	view, err := manager.SearchRecords()
	if err != nil || !view.Complete || len(view.Records) == 0 || view.SourceID != manager.cfg.ID {
		t.Fatalf("complete view: %+v %v", view, err)
	}
	records := view.Records
	records[0].Record.SessionID = "mutated"
	if manager.snapshot.FullRecords[0].Record.SessionID == "mutated" {
		t.Fatal("shared mutable record")
	}
	manager.familyStale = map[remoteSessionKey]bool{{Agent: records[0].Record.Agent, ID: manager.snapshot.FullRecords[0].Record.SessionID}: true}
	stale, err := manager.SearchRecords()
	if err != nil || stale.Complete || len(stale.Records) != 0 {
		t.Fatal("stale family must not become authoritative absence")
	}
	manager.familyStale = nil
	manager.snapshot.CoverageSinceMs = 1
	manager.lastRequestedMs = 7
	ranged, err := manager.SearchRecords()
	if err != nil || !ranged.Complete || ranged.SinceMs != 1 {
		t.Fatalf("range-limited view must bound deletion, not block indexing: %+v %v", ranged, err)
	}
	if manager.lastRequestedMs != 7 || manager.refreshing {
		t.Fatal("search widened the requested history window")
	}
}
