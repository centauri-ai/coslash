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

type searchService struct {
	index   *searchpkg.Index
	trigger chan struct{}
	cancel  context.CancelFunc
	done    chan struct{}
}

func startSearchService(mgr *synthesis.Manager, remotes *remote.Manager) *searchService {
	service := &searchService{trigger: make(chan struct{}, 1), done: make(chan struct{})}
	index, err := searchpkg.Open(filepath.Join(settings.Home(), "search", "index.sqlite"))
	if err != nil {
		log.Printf("open search index: %v", err)
		close(service.done)
		return service
	}
	service.index = index
	ctx, cancel := context.WithCancel(context.Background())
	service.cancel = cancel
	go func() {
		defer close(service.done)
		defer index.Close()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		last := time.Time{}
		for {
			if last.IsZero() || time.Since(last) >= 30*time.Second {
				if err := refreshSearchIndex(ctx, index, mgr, remotes); err != nil && ctx.Err() == nil {
					log.Printf("refresh search index: %v", err)
					index.SetState("unavailable", 0)
				}
				last = time.Now()
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-service.trigger:
			}
		}
	}()
	return service
}

func (s *searchService) notify() {
	if s == nil || s.index == nil {
		return
	}
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

func (s *searchService) close() {
	if s == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	<-s.done
}

func refreshSearchIndex(ctx context.Context, index *searchpkg.Index, mgr *synthesis.Manager, remotes *remote.Manager) error {
	sessions, err := listSessions(ctx, 0)
	if err != nil {
		return err
	}
	identities := make([]searchpkg.Identity, 0, len(sessions))
	for _, original := range sessions {
		if err := ctx.Err(); err != nil {
			return err
		}
		value := *original
		if mgr != nil {
			value.Synthesis = mgr.Lookup(value.Agent, value.ID, synthesis.Revision(&value))
		}
		coverage := "full"
		if session.SearchTextPartial(value.Digest) {
			coverage = "partial"
		}
		if err := index.Upsert(ctx, localSourceID, value, coverage); err != nil {
			return err
		}
		identities = append(identities, searchpkg.Identity{SourceID: localSourceID, Agent: value.Agent, SessionID: value.ID})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := index.Reconcile(ctx, localSourceID, identities); err != nil {
		return err
	}
	total := len(identities)
	sources := []string{localSourceID}
	if remotes != nil {
		remotes.ListView(0)
		source, records, complete, err := remotes.SearchRecords()
		if err != nil {
			return err
		}
		if source != "" {
			sources = append(sources, source)
		}
		if complete {
			remoteIDs := make([]searchpkg.Identity, 0, len(records))
			for _, entry := range records {
				record := entry.Record
				value, err := fullsessionrecord.ToSession(record)
				if err != nil {
					return err
				}
				value.Synthesis = nil
				value.DetailRevision = record.RevisionID
				if entry.Repository != "" {
					repo := entry.Repository
					value.Repository = &repo
				}
				if err := index.Upsert(ctx, source, *value, "record"); err != nil {
					return err
				}
				remoteIDs = append(remoteIDs, searchpkg.Identity{SourceID: source, Agent: value.Agent, SessionID: value.ID})
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := index.Reconcile(ctx, source, remoteIDs); err != nil {
				return err
			}
			total += len(remoteIDs)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := index.ReconcileSources(ctx, sources); err != nil {
		return err
	}
	index.SetState("ready", total)
	return nil
}
