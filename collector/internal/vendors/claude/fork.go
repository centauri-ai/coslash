package claude

import (
	"log"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// Fork assigns each usage-bearing message.id shared by more than one root
// transcript to the file furthest upstream in the fork lineage — forking
// copies the parent's rows verbatim, usage included — then subtracts the
// non-owned ids' usage from each file's Tokens. Ownership resolution is
// deliberately conservative: an unresolvable tie leaves full usage on both
// sides rather than dropping tokens.
//
// Upstream priority: containment (a fork's ids strictly contain its parent's),
// then file birthtime, then id count, then a tie yielding no owner.
func applyForkedUsage(parsed []*parsedSession) {
	applyForkedUsageSource(vendors.LocalReadSource, parsed)
}

func applyForkedUsageSource(source vendors.ReadSource, parsed []*parsedSession) {
	type entry struct {
		p     *parsedSession
		usage map[string]messageUsage
		ids   map[string]struct{}
		birth int64
	}
	entries := []*entry{}
	for _, p := range parsed {
		if p.transcript.ParentID != "" {
			continue // children are never fork-adjusted
		}
		usage := p.forkUsage
		if len(usage) == 0 {
			continue
		}
		ids := make(map[string]struct{}, len(usage))
		for id := range usage {
			ids[id] = struct{}{}
		}
		entries = append(entries, &entry{
			p: p, usage: usage, ids: ids, birth: fileCreationTime(source, p.transcript.LogPath),
		})
	}

	type candidate struct {
		path  string
		ids   map[string]struct{}
		birth int64
		count int
		tie   bool // another file ties the owner on every signal
	}
	upstream := func(e *entry, c *candidate) int {
		switch {
		case strictSubset(e.ids, c.ids):
			return -1
		case strictSubset(c.ids, e.ids):
			return 1
		case e.birth != c.birth:
			if e.birth < c.birth {
				return -1
			}
			return 1
		case len(e.ids) != len(c.ids):
			if len(e.ids) < len(c.ids) {
				return -1
			}
			return 1
		default:
			return 0
		}
	}
	owners := map[string]*candidate{}
	for _, e := range entries {
		for id := range e.ids {
			c, ok := owners[id]
			if !ok {
				owners[id] = &candidate{
					path: e.p.transcript.LogPath, ids: e.ids, birth: e.birth, count: 1,
				}
				continue
			}
			c.count++
			switch upstream(e, c) {
			case -1:
				c.path, c.ids, c.birth, c.tie = e.p.transcript.LogPath, e.ids, e.birth, false
			case 0:
				c.tie = true
			}
		}
	}

	for _, e := range entries {
		for id, message := range e.usage {
			c, ok := owners[id]
			if !ok || c.count < 2 || c.tie || c.path == e.p.transcript.LogPath {
				continue
			}
			// Inherited from a forked-from sibling: counted under the owner.
			usage := message.usage
			tokens := e.p.transcript.Session.Tokens[message.model]
			tokens.InputTokens -= usage.InputTokens
			tokens.OutputTokens -= usage.OutputTokens
			tokens.CacheReadInputTokens -= usage.CacheReadInputTokens
			tokens.CacheCreation1hInputTokens -= usage.CacheWriteInputTokens.Ephemeral1h
			tokens.CacheCreationInputTokens -= usage.CacheWriteInputTokens.Ephemeral5m +
				usage.untieredCacheCreation()
			e.p.transcript.Session.Tokens[message.model] = tokens
		}
	}
}

func strictSubset[V any](a, b map[string]V) bool {
	return len(a) < len(b) && subset(a, b)
}

func subset[V any](a, b map[string]V) bool {
	if len(a) > len(b) {
		return false
	}
	for id := range a {
		if _, ok := b[id]; !ok {
			return false
		}
	}
	return true
}

func fileCreationTime(source vendors.ReadSource, path string) int64 {
	info, err := source.Stat(path)
	if err != nil {
		log.Printf("%s: stat for fork ordering failed: %v", path, err)
		return 0
	}
	if created, ok := birthtime(info); ok {
		return created
	}
	return info.ModTime().UnixMilli()
}

// collapseBackgroundRehomes drops a root that a background re-home superseded
func collapseBackgroundRehomes(parsed []*parsedSession) []*parsedSession {
	roots := []*parsedSession{}
	for _, p := range parsed {
		if p.transcript.ParentID == "" && len(p.rowUUIDs) > 0 {
			roots = append(roots, p)
		}
	}
	// Each re-home supersedes only the root it copied
	supersededBy := map[string]*parsedSession{}
	for _, successor := range roots {
		if !successor.background {
			continue
		}
		var predecessor *parsedSession
		for _, candidate := range roots {
			if !supersedes(candidate, successor) {
				continue
			}
			if predecessor == nil || later(candidate, predecessor) {
				predecessor = candidate
			}
		}
		if predecessor == nil {
			continue
		}
		id := predecessor.transcript.Session.ID
		if prior, ok := supersededBy[id]; !ok || later(successor, prior) {
			supersededBy[id] = successor
		}
	}

	survivorOf := func(p *parsedSession) *parsedSession {
		for {
			next, ok := supersededBy[p.transcript.Session.ID]
			if !ok {
				return p
			}
			p = next
		}
	}

	survivors := make([]*parsedSession, 0, len(parsed))
	for _, p := range parsed {
		if successor, ok := supersededBy[p.transcript.Session.ID]; ok {
			final := survivorOf(successor)
			log.Printf("%s: superseded by background re-home %s",
				p.transcript.Session.ID, final.transcript.Session.ID)
			foldTokens(final.transcript.Session, p.transcript.Session)
			continue
		}
		// Subagent transcripts stay keyed to the predecessor's uuid on disk,
		// so composeSessions orphans them unless they follow the survivor.
		if successor, ok := supersededBy[p.transcript.ParentID]; ok {
			p.transcript.ParentID = survivorOf(successor).transcript.Session.ID
		}
		survivors = append(survivors, p)
	}
	return survivors
}

func supersedes(predecessor, successor *parsedSession) bool {
	return subset(predecessor.rowUUIDs, successor.rowUUIDs) &&
		later(successor, predecessor)
}

// later orders two files by how much of the session they hold: conversation
func later(a, b *parsedSession) bool {
	if len(a.rowUUIDs) != len(b.rowUUIDs) {
		return len(a.rowUUIDs) > len(b.rowUUIDs)
	}
	return a.rowCount > b.rowCount
}

func foldTokens(into, from *session.Session) {
	for model, add := range from.Tokens {
		total := into.Tokens[model]
		total.InputTokens += add.InputTokens
		total.OutputTokens += add.OutputTokens
		total.CacheReadInputTokens += add.CacheReadInputTokens
		total.CacheCreationInputTokens += add.CacheCreationInputTokens
		total.CacheCreation1hInputTokens += add.CacheCreation1hInputTokens
		into.Tokens[model] = total
	}
}
