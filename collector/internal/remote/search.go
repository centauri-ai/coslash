package remote

import (
	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
)

type SearchRecord struct {
	Record     fullsessionv1.Record
	Repository string
}

// SearchRecords returns only records from a complete, live all-time view.
// Snapshot records are immutable; cloning happens after releasing the lock.
func (manager *Manager) SearchRecords() (string, []SearchRecord, bool, error) {
	manager.mu.Lock()
	health := manager.healthLocked(0)
	if manager.cfg == nil || !manager.cfg.Enabled || manager.snapshot == nil ||
		!health.Complete || health.State != StateOK || manager.legacyStale {
		manager.mu.Unlock()
		return health.SourceID, nil, false, nil
	}
	for _, stale := range manager.familyStale {
		if stale {
			manager.mu.Unlock()
			return health.SourceID, nil, false, nil
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
		if !manager.familyStale[remoteSessionKey{Agent: full.Record.Agent, ID: full.Record.SessionID}] {
			records = append(records, SearchRecord{Record: full.Record, Repository: repositories[remoteSessionKey{Agent: full.Record.Agent, ID: full.Record.SessionID}]})
		}
	}
	manager.mu.Unlock()
	for i := range records {
		copy, err := cloneFullSessionRecord(records[i].Record)
		if err != nil {
			return health.SourceID, nil, false, err
		}
		records[i].Record = *copy
	}
	return health.SourceID, records, true, nil
}
