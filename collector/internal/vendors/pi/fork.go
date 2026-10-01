package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"path/filepath"
	"reflect"
)

func canonicalPath(path string) string {
	p, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// Pi preserves non-label identities. A clone may bypass labels in parent links.
func inheritedEntriesContext(ctx context.Context, t, parent *transcript) (map[string]bool, bool) {
	inherited := map[string]bool{}
	if t.Header.ParentSession == "" {
		return inherited, true
	}
	if parent == nil || canonicalPath(t.Header.ParentSession) != canonicalPath(parent.Path) || t.Header.ID == parent.Header.ID || parent.Incomplete {
		return inherited, false
	}
	oldParents, newParents := labelParents(ctx, parent), labelParents(ctx, t)
	decode := func(raw json.RawMessage, value *map[string]any) error {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		return decoder.Decode(value)
	}
	matched, novel := false, false
	for _, e := range t.Entries {
		if ctx.Err() != nil {
			return inherited, false
		}
		if e.Type == "label" {
			continue
		}
		index, exists := parent.ByID[e.ID]
		if !exists {
			novel = true
			continue
		}
		if novel {
			return inherited, false
		}
		old := parent.Entries[index]
		var a, b map[string]any
		if decode(e.Raw, &a) != nil || decode(old.Raw, &b) != nil {
			return inherited, false
		}
		delete(a, "timestamp")
		delete(b, "timestamp")
		delete(a, "parentId")
		delete(b, "parentId")
		// firstKeptEntryId can also bypass removed label entries in a selected clone.
		if e.Type == "compaction" {
			if id, ok := b["firstKeptEntryId"].(string); ok {
				if i, found := parent.ByID[id]; found && parent.Entries[i].Type == "label" {
					for j := i + 1; j < len(parent.Entries); j++ {
						if inherited[parent.Entries[j].ID] || parent.Entries[j].ID == e.ID {
							b["firstKeptEntryId"] = parent.Entries[j].ID
							break
						}
					}
				}
			}
		}
		if !reflect.DeepEqual(a, b) {
			return inherited, false
		}
		// Parent rewrites must only bypass labels, never unrelated conversation entries.
		oldParent, oldOK := oldParents(old.ParentID)
		newParent, newOK := newParents(e.ParentID)
		if !oldOK || !newOK || oldParent != newParent {
			return inherited, false
		}

		inherited[e.ID], matched = true, true
	}
	return inherited, matched
}

// Memoize label-chain resolution so shared ancestors are traversed only once.
func labelParents(ctx context.Context, t *transcript) func(*string) (string, bool) {
	type resolved struct {
		id string
		ok bool
	}
	cache := map[string]resolved{}
	return func(parent *string) (string, bool) {
		if ctx.Err() != nil {
			return "", false
		}
		id := ""
		if parent != nil {
			id = *parent
		}
		path := []string{}
		seen := map[string]bool{}
		result := resolved{ok: true}
		for id != "" {
			if ctx.Err() != nil {
				return "", false
			}
			if previous, found := cache[id]; found {
				result = previous
				break
			}
			if seen[id] {
				result.ok = false
				break
			}
			seen[id] = true
			path = append(path, id)
			i, found := t.ByID[id]
			if !found {
				result.ok = false
				break
			}
			e := t.Entries[i]
			if e.Type != "label" {
				result.id = id
				break
			}
			id = ""
			if e.ParentID != nil {
				id = *e.ParentID
			}
		}
		for _, key := range path {
			if ctx.Err() != nil {
				return "", false
			}
			cache[key] = result
		}
		return result.id, result.ok
	}
}

// Parent files are parsed once per collection, including parents outside discovered roots.
func parentCache(items []*transcript, scans ...*vendors.SourceScan) func(*transcript) *transcript {
	return parentCacheContext(context.Background(), items, scans...)
}
func parentCacheContext(ctx context.Context, items []*transcript, scans ...*vendors.SourceScan) func(*transcript) *transcript {
	cache := map[string]*transcript{}
	for _, t := range items {
		cache[canonicalPath(t.Path)] = t
	}
	blocked := map[string]bool{}
	for _, scan := range scans {
		for _, path := range scan.Files {
			path = canonicalPath(path)
			if _, ok := cache[path]; !ok {
				blocked[path] = true
			}
		}
	}
	load := func(path string) *transcript {
		path = canonicalPath(path)
		if blocked[path] {
			return nil
		}
		if t, ok := cache[path]; ok {
			return t
		}
		t, _ := parseTranscriptContext(ctx, path)
		cache[path] = t
		return t
	}
	return func(t *transcript) *transcript {
		if t.Header.ParentSession == "" {
			return nil
		}
		seen := map[string]bool{canonicalPath(t.Path): true}
		current, immediate := t, (*transcript)(nil)
		for depth := 0; depth < 256; depth++ {
			if ctx.Err() != nil {
				return nil
			}
			if current.Header.ParentSession == "" {
				return immediate
			}
			path := canonicalPath(current.Header.ParentSession)
			if seen[path] {
				return nil
			}
			seen[path] = true
			parent := load(path)
			if parent == nil || parent.Header.ID == current.Header.ID {
				return nil
			}
			if _, ok := inheritedEntriesContext(ctx, current, parent); !ok {
				return nil
			}
			if immediate == nil {
				immediate = parent
			}
			current = parent
		}
		return nil
	}
}
