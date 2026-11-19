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
		_, records, complete, err := manager.SearchRecords()
		if err != nil || complete || len(records) != 0 {
			t.Fatalf("%s exposed records: %v %v", state, complete, err)
		}
	}
	manager.state = StateOK
	source, records, complete, err := manager.SearchRecords()
	if err != nil || !complete || len(records) == 0 || source != manager.cfg.ID {
		t.Fatalf("complete view: %s %d %v %v", source, len(records), complete, err)
	}
	records[0].Record.SessionID = "mutated"
	if manager.snapshot.FullRecords[0].Record.SessionID == "mutated" {
		t.Fatal("shared mutable record")
	}
	manager.familyStale = map[remoteSessionKey]bool{{Agent: records[0].Record.Agent, ID: manager.snapshot.FullRecords[0].Record.SessionID}: true}
	_, staleRecords, staleComplete, staleErr := manager.SearchRecords()
	if staleErr != nil || staleComplete || len(staleRecords) != 0 {
		t.Fatal("stale family must not become authoritative absence")
	}
	manager.familyStale = nil
	manager.snapshot.CoverageSinceMs = 1
	_, _, complete, err = manager.SearchRecords()
	if err != nil || complete {
		t.Fatal("accepted range-limited view")
	}
}
