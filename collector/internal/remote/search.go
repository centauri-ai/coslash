package remote

import (
	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
)

type SearchRecord struct {
	Record     fullsessionv1.Record
	Repository string
}

// SearchView is the live view's full records. When Complete, a session active
// at or after SinceMs that is missing from Records no longer exists.
type SearchView struct {
	SourceID string
	Records  []SearchRecord
	SinceMs  int64
	Complete bool
}

// SearchRecords returns records only from a complete, live view, over whatever
// history window the list already fetched; it never widens that window.
// Snapshot records are immutable, so the view shares them.
func (manager *Manager) SearchRecords() SearchView {
	manager.mu.Lock()
	if manager.cfg == nil || !manager.cfg.Enabled || manager.snapshot == nil {
		view := SearchView{}
		if manager.cfg != nil {
			view.SourceID = manager.cfg.ID
		}
		manager.mu.Unlock()
		return view
	}
	view := SearchView{SourceID: manager.cfg.ID, SinceMs: manager.snapshot.CoverageSinceMs}
	health := manager.healthLocked(view.SinceMs)
	if !health.Complete || health.State != StateOK || manager.legacyStale {
		manager.mu.Unlock()
		return view
	}
	for _, stale := range manager.familyStale {
		if stale {
			manager.mu.Unlock()
			return view
		}
	}
	records := make([]SearchRecord, 0, len(manager.snapshot.FullRecords))
	repositories := make(map[remoteSessionKey]string, len(manager.sessions))
	for _, value := range manager.sessions {
		if value.Repository != nil {
			repositories[remoteSessionKey{Agent: value.Agent, ID: value.ID}] = *value.Repository
		}
	}
	for _, full := range manager.snapshot.FullRecords {
		records = append(records, SearchRecord{Record: full.Record, Repository: repositories[remoteSessionKey{Agent: full.Record.Agent, ID: full.Record.SessionID}]})
	}
	manager.mu.Unlock()
	view.Records, view.Complete = records, true
	return view
}
