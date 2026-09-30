package pi

import (
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
func inheritedEntries(t, parent *transcript) (map[string]bool, bool) {
	inherited := map[string]bool{}
	if t.Header.ParentSession == "" {
		return inherited, true
	}
	if parent == nil || canonicalPath(t.Header.ParentSession) != canonicalPath(parent.Path) || t.Header.ID == parent.Header.ID || parent.Incomplete {
		return inherited, false
	}
	matched, novel := false, false
	for _, e := range t.Entries {
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
		if json.Unmarshal(e.Raw, &a) != nil || json.Unmarshal(old.Raw, &b) != nil {
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
		oldParent, oldOK := withoutLabels(parent, old.ParentID)
		newParent, newOK := withoutLabels(t, e.ParentID)
		if !oldOK || !newOK || oldParent != newParent {
			return inherited, false
		}

		inherited[e.ID], matched = true, true
	}
	return inherited, matched
}

func withoutLabels(t *transcript, id *string) (string, bool) {
	seen := map[string]bool{}
	for id != nil && *id != "" {
		if seen[*id] {
			return "", false
		}
		seen[*id] = true
		i, ok := t.ByID[*id]
		if !ok {
			return "", false
		}
		e := t.Entries[i]
		if e.Type != "label" {
			return *id, true
		}
		id = e.ParentID
	}
	return "", true
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
			if _, ok := inheritedEntries(current, parent); !ok {
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
