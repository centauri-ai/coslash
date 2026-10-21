package grok

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// IsSourceFile accepts only parser inputs relative to one session directory.
func IsSourceFile(path string) bool {
	switch filepath.ToSlash(path) {
	case "summary.json", "updates.jsonl", "signals.json", "usage.json", "plan.json", "plan.md", "plan_mode.json", "prompt_context.json", "goal/state.json":
		return true
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	return len(parts) == 2 && parts[0] == "compaction_checkpoints" && strings.HasSuffix(parts[1], ".json") ||
		len(parts) == 3 && parts[0] == "subagents" && parts[1] != "" && parts[1] != "." && parts[1] != ".." && (parts[2] == "meta.json" || parts[2] == "output.json")
}

// FamilyRef is a verified family, with its root directory first and children
// in deterministic depth-first order. ActivityMs is summary-derived activity.
type FamilyRef struct {
	ID         string
	Dirs       []string
	ActivityMs int64
}

// PlanFamiliesContext indexes only the supplied directories, once per pass.
// Duplicate IDs, conflicting parents and cycles fail rather than choose a source.
func PlanFamiliesContext(ctx context.Context, dirs []string) ([]FamilyRef, error) {
	refs, _, err := planFamiliesContext(ctx, dirs)
	return refs, err
}

func planFamiliesContext(ctx context.Context, dirs []string) ([]FamilyRef, map[string]string, error) {
	byID := map[string]string{}
	summaries := map[string]*summaryFile{}
	seenDirs := map[string]bool{}
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		dir = filepath.Clean(dir)
		if seenDirs[dir] {
			continue
		}
		seenDirs[dir] = true
		summary, err := readSummary(dir)
		if err != nil {
			return nil, nil, errors.New("grok: invalid session summary")
		}
		if summary.ChatFormatVersion != 1 || summary.Info.ID == "" {
			continue
		}
		id := summary.Info.ID
		if byID[id] != "" {
			return nil, nil, errors.New("grok: ambiguous session identity")
		}
		byID[id], summaries[id] = dir, summary
	}
	parentOf := map[string]string{}
	for id, summary := range summaries {
		if isSubagentKind(summary.SessionKind) && summary.ParentSessionID != "" {
			parentOf[id] = summary.ParentSessionID
		}
	}
	for id, dir := range byID {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		for _, meta := range readSubagentMetas(ctx, dir, false) {
			child := summaries[meta.ChildSessionID]
			if child == nil || !isSubagentKind(child.SessionKind) {
				continue
			}
			if parent := parentOf[meta.ChildSessionID]; parent != "" && parent != id {
				return nil, nil, errors.New("grok: ambiguous parent lineage")
			}
			parentOf[meta.ChildSessionID] = id
		}
	}
	children := map[string][]string{}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
		if parent := parentOf[id]; parent != "" {
			if byID[parent] == "" {
				return nil, nil, errors.New("grok: missing family parent")
			}
			children[parent] = append(children[parent], id)
		}
	}
	sort.Strings(ids)
	for id := range children {
		sort.Strings(children[id])
	}
	refs := []FamilyRef{}
	visited := map[string]bool{}
	for _, id := range ids {
		if parentOf[id] != "" {
			continue
		}
		ref := FamilyRef{ID: id}
		stack := []string{id}
		for len(stack) > 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			member := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			visited[member] = true
			ref.Dirs = append(ref.Dirs, byID[member])
			summary := summaries[member]
			ref.ActivityMs = max(ref.ActivityMs, parseTime(summary.LastActiveAt), parseTime(summary.UpdatedAt), parseTime(summary.CreatedAt))
			for i := len(children[member]) - 1; i >= 0; i-- {
				stack = append(stack, children[member][i])
			}
		}
		refs = append(refs, ref)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if len(visited) != len(byID) {
		return nil, nil, errors.New("grok: cyclic parent lineage")
	}
	return refs, parentOf, nil
}

// ParseDirsContext parses selected whole families without scanning the source root.
func ParseDirsContext(ctx context.Context, dirs []string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	refs, parents, err := planFamiliesContext(ctx, dirs)
	if err != nil {
		return nil, nil, err
	}
	parsed := []*vendors.ParsedSession{}
	metas := map[string][]subagentMeta{}
	for _, ref := range refs {
		for _, dir := range ref.Dirs {
			item, err := parseSessionContext(ctx, dir)
			if err != nil {
				return nil, nil, err
			}
			if item == nil {
				return nil, nil, errors.New("grok: session changed during discovery")
			}
			item.ParentID = parents[item.Session.ID]
			for _, meta := range readSubagentMetas(ctx, dir, true) {
				if parents[meta.ChildSessionID] == item.Session.ID {
					metas[dir] = append(metas[dir], meta)
				}
			}
			parsed = append(parsed, item)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	attachSubagents(parsed, metas)
	metadata, err := loadMetadataContext(ctx)
	return parsed, metadata, err
}
