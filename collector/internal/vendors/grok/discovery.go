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
	ID           string
	Dirs         []string
	ActivityMs   int64
	nonChildren  map[string]bool
	nonChildDirs map[string]string
	memberIDs    []string
}

// PlanFamiliesContext indexes only the supplied directories, once per pass.
// An unreadable summary returns an error with any independently verified families.
// Duplicate IDs, conflicting parents and cycles fail rather than choose a source.
func PlanFamiliesContext(ctx context.Context, dirs []string) ([]FamilyRef, error) {
	refs, _, err := planFamiliesContext(ctx, dirs, nil, 0, nil)
	return refs, err
}

// PlanFamiliesContextWithTranscriptLimit avoids lineage recovery scans of
// transcripts beyond the supplied byte limit and reports affected member IDs.
func PlanFamiliesContextWithTranscriptLimit(ctx context.Context, dirs []string, maxTranscriptBytes int64) ([]FamilyRef, map[string]bool, error) {
	oversizedMembers := map[string]bool{}
	refs, _, err := planFamiliesContext(ctx, dirs, nil, maxTranscriptBytes, oversizedMembers)
	return refs, oversizedMembers, err
}

func planFamiliesContext(ctx context.Context, dirs []string, nonChildren map[string]bool, maxTranscriptBytes int64, oversizedMembers map[string]bool) ([]FamilyRef, map[string]string, error) {
	byID := map[string]string{}
	summaries := map[string]*summaryFile{}
	seenDirs := map[string]bool{}
	var summaryErr error
	unreadable := []string{}
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
			summaryErr = errors.New("grok: invalid session summary")
			unreadable = append(unreadable, dir)
			continue
		}
		if summary.ChatFormatVersion != 1 {
			continue
		}
		if summary.Info.ID == "" {
			summaryErr = errors.New("grok: invalid session summary")
			unreadable = append(unreadable, dir)
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
	blocked := map[string]bool{}
	for _, dir := range unreadable {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		metas, missing := readSubagentMetasWithTranscriptLimit(ctx, dir, false, maxTranscriptBytes)
		markOversizedLineage(oversizedMembers, blocked, "", missing, summaries)
		for _, meta := range metas {
			if child := summaries[meta.ChildSessionID]; child != nil && isSubagentKind(child.SessionKind) {
				blocked[meta.ChildSessionID] = true
			}
		}
	}
	excluded := map[string][]string{}
	for id, dir := range byID {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		metas, missing := readSubagentMetasWithTranscriptLimit(ctx, dir, false, maxTranscriptBytes)
		if len(missing) > 0 {
			blocked[id] = true
		}
		markOversizedLineage(oversizedMembers, blocked, id, missing, summaries)
		for _, meta := range metas {
			child := summaries[meta.ChildSessionID]
			if child == nil {
				if nonChildren[meta.ChildSessionID] {
					excluded[id] = append(excluded[id], meta.ChildSessionID)
					continue
				}
				if summaryErr != nil {
					blocked[id] = true
					continue
				}
				return nil, nil, errors.New("grok: missing family child")
			}
			if !isSubagentKind(child.SessionKind) {
				excluded[id] = append(excluded[id], meta.ChildSessionID)
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
				if summaryErr == nil {
					return nil, nil, errors.New("grok: missing family parent")
				}
				blocked[id] = true
				continue
			}
			children[parent] = append(children[parent], id)
		} else if summaryErr != nil && isSubagentKind(summaries[id].SessionKind) {
			// An incomplete scan cannot establish that a parentless subagent is a true orphan.
			blocked[id] = true
		}
	}
	sort.Strings(ids)
	for id := range children {
		sort.Strings(children[id])
	}
	if oversizedMembers != nil {
		stack := make([]string, 0, len(oversizedMembers))
		for id := range oversizedMembers {
			stack = append(stack, id)
		}
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, child := range children[id] {
				if !oversizedMembers[child] {
					oversizedMembers[child] = true
					stack = append(stack, child)
				}
			}
		}
	}
	refs := []FamilyRef{}
	visited := map[string]bool{}
	for _, id := range ids {
		if parent := parentOf[id]; parent != "" && byID[parent] != "" {
			continue
		}
		ref := FamilyRef{ID: id}
		withheld := false
		stack := []string{id}
		for len(stack) > 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			member := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			visited[member] = true
			withheld = withheld || blocked[member]
			for _, excludedID := range excluded[member] {
				if ref.nonChildren == nil {
					ref.nonChildren = map[string]bool{}
				}
				ref.nonChildren[excludedID] = true
				if ref.nonChildDirs == nil {
					ref.nonChildDirs = map[string]string{}
				}
				ref.nonChildDirs[excludedID] = byID[excludedID]
			}
			ref.Dirs = append(ref.Dirs, byID[member])
			ref.memberIDs = append(ref.memberIDs, member)
			summary := summaries[member]
			ref.ActivityMs = max(ref.ActivityMs, parseTime(summary.LastActiveAt), parseTime(summary.UpdatedAt), parseTime(summary.CreatedAt))
			for i := len(children[member]) - 1; i >= 0; i-- {
				stack = append(stack, children[member][i])
			}
		}
		if !withheld {
			refs = append(refs, ref)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if len(visited) != len(byID) {
		return nil, nil, errors.New("grok: cyclic parent lineage")
	}
	return refs, parentOf, summaryErr
}

