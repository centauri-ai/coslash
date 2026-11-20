package syncv4

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/session"
)

func locationEligible(e Entry) bool {
	return e.Selection.SourceID == "local" && e.SessionID != "" && (e.Listed || e.RevisionID != "") && !e.Excluded && !e.SourceBlocked && !e.ListRejected && e.FailureCode != "session_deleted"
}

func locationListing(e Entry, l session.Location, version int64) hubclient.V4ListItem {
	meta := e.Session
	meta.Repo = l.Repo
	meta.CWDLabel = cwdLabel(l.CWD)
	item := hubclient.V4ListItem{V4Session: meta, ActivityAt: time.UnixMilli(e.Activity).UTC(), ContentBytes: max(0, e.ContentBytes), LocationVersion: version, MainCheckout: l.Main, Location: &hubclient.V4Location{CWD: l.CWD, Root: l.Root, Source: "Local"}}
	if l.ID != "" {
		if !l.Main {
			label := strings.TrimSpace(strings.ReplaceAll(l.Label, "\\", "_"))
			if label == "" || label == "." || label == ".." {
				label = "Checkout " + l.ID[:min(8, len(l.ID))]
			}
			item.Worktree = &hubclient.V4Worktree{ID: l.ID, Label: label}
		}
		item.Location.Evidence = "Git worktree"
		item.Location.Availability = l.Availability
		item.Location.Head = truncate(strings.TrimSpace(l.HeadKind+" "+l.Head), 255)
		item.Location.ObservedAt = time.UnixMilli(l.LastSeen).UTC().Format(time.RFC3339Nano)
	}
	return item
}

// Existing personal sessions receive location changes independently of content or import-plan age.
func (r *Runner) syncLocations(ctx context.Context) error {
	if !r.Queue.HubAdvertises(hubclient.CapabilityWorktreeLabels) {
		return nil
	}
	lister, ok := r.Hub.(listingTransport)
	if !ok {
		return errors.New("v4 listing transport unavailable")
	}
	entries := r.Queue.Entries()
	if r.lastLocationProbe.IsZero() || r.now().Sub(r.lastLocationProbe) >= 5*time.Minute {
		var cards []*session.Session
		for _, e := range entries {
			if e.LocationCWD != "" && locationEligible(e) && !leftOut(e.Session, r.config.LeaveOut) {
				cards = append(cards, &session.Session{ID: e.Selection.SessionID, Agent: e.Selection.Agent, WorkingDirectory: e.LocationCWD, LastActivityTime: e.Activity})
			}
		}
		if err := session.RefreshLocalLocations(ctx, cards, nil); err != nil {
			return err
		}
		r.lastLocationProbe = r.now()
	}
	var rounds [][]hubclient.V4ListItem
	for _, e := range entries {
		if !locationEligible(e) || leftOut(e.Session, r.config.LeaveOut) {
			continue
		}
		p, err := session.StoredLocations(ctx, "local", e.Selection.Agent, e.Selection.SessionID, e.ListedLocationVersion)
		if err != nil {
			return err
		}
		slices.Reverse(p.Locations)
		for i, l := range p.Locations {
			version := p.Version - int64(len(p.Locations)-i-1)
			if version <= e.ListedLocationVersion {
				continue
			}
			item := locationListing(e, l, version)
			if leftOut(item.V4Session, r.config.LeaveOut) {
				continue
			}
			for len(rounds) <= i {
				rounds = append(rounds, nil)
			}
			rounds[i] = append(rounds[i], item)
		}
	}
	for _, round := range rounds {
		for chunk := range slices.Chunk(round, 50) {
			if err := r.ensureConsent(ctx); err != nil {
				return err
			}
			if !r.Queue.HubAdvertises(hubclient.CapabilityWorktreeLabels) || r.config.ImportPlan == nil {
				return nil
			}
			batch := r.Queue.eligibleLocationItems(chunk)
			if len(batch) == 0 {
				continue
			}
			results, err := listBatchIsolating(ctx, lister, batch)
			if err != nil {
				return err
			}
			if err = r.Queue.recordLocationResults(batch, results, r.now()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (q *Queue) eligibleLocationItems(items []hubclient.V4ListItem) []hubclient.V4ListItem {
	q.mu.Lock()
	defer q.mu.Unlock()
	pending := make(map[string]hubclient.V4ListItem, len(items))
	for _, item := range items {
		pending[item.LocalKeyHash] = item
	}
	batch := make([]hubclient.V4ListItem, 0, len(items))
	for _, e := range q.state.Entries {
		item, ok := pending[e.Key]
		if ok && locationEligible(e) && !leftOut(e.Session, q.state.Config.LeaveOut) && !leftOut(item.V4Session, q.state.Config.LeaveOut) {
			batch = append(batch, item)
		}
	}
	return batch
}

func (q *Queue) recordLocationResults(batch []hubclient.V4ListItem, results []hubclient.V4ListResult, now time.Time) error {
	expected := make(map[string]int64, len(batch))
	for _, item := range batch {
		expected[item.LocalKeyHash] = item.LocationVersion
	}
	acknowledged := make(map[string]int64, len(results))
	var denied []hubclient.V4ListResult
	for _, result := range results {
		version, ok := expected[result.LocalKeyHash]
		if !ok {
			return errors.New("v4 location listing changed item identity")
		}
		delete(expected, result.LocalKeyHash)
		switch result.State {
		case "existing", "listed":
			acknowledged[result.LocalKeyHash] = version
		case "left_out":
			denied = append(denied, result)
		case "rejected":
			if result.Code == "session_deleted" {
				denied = append(denied, result)
			} else {
				return errors.New("v4 location listing rejected")
			}
		default:
			return errors.New("unknown v4 location listing result")
		}
	}
	if len(expected) != 0 {
		return errors.New("v4 location listing omitted item identity")
	}
	if len(denied) > 0 {
		if err := q.MarkListedAt(denied, now); err != nil {
			return err
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	previous := slices.Clone(q.state.Entries)
	for i := range q.state.Entries {
		e := &q.state.Entries[i]
		e.ListedLocationVersion = max(e.ListedLocationVersion, acknowledged[e.Key])
	}
	if err := q.save(); err != nil {
		q.state.Entries = previous
		return err
	}
	return nil
}
