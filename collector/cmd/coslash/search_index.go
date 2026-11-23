package main

import (
	"context"
	"log"
	"path/filepath"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/fullsessionrecord"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	searchpkg "github.com/centauri-ai/coslash/collector/internal/search"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
)

// startSearchIndex refreshes the index every 30 seconds until stop returns.
// A nil index means search is unavailable.
func startSearchIndex(mgr *synthesis.Manager, remotes *remote.Manager) (*searchpkg.Index, func()) {
	index, err := searchpkg.Open(filepath.Join(settings.Home(), "search", "index.sqlite"))
	if err != nil {
		log.Printf("open search index: %v", err)
		return nil, func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer index.Close()
		for ready := false; ; {
			ready = runSearchPass(ctx, index, mgr, remotes, ready)
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
		}
	}()
	return index, func() { cancel(); <-done }
}

// runSearchPass reports whether a pass has ever completed. After one has, a
// failed pass leaves the last-good rows searchable instead of marking search unavailable.
func runSearchPass(ctx context.Context, index *searchpkg.Index, mgr *synthesis.Manager, remotes *remote.Manager, ready bool) bool {
	err := refreshSearchIndex(ctx, index, mgr, remotes)
	if err != nil && ctx.Err() == nil {
		log.Printf("refresh search index: %v", err)
		if !ready {
			index.SetState("unavailable", 0)
		}
	}
	return ready || err == nil
}

func refreshSearchIndex(ctx context.Context, index *searchpkg.Index, mgr *synthesis.Manager, remotes *remote.Manager) error {
	sessions, err := listSessions(ctx, 0)
	if err != nil {
		return err
	}
	// A session that fails to index keeps its last-good rows; only a pass in
	// which every session fails is an error.
	attempted, failed := 0, 0
	var firstErr error
	skip := func(err error) {
		if failed++; firstErr == nil {
			firstErr = err
		}
	}
	upsert := func(sourceID string, value session.Session, coverage string) error {
		attempted++
		err := index.Upsert(ctx, sourceID, value, coverage)
		if err != nil && ctx.Err() == nil {
			skip(err)
			return nil
		}
		return err
	}
	identities := make([]searchpkg.Identity, 0, len(sessions))
	for _, original := range sessions {
		value := *original
		if mgr != nil {
			value.Synthesis = mgr.Lookup(value.Agent, value.ID, synthesis.Revision(&value))
		}
		coverage := "full"
		if session.SearchTextPartial(value.Digest) {
			coverage = "partial"
		}
		identities = append(identities, searchpkg.Identity{SourceID: localSourceID, Agent: value.Agent, SessionID: value.ID})
		if err := upsert(localSourceID, value, coverage); err != nil {
			return err
		}
	}
	if err := index.Reconcile(ctx, localSourceID, identities, 0); err != nil {
		return err
	}
	total := len(identities)
	sources := []string{localSourceID}
	if remotes != nil {
		view := remotes.SearchRecords()
		if view.SourceID != "" {
			sources = append(sources, view.SourceID)
		}
		if view.Complete {
			remoteIDs := make([]searchpkg.Identity, 0, len(view.Records))
			for _, entry := range view.Records {
				record := entry.Record
				remoteIDs = append(remoteIDs, searchpkg.Identity{SourceID: view.SourceID, Agent: record.Agent, SessionID: record.SessionID})
				value, err := fullsessionrecord.ToSession(record)
				if err != nil {
					attempted++
					skip(err)
					continue
				}
				value.Synthesis = nil
				value.DetailRevision = record.RevisionID
				if entry.Repository != "" {
					repo := entry.Repository
					value.Repository = &repo
				}
				if err := upsert(view.SourceID, *value, "record"); err != nil {
					return err
				}
			}
			if err := index.Reconcile(ctx, view.SourceID, remoteIDs, view.SinceMs); err != nil {
				return err
			}
			total += len(remoteIDs)
		}
	}
	if failed > 0 {
		if failed == attempted {
			return firstErr
		}
		log.Printf("search index skipped %d of %d sessions: %v", failed, attempted, firstErr)
	}
	if err := index.ReconcileSources(ctx, sources); err != nil {
		return err
	}
	index.SetState("ready", total)
	return nil
}