func markOversizedLineage(oversizedMembers, blocked map[string]bool, parentID string, missing map[string]bool, summaries map[string]*summaryFile) {
	if len(missing) == 0 {
		return
	}
	if oversizedMembers != nil && parentID != "" {
		oversizedMembers[parentID] = true
	}
	for candidate := range missing {
		child := summaries[candidate]
		if child == nil || !isSubagentKind(child.SessionKind) ||
			(parentID != "" && child.ParentSessionID != "" && child.ParentSessionID != parentID) {
			continue
		}
		blocked[candidate] = true
		if oversizedMembers != nil {
			oversizedMembers[child.Info.ID] = true
		}
	}
}

// ParseDirsContext verifies a standalone set of directories without a root scan.
// For batches selected from a larger plan, use ParseFamiliesContext.
func ParseDirsContext(ctx context.Context, dirs []string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	return parseDirsContext(ctx, dirs, nil)
}

// ParseFamiliesContext retains verified non-child references across selected batches.
// Pass the unchanged refs returned by PlanFamiliesContext; this never scans the root.
func ParseFamiliesContext(ctx context.Context, families []FamilyRef) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	dirs := []string{}
	nonChildren := map[string]bool{}
	for _, family := range families {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		dirs = append(dirs, family.Dirs...)
		for id := range family.nonChildren {
			nonChildren[id] = true
		}
	}
	return parseDirsContext(ctx, dirs, nonChildren)
}

func parseDirsContext(ctx context.Context, dirs []string, nonChildren map[string]bool) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	parsed, err := parseFamilyDirsContext(ctx, dirs, nonChildren)
	if err != nil {
		return nil, nil, err
	}
	metadata, err := loadMetadataContext(ctx)
	return parsed, metadata, err
}

func parseFamilyDirsContext(ctx context.Context, dirs []string, nonChildren map[string]bool) ([]*vendors.ParsedSession, error) {
	refs, parents, err := planFamiliesContext(ctx, dirs, nonChildren, 0, nil)
	if err != nil {
		return nil, err
	}
	parsed := []*vendors.ParsedSession{}
	metas := map[string][]subagentMeta{}
	for _, ref := range refs {
		for _, dir := range ref.Dirs {
			item, err := parseSessionContext(ctx, dir)
			if err != nil {
				return nil, err
			}
			if item == nil {
				return nil, errors.New("grok: session changed during discovery")
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
		return nil, err
	}
	attachSubagents(parsed, metas)
	return parsed, nil
}
