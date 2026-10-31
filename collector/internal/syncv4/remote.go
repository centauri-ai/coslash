package syncv4

import (
	"sort"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

type sshImport struct {
	StartedAt   int64             `json:"startedAt"`
	PlanVersion int64             `json:"planVersion"`
	Selected    map[string]string `json:"selected"` // upload key to agent
}

// RemoteEntries admits verified complete roots, never stale display-only cards.
func RemoteEntries(view remote.ListResult, installID, home string) []Entry {
	var entries []Entry
	for _, indexed := range view.Sessions {
		item := indexed.Session
		if item == nil || indexed.Key.SourceID == "" || item.ParentSessionID != "" || indexed.DisplayStale || indexed.RevisionID == "" || indexed.FamilyRevision == "" ||
			(item.Agent != "codex" && item.Agent != "claude") {
			continue
		}
		local := discoveredEntries([]*session.Session{item}, installID, nil, nil, nil)
		if len(local) != 1 {
			continue
		}
		entry := local[0]
		entry.Key = localKey(indexed.Key.SourceID, item.Agent, item.ID)
		entry.Session.LocalKeyHash = entry.Key
		if relative, ok := strings.CutPrefix(item.WorkingDirectory, strings.TrimRight(home, "/")+"/"); home != "" && ok {
			entry.Session.CWDLabel = truncate("~/"+relative, 500)
		}
		entry.Selection = sessionbackupproducer.Selection{SourceKind: sessionbackupv1.SourceSSH, SourceID: indexed.Key.SourceID, Agent: item.Agent, SessionID: item.ID}
		entry.SourceRevision = indexed.FamilyRevision
		entries = append(entries, entry)
	}
	return entries
}

// The Hub key of an uploaded mirror stays stable when its verified SSH origin
// replaces local capture, preserving the session ID and its existing history.
func (q *Queue) reconcileMirrors(found []Entry) []Entry {
	bySource := make(map[sessionbackupproducer.Selection]Entry)
	byKey := make(map[string]Entry, len(q.state.Entries))
	sshClaude := make(map[string]bool)
	for _, entry := range q.state.Entries {
		byKey[entry.Key] = entry
		if entry.Selection.SourceKind == sessionbackupv1.SourceSSH {
			bySource[entry.Selection] = entry
			if entry.Selection.Agent == "claude" {
				sshClaude[entry.Selection.SessionID] = true
			}
		}
	}
	result := make([]Entry, 0, len(found))
	for _, entry := range found {
		if entry.Selection.SourceKind != sessionbackupv1.SourceSSH {
			continue
		}
		if previous, ok := bySource[entry.Selection]; ok {
			entry.Key = previous.Key
			entry.LocalMirror = entry.LocalMirror || previous.LocalMirror
		} else if entry.LocalMirror && entry.Selection.Agent == "claude" {
			key := localKey("local", "claude", entry.Selection.SessionID)
			if previous, ok := byKey[key]; !ok || previous.Selection.SourceKind == sessionbackupv1.SourceLocal {
				entry.Key = key
			}
		}
		entry.Session.LocalKeyHash = entry.Key
		if entry.Selection.Agent == "claude" {
			sshClaude[entry.Selection.SessionID] = true
		}
		byKey[entry.Key] = entry
		result = append(result, entry)
	}
	for _, entry := range found {
		if entry.Selection.SourceKind == sessionbackupv1.SourceSSH {
			continue
		}
		if entry.Selection.SourceKind == sessionbackupv1.SourceLocal && entry.Selection.Agent == "claude" {
			if entry.LocalMirror && sshClaude[entry.Selection.SessionID] {
				continue
			}
			if previous, ok := byKey[entry.Key]; ok && previous.Selection.SourceKind == sessionbackupv1.SourceSSH {
				entry.Key = localKey("local-native", "claude", entry.Selection.SessionID)
				entry.Session.LocalKeyHash = entry.Key
			}
		}
		result = append(result, entry)
	}
	return result
}

// A newly connected source needs its own bounded first selection even when the
// relay has completed catch-up. This does not reopen the relay's local history.
func (q *Queue) initialSSHImport(found []Entry, now time.Time, plan *hubclient.V4ImportPlan) []Entry {
	if plan == nil {
		return found
	}
	known := make(map[string]Entry, len(q.state.Entries))
	for _, entry := range q.state.Entries {
		known[entry.Key] = entry
	}
	ordered := append([]Entry(nil), found...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Activity > ordered[j].Activity })
	result := make([]Entry, 0, len(ordered))
	for _, entry := range ordered {
		if entry.Selection.SourceKind != sessionbackupv1.SourceSSH {
			result = append(result, entry)
			continue
		}
		if q.state.SSHImports == nil {
			q.state.SSHImports = map[string]sshImport{}
		}
		source, ok := q.state.SSHImports[entry.Selection.SourceID]
		if !ok {
			source = sshImport{StartedAt: now.UnixMilli(), PlanVersion: plan.Version, Selected: map[string]string{}}
		}
		q.state.SSHImports[entry.Selection.SourceID] = source
		if plan.Backfill || plan.History {
			result = append(result, entry)
			continue
		}
		previous, exists := known[entry.Key]
		if exists && entry.LocalMirror && previous.Selection.SourceKind == sessionbackupv1.SourceLocal &&
			(previous.SyncedActivity > 0 || previous.RevisionID != "" || q.inPlanScopeLocked(previous, *plan, time.UnixMilli(q.state.PlanStartedAt))) {
			entry.ChangedPlanVersion = plan.Version
			result = append(result, entry)
			continue
		}
		if exists && previous.Selection.SourceKind == sessionbackupv1.SourceSSH || source.PlanVersion != plan.Version || entry.Activity > source.StartedAt {
			result = append(result, entry)
			continue
		}
		byAgent := 0
		for _, agent := range source.Selected {
			if agent == entry.Selection.Agent {
				byAgent++
			}
		}
		limit := plan.MaxSessionsPerAgent
		if limit == 0 {
			limit = 30
		}
		if !inWindow(entry, *plan, time.UnixMilli(source.StartedAt)) ||
			plan.MaxSessions > 0 && int64(len(source.Selected)) >= plan.MaxSessions || int64(byAgent) >= limit {
			continue
		}
		source.Selected[entry.Key] = entry.Selection.Agent
		entry.ChangedPlanVersion = plan.Version
		result = append(result, entry)
	}
	return result
}
