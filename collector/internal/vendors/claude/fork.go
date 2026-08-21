package claude

import (
	"log"

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

func strictSubset(a, b map[string]struct{}) bool {
	if len(a) >= len(b) {
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
