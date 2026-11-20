package collector

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/review"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
	"github.com/centauri-ai/coslash/collector/internal/vendors/cursor"
	"github.com/centauri-ai/coslash/collector/internal/vendors/grok"
	"github.com/centauri-ai/coslash/collector/internal/vendors/opencode"
	"github.com/centauri-ai/coslash/collector/internal/vendors/pi"
)

const (
	maxProbeWorkers     = 8
	windowContextBuffer = 24 * time.Hour
)

type vendorSource struct {
	name       string
	collect    func(context.Context, int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error)
	loadFacts  func(context.Context, string) (*vendors.ParsedSession, error)
	loadFamily func(context.Context, string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error)
	health     func(context.Context) vendors.SourceHealth
}

var vendorSources = func() []vendorSource {
	sources := []vendorSource{
		{
			name: vendors.AgentClaude, collect: claude.Collect, loadFacts: claude.GetSessionFacts,
			loadFamily: claude.GetSessionFamily,
			health:     claude.Health,
		},
		{
			name: vendors.AgentCodex, collect: codex.Collect, loadFacts: codex.GetSessionFacts,
			loadFamily: codex.GetSessionFamily,
			health:     codex.Health,
		},
		{
			name: vendors.AgentOpenCode, collect: opencode.Collect, loadFacts: opencode.GetSessionFacts,
			loadFamily: opencode.GetSessionFamily,
			health:     opencode.Health,
		},
		{
			name: vendors.AgentCursor, collect: cursor.Collect, loadFacts: cursor.GetSessionFacts,
			loadFamily: cursor.GetSessionFamily,
			health:     cursor.Health,
		},
		{
			name: vendors.AgentGrok, collect: grok.Collect, loadFacts: grok.GetSessionFacts,
			loadFamily: grok.GetSessionFamily,
			health:     grok.Health,
		},
	}
	if vendors.PiCollectionSupported() {
		sources = append([]vendorSource{{name: vendors.AgentPi, collect: pi.Collect, loadFacts: pi.GetSessionFacts, loadFamily: pi.GetSessionFamily, health: pi.Health}}, sources...)
	}
	return sources
}()

type SourceHealth = vendors.SourceHealth

func Sources(ctx context.Context) []SourceHealth {
	health := make([]SourceHealth, 0, len(vendorSources))
	for _, source := range vendorSources {
		health = append(health, source.health(ctx))
	}
	return health
}

func List(ctx context.Context, since int64) ([]*session.Session, error) {
	parsed, metadata, err := collect(ctx, max(0, since-windowContextBuffer.Milliseconds()))
	if err != nil {
		return nil, err
	}
	roots, err := finalizeLocalSessions(ctx, parsed, metadata, false)
	if err != nil {
		return nil, err
	}
	if since > 0 {
		roots = slices.DeleteFunc(roots, func(root *vendors.ParsedSession) bool {
			live := sessionMetadata(metadata, root.Session.Agent).Lookup(root.Session.ID)
			isLive := live != nil && live.Live != ""
			return !isLive && root.Session.LastActivityTime < since
		})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	roots, err = servableRoots(ctx, roots)
	if err != nil {
		return nil, err
	}
	if err := probeLastEdits(ctx, roots); err != nil {
		return nil, err
	}
	if err := probeGitEnvironment(ctx, roots); err != nil {
		return nil, err
	}
	sessions := make([]*session.Session, 0, len(roots))
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sessions = append(sessions, root.Session)
	}
	return sessions, nil
}

// FinalizeBatch composes already-parsed whole families into servable
// root sessions with the same enrichment and environment probes as List.
// Streamed discovery calls it once per batch.
func FinalizeBatch(
	ctx context.Context,
	parsed []*vendors.ParsedSession,
	metadata map[string]*vendors.SessionMetadata,
) ([]*session.Session, error) {
	roots, err := finalizeLocalSessions(ctx, parsed, metadata, false)
	if err != nil {
		return nil, err
	}
	roots, err = servableRoots(ctx, roots)
	if err != nil {
		return nil, err
	}
	if err := probeLastEdits(ctx, roots); err != nil {
		return nil, err
	}
	if err := probeGitEnvironment(ctx, roots); err != nil {
		return nil, err
	}
	sessions := make([]*session.Session, 0, len(roots))
	for _, root := range roots {
		sessions = append(sessions, root.Session)
	}
	return sessions, nil
}

// GetSessionForPreview returns the selected fully composed session family.
// An empty agent searches every vendor.
func GetSessionForPreview(ctx context.Context, agent, id string) (*session.Session, error) {
	if id == "" {
		return nil, nil
	}
	var failures []error
	for _, source := range vendorSources {
		if agent != "" && source.name != agent {
			continue
		}
		parsed, metadata, err := source.loadFamily(ctx, id)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", source.name, err))
			continue
		}
		roots, err := loadedRoots(ctx, parsed, map[string]*vendors.SessionMetadata{source.name: metadata}, false, true)
		if err != nil {
			return nil, err
		}
		if root := previewRootFor(parsed, roots, id); root != nil {
			return root.Session, nil
		}
	}
	if len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	return nil, nil
}

// GetSessionDetail loads one exact vendor family for the inspector. Requiring
// the agent as well as the vendor session ID keeps identical IDs from
// different parsers distinct and avoids a machine-wide collection.
func GetSessionDetail(ctx context.Context, agent, id string) (*session.Session, error) {
	return getSessionDetail(ctx, agent, id, true)
}

// GetSessionChanges loads one exact vendor family for an exact diff without
// probing display-only filesystem and Git environment fields.
func GetSessionChanges(ctx context.Context, agent, id string) (*session.Session, error) {
	return getSessionDetail(ctx, agent, id, false)
}

func getSessionDetail(ctx context.Context, agent, id string, probeEnvironment bool) (*session.Session, error) {
	if id == "" {
		return nil, nil
	}
	for _, source := range vendorSources {
		if source.name != agent {
			continue
		}
		parsed, metadata, err := source.loadFamily(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source.name, err)
		}
		roots, err := loadedRoots(ctx, parsed, map[string]*vendors.SessionMetadata{source.name: metadata}, true, probeEnvironment)
		if err != nil {
			return nil, err
		}
		for _, candidate := range roots {
			if candidate.Session.ID == id {
				return candidate.Session, nil
			}
		}
		return nil, nil
	}
	return nil, nil
}

// loadedRoots finalizes one loaded family into servable roots and, when asked,
// probes their filesystem and Git environment.
func loadedRoots(
	ctx context.Context,
	parsed []*vendors.ParsedSession,
	metadata map[string]*vendors.SessionMetadata,
	preserveSubagentText, probeEnvironment bool,
) ([]*vendors.ParsedSession, error) {
	roots, err := finalizeLocalSessions(ctx, parsed, metadata, preserveSubagentText)
	if err != nil {
		return nil, err
	}
	roots, err = servableRoots(ctx, roots)
	if err != nil || !probeEnvironment {
		return roots, err
	}
	if err := probeLastEdits(ctx, roots); err != nil {
		return nil, err
	}
	if err := probeGitEnvironment(ctx, roots); err != nil {
		return nil, err
	}
	return roots, nil
}

func previewRootFor(parsed, roots []*vendors.ParsedSession, id string) *vendors.ParsedSession {
	byID := map[sessionKey]*vendors.ParsedSession{}
	for _, item := range parsed {
		if item != nil && item.Session != nil {
			byID[sessionKey{item.Session.Agent, item.Session.ID}] = item
		}
	}
	for _, item := range parsed {
		if item == nil || item.Session == nil || item.Session.ID != id {
			continue
		}
		seen := map[sessionKey]bool{}
		for item.ParentID != "" {
			key := sessionKey{item.Session.Agent, item.Session.ID}
			if seen[key] {
				item = nil
				break
			}
			seen[key] = true
			item = byID[sessionKey{item.Session.Agent, item.ParentID}]
			if item == nil {
				break
			}
		}
		if item == nil {
			continue
		}
		for _, root := range roots {
			if root.Session.Agent == item.Session.Agent && root.Session.ID == item.Session.ID {
				return root
			}
		}
	}
	return nil
}

func finalizeLocalSessions(
	ctx context.Context,
	parsed []*vendors.ParsedSession,
	metadata map[string]*vendors.SessionMetadata,
	preserveSubagentText bool,
) ([]*vendors.ParsedSession, error) {
	roots, err := finalizeSessions(ctx, parsed, metadata, vendors.LocalReadSource, true, true, true)
	if err != nil {
		return nil, err
	}
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		revision, err := session.LocalDetailRevision(ctx, *root.Session)
		if err != nil && ctx.Err() != nil {
			return nil, err
		}
		if err == nil {
			root.Session.DetailRevision = revision
		}
		if !preserveSubagentText {
			for index := range root.Session.Subagents {
				root.Session.Subagents[index].Task = session.Truncate(root.Session.Subagents[index].Task, session.TruncateTextLimit)
				root.Session.Subagents[index].Result = session.Truncate(root.Session.Subagents[index].Result, session.TruncateTextLimit)
			}
		}
	}
	return roots, nil
}

func finalizeSessions(
	ctx context.Context,
	parsed []*vendors.ParsedSession,
	metadata map[string]*vendors.SessionMetadata,
	source vendors.ReadSource,
	useLiveStatus bool,
	allowLocalActivityFallbacks bool,
	preserveSubagentText bool,
) ([]*vendors.ParsedSession, error) {
	if err := applySessionEnrichment(ctx, parsed, metadata); err != nil {
		return nil, err
	}
	if err := applyActivityFallbacks(ctx, parsed, allowLocalActivityFallbacks); err != nil {
		return nil, err
	}
	if err := enrichModelsAndCosts(ctx, parsed); err != nil {
		return nil, err
	}
	composition, err := composeSessions(ctx, parsed)
	if err != nil {
		return nil, err
	}
	for _, root := range composition.roots {
		root.Session.SynthesisRevision = synthesis.Revision(root.Session)
	}
	if err := promoteFamilyActivity(ctx, composition); err != nil {
		return nil, err
	}
	if err := resolveNames(ctx, composition.parsed, metadata); err != nil {
		return nil, err
	}
	workflowAgents, err := claude.WorkflowAgents(ctx, source, composition.parsed)
	if err != nil {
		return nil, err
	}
	if err := enrichSubagents(ctx, composition, metadata, workflowAgents, preserveSubagentText, useLiveStatus); err != nil {
		return nil, err
	}
	for _, p := range composition.parsed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := removeUnresolvedSpawnRows(ctx, p.Session); err != nil {
			return nil, err
		}
	}
	if err := resolveStatus(ctx, composition.roots, metadata, useLiveStatus, source == vendors.LocalReadSource); err != nil {
		return nil, err
	}
	return composition.roots, nil
}

func promoteFamilyActivity(ctx context.Context, composition sessionComposition) error {
	parents := make(map[*vendors.ParsedSession]*vendors.ParsedSession, len(composition.children))
	for _, link := range composition.children {
		if err := ctx.Err(); err != nil {
			return err
		}
		parents[link.child] = link.parent
	}
	for child, parent := range parents {
		if err := ctx.Err(); err != nil {
			return err
		}
		activity := child.Session.LastActivityTime
		seen := map[*vendors.ParsedSession]bool{child: true}
		for parent != nil && !seen[parent] {
			if err := ctx.Err(); err != nil {
				return err
			}
			seen[parent] = true
			if activity > parent.Session.LastActivityTime {
				parent.Session.LastActivityTime = activity
				parent.Session.ActivityFallback = child.Session.ActivityFallback
			} else if activity == parent.Session.LastActivityTime && !child.Session.ActivityFallback {
				parent.Session.ActivityFallback = false
			}
			parent = parents[parent]
		}
	}
	return nil
}

func applyActivityFallbacks(ctx context.Context, parsed []*vendors.ParsedSession, allowLocalFallbacks bool) error {
	var collectedAt int64
	if allowLocalFallbacks {
		collectedAt = time.Now().UnixMilli()
	}
	for _, item := range parsed {
		if err := ctx.Err(); err != nil {
			return err
		}
		s := item.Session
		if s.LastActivityTime == 0 && item.LogModifiedAtMs > 0 {
			s.LastActivityTime = item.LogModifiedAtMs
		} else if allowLocalFallbacks && s.LastActivityTime == 0 && item.LogPath != "" {
			s.LastActivityTime = session.FileModificationTime(item.LogPath)
		}
		// Prefer source-derived activity. Local display paths may use collection
		// time, while portable records leave missing timing invalid so Freeze can
		// reject it instead of producing a wall-clock-dependent revision.
		if s.StartedAt == 0 {
			s.StartedAt = s.LastActivityTime
			if s.StartedAt == 0 && allowLocalFallbacks {
				s.StartedAt = collectedAt
				s.LastActivityTime = collectedAt
				s.ActivityFallback = true
			}
		}
	}
	return nil
}

func enrichModelsAndCosts(ctx context.Context, parsed []*vendors.ParsedSession) error {
	for _, item := range parsed {
		if err := ctx.Err(); err != nil {
			return err
		}
		s := item.Session
		if s.ContextWindow == nil && s.Model != nil {
			s.ContextWindow = session.ContextWindowFor(*s.Model)
		}
		if s.Agent == vendors.AgentPi {
			// Pi reports an aggregate source cost, without component pricing.
			s.Cost = item.RecordedCost
			if s.CostUnavailable {
				s.Cost = nil
			}
			continue
		}
		if s.Agent != vendors.AgentGrok || s.CostUnavailable || s.TokensUnavailable || s.UnattributedTokens != nil {
			session.AttachCost(s, item.RecordedCost)
			continue
		}
		missing := session.Session{Tokens: make(map[string]session.ModelTokens)}
		for model, used := range s.Tokens {
			if cost, known := item.RecordedModelCosts[model]; known {
				used.Cost = cost
				s.Tokens[model] = used
			} else {
				missing.Tokens[model] = used
			}
		}
		session.AttachCost(&missing, nil)
		for model, used := range missing.Tokens {
			s.Tokens[model] = used
		}
		if item.RecordedCost != nil {
			session.AttachCost(s, item.RecordedCost)
			continue
		}
		s.UnpricedModels = slices.DeleteFunc(session.UnpricedModels(s.Tokens), func(model string) bool {
			_, sourceCostKnown := item.RecordedModelCosts[model]
			return sourceCostKnown
		})
		s.Cost = nil
		if !item.CostIncomplete && len(s.UnpricedModels) == 0 && len(s.Tokens) > 0 {
			var total float64
			for _, model := range slices.Sorted(maps.Keys(s.Tokens)) {
				total += s.Tokens[model].Cost
			}
			s.Cost = &total
		}
	}
	return nil
}

func collect(
	ctx context.Context,
	since int64,
) ([]*vendors.ParsedSession, map[string]*vendors.SessionMetadata, error) {
	parsed := []*vendors.ParsedSession{}
	metadata := map[string]*vendors.SessionMetadata{}
	var failures []error
	for _, source := range vendorSources {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		vendorParsed, vendorMetadata, err := source.collect(ctx, since)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			log.Printf("%s session collection failed: %v; serving other vendors", source.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", source.name, err))
			continue
		}
		metadata[source.name] = vendorMetadata
		parsed = append(parsed, vendorParsed...)
	}
	if len(failures) == len(vendorSources) {
		return nil, nil, errors.Join(failures...)
	}
	return parsed, metadata, nil
}

type sessionKey struct {
	agent string
	id    string
}

type childLink struct {
	child  *vendors.ParsedSession
	parent *vendors.ParsedSession
}

type sessionComposition struct {
	parsed   []*vendors.ParsedSession
	roots    []*vendors.ParsedSession
	children []childLink
}

func composeSessions(ctx context.Context, parsed []*vendors.ParsedSession) (sessionComposition, error) {
	byID := make(map[sessionKey]*vendors.ParsedSession, len(parsed))
	for _, p := range parsed {
		if err := ctx.Err(); err != nil {
			return sessionComposition{}, err
		}
		byID[sessionKey{agent: p.Session.Agent, id: p.Session.ID}] = p
	}
	composition := sessionComposition{parsed: parsed, roots: []*vendors.ParsedSession{}}
	for _, p := range parsed {
		if err := ctx.Err(); err != nil {
			return sessionComposition{}, err
		}
		if p.ParentID == "" {
			composition.roots = append(composition.roots, p)
			continue
		}
		parent, ok := byID[sessionKey{agent: p.Session.Agent, id: p.ParentID}]
		if !ok {
			log.Printf(
				"%s: %s parent %s not found, dropping child",
				p.Session.Agent,
				p.Session.ID,
				p.ParentID,
			)
			continue
		}
		if deref(p.Session.Status) == "waiting" {
			status := "waiting"
			parent.Session.Status = &status
		}
		composition.children = append(composition.children, childLink{child: p, parent: parent})
	}
	return composition, nil
}

func enrichSubagents(
	ctx context.Context,
	composition sessionComposition,
	metadata map[string]*vendors.SessionMetadata,
	claudeDynamicWorkflows map[string]*claude.WorkflowAgent,
	preserveText bool,
	useLiveStatus bool,
) error {
	parents := make(map[*vendors.ParsedSession]*vendors.ParsedSession, len(composition.children))
	for _, link := range composition.children {
		if err := ctx.Err(); err != nil {
			return err
		}
		parents[link.child] = link.parent
	}
	for _, link := range composition.children {
		if err := ctx.Err(); err != nil {
			return err
		}
		p, parent := link.child, link.parent
		subagent := subagentFrom(
			p,
			parent,
			sessionMetadata(metadata, p.Session.Agent),
			claudeDynamicWorkflows[p.Session.ID],
			preserveText,
			useLiveStatus,
		)
		if err := linkSpawnDigest(ctx, parent.Session, p.SpawnKey, subagent); err != nil {
			return err
		}
		if len(p.Session.Skills) > 0 {
			seen := map[*vendors.ParsedSession]bool{p: true}
			for owner := parent; owner != nil && !seen[owner]; owner = parents[owner] {
				seen[owner] = true
				owner.Session.Skills = session.MergeSkills(owner.Session.Skills, p.Session.Skills)
			}
		}
		// Pull requests that a subagent created belong to every ancestor session.
		if len(p.Session.PullRequestLinks) > 0 {
			seenAncestors := map[*vendors.ParsedSession]bool{p: true}
			for owner := parent; owner != nil && !seenAncestors[owner]; owner = parents[owner] {
				seenAncestors[owner] = true
				owner.Session.SetPullRequestLinks(session.MergePullRequestLinks(owner.Session.PullRequestLinks, p.Session.PullRequestLinks))
			}
		}
		if p.Session.Agent != vendors.AgentCursor {
			parent.Session.Subagents = append(parent.Session.Subagents, subagent)
			continue
		}
		seen := map[*vendors.ParsedSession]bool{p: true}
		for owner := parent; owner != nil && !seen[owner]; owner = parents[owner] {
			if err := ctx.Err(); err != nil {
				return err
			}
			seen[owner] = true
			owner.Session.Subagents = append(owner.Session.Subagents, subagent)
		}
	}
	return nil
}

// ListRemote composes already-read Claude and Codex facts without probing the
// remote filesystem as if it were local.
func ListRemote(
	ctx context.Context,
	source vendors.ReadSource,
	collections map[string]vendors.RemoteCollection,
	since int64,
) ([]*session.Session, error) {
	return listRemote(ctx, source, collections, since, false, true)
}

type PortableSession struct {
	ParentSessionID string
	Session         *session.Session
}

func ComposePortable(
	ctx context.Context,
	source vendors.ReadSource,
	collections map[string]vendors.RemoteCollection,
) ([]PortableSession, error) {
	parsed, metadata := remoteInputs(collections, source == vendors.LocalReadSource)
	finalized, err := finalizeSessions(ctx, parsed, metadata, source, false, false, true)
	if err != nil {
		return nil, err
	}
	roots, err := servableRoots(ctx, finalized)
	if err != nil {
		return nil, err
	}
	rootKeys := make(map[sessionKey]bool, len(roots))
	byKey := make(map[sessionKey]*vendors.ParsedSession, len(parsed))
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rootKeys[sessionKey{agent: root.Session.Agent, id: root.Session.ID}] = true
	}
	for _, item := range parsed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		byKey[sessionKey{agent: item.Session.Agent, id: item.Session.ID}] = item
	}
	portable := make([]PortableSession, 0, len(parsed))
	for _, item := range parsed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if belongsToRoot(item, byKey, rootKeys) {
			value := item.Session
			if value.Agent == vendors.AgentPi {
				// Pi modality comes from runtime ownership, not transcript content.
				copy := *value
				copy.Entrypoint = nil
				value = &copy
			}
			portable = append(portable, PortableSession{ParentSessionID: item.ParentID, Session: value})
		}
	}
	return portable, nil
}

func listRemote(
	ctx context.Context,
	source vendors.ReadSource,
	collections map[string]vendors.RemoteCollection,
	since int64,
	preserveSubagentText bool,
	useLiveStatus bool,
) ([]*session.Session, error) {
	parsed, metadata := remoteInputs(collections, false)
	roots, err := finalizeSessions(ctx, parsed, metadata, source, useLiveStatus, false, preserveSubagentText)
	if err != nil {
		return nil, err
	}
	if since > 0 {
		roots = slices.DeleteFunc(roots, func(root *vendors.ParsedSession) bool {
			live := sessionMetadata(metadata, root.Session.Agent).Lookup(root.Session.ID)
			isLive := live != nil && live.Live != ""
			return !isLive && root.Session.LastActivityTime < since
		})
	}
	roots, err = servableRoots(ctx, roots)
	if err != nil {
		return nil, err
	}
	sessions := make([]*session.Session, 0, len(roots))
	for _, root := range roots {
		sessions = append(sessions, root.Session)
	}
	return sessions, nil
}

func remoteInputs(collections map[string]vendors.RemoteCollection, includeLocalAgents bool) ([]*vendors.ParsedSession, map[string]*vendors.SessionMetadata) {
	parsed := []*vendors.ParsedSession{}
	metadata := map[string]*vendors.SessionMetadata{}
	agents := []string{vendors.AgentClaude, vendors.AgentCodex, vendors.AgentCursor, vendors.AgentOpenCode}
	if includeLocalAgents {
		agents = append(agents, vendors.AgentGrok, vendors.AgentPi)
	}
	for _, agent := range agents {
		collection, ok := collections[agent]
		if !ok {
			continue
		}
		parsed = append(parsed, collection.Sessions...)
		metadata[agent] = collection.Metadata
	}
	return parsed, metadata
}

func belongsToRoot(item *vendors.ParsedSession, byKey map[sessionKey]*vendors.ParsedSession, roots map[sessionKey]bool) bool {
	seen := map[sessionKey]bool{}
	for item != nil {
		key := sessionKey{agent: item.Session.Agent, id: item.Session.ID}
		if roots[key] {
			return true
		}
		if item.ParentID == "" || seen[key] {
			return false
		}
		seen[key] = true
		item = byKey[sessionKey{agent: item.Session.Agent, id: item.ParentID}]
	}
	return false
}

func linkSpawnDigest(ctx context.Context, parent *session.Session, spawnKey string, subagent session.Subagent) error {
	claimed := -1
	for index, entry := range parent.Digest {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Category != session.DigestSubagent || entry.SpawnKey != spawnKey {
			continue
		}
		if entry.SubagentID == "" {
			parent.Digest[index].SubagentID = subagent.ID
			parent.Digest[index].Description = subagent.Name
			return nil
		}
		claimed = index
	}
	if claimed >= 0 {
		row := parent.Digest[claimed]
		row.SubagentID = subagent.ID
		row.Description = subagent.Name
		parent.Digest = slices.Insert(parent.Digest, claimed+1, row)
		return nil
	}
	log.Printf("%s: subagent %s has no spawn row in the parent transcript, "+
		"showing it in the rail but not the digest", parent.ID, subagent.ID)
	return nil
}

func removeUnresolvedSpawnRows(ctx context.Context, s *session.Session) error {
	kept := s.Digest[:0]
	for _, entry := range s.Digest {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Category != session.DigestSubagent || entry.SubagentID != "" {
			kept = append(kept, entry)
		}
	}
	s.Digest = kept
	return nil
}

func probeLastEdits(ctx context.Context, roots []*vendors.ParsedSession) error {
	for _, p := range roots {
		if err := ctx.Err(); err != nil {
			return err
		}
		s := p.Session
		if s.LastEditAt == nil {
			s.LastEditAt = session.LatestFileModificationTime(ctx, s.WorkingDirectory, s.FileEdits)
			if err := ctx.Err(); err != nil {
				return err
			}
		}
	}
	return nil
}

func probeGitEnvironment(ctx context.Context, roots []*vendors.ParsedSession) error {
	// Drift is measured for the recorded or best-effort current branch against
	// the repo's base branch, so it memoizes per (cwd, branch).
	type driftKey struct{ cwd, branch string }
	type driftSlot struct{ drift *session.GitDrift }
	type repository struct {
		name      string
		localOnly bool
	}
	repoByCwd := map[string]repository{}
	branchByCwd := map[string]*string{}
	drifts := map[driftKey]*driftSlot{}
	for _, p := range roots {
		if err := ctx.Err(); err != nil {
			return err
		}
		s := p.Session
		if s.WorkingDirectory == "" {
			continue
		}
		repoByCwd[s.WorkingDirectory] = repository{}
		if deref(s.Branch) == "" {
			branchByCwd[s.WorkingDirectory] = nil
		}
	}
	for cwd := range repoByCwd {
		if err := ctx.Err(); err != nil {
			return err
		}
		name, localOnly := session.CanonicalRepositoryName(ctx, cwd)
		if err := ctx.Err(); err != nil {
			return err
		}
		repoByCwd[cwd] = repository{name: name, localOnly: localOnly}
		if _, needsBranchProbe := branchByCwd[cwd]; needsBranchProbe {
			branchByCwd[cwd] = session.CurrentBranch(ctx, cwd)
			if err := ctx.Err(); err != nil {
				return err
			}
		}
	}
	for _, p := range roots {
		if err := ctx.Err(); err != nil {
			return err
		}
		s := p.Session
		if s.WorkingDirectory == "" {
			continue
		}
		if deref(s.Branch) == "" {
			s.Branch = branchByCwd[s.WorkingDirectory]
		}
		drifts[driftKey{cwd: s.WorkingDirectory, branch: deref(s.Branch)}] = &driftSlot{}
	}
	// BranchDrift waits on git subprocesses, so distinct keys probe
	// concurrently. Results land through slot pointers — never map writes,
	// which would race the range still spawning goroutines.
	workers := make(chan struct{}, maxProbeWorkers)
	var wg sync.WaitGroup
	canceled := false
	for key, slot := range drifts {
		if ctx.Err() != nil {
			canceled = true
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case workers <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-workers }()
			if ctx.Err() != nil {
				return
			}
			branch := key.branch
			slot.drift = session.BranchDrift(ctx, key.cwd, &branch)
		}()
	}
	wg.Wait()
	if canceled || ctx.Err() != nil {
		return ctx.Err()
	}
	reconcileCommitFacts := session.NewCommitFactsReconciler(ctx)
	for _, p := range roots {
		if err := ctx.Err(); err != nil {
			return err
		}
		s := p.Session
		facts := reconcileCommitFacts(s.CommitLog, s.WorkingDirectory, s.Branch)
		if err := ctx.Err(); err != nil {
			return err
		}
		s.Commits, s.CommitSHAs = facts.Subjects, facts.SHAs
		s.GitProbed = true // synthesis's lazy probe must not redo this
		if s.WorkingDirectory == "" {
			continue
		}
		s.Git = drifts[driftKey{cwd: s.WorkingDirectory, branch: deref(s.Branch)}].drift
		// A remote the agent recorded (Codex's session_meta) outlives a
		// folder that no longer has one.
		if repo := repoByCwd[s.WorkingDirectory]; !repo.localOnly || s.Repository == nil || *s.Repository == "" || s.RepositoryLocalOnly {
			s.Repository = &repo.name
			s.RepositoryLocalOnly = repo.localOnly
		}
	}
	return nil
}

func resolveNames(ctx context.Context, roots []*vendors.ParsedSession, metadata map[string]*vendors.SessionMetadata) error {
	for _, p := range roots {
		if err := ctx.Err(); err != nil {
			return err
		}
		s := p.Session
		if s.FirstPrompt != nil {
			if name, ok := review.NameFromPrompt(*s.FirstPrompt); ok {
				s.Name = &name
				continue
			}
		}
		enrichment := sessionMetadata(metadata, s.Agent).Lookup(s.ID)
		name := p.Name
		if enrichment != nil {
			name = cmp.Or(enrichment.Name, name)
		}
		if name != "" {
			s.Name = &name
		}
	}
	return nil
}

func applySessionEnrichment(ctx context.Context, parsed []*vendors.ParsedSession, metadata map[string]*vendors.SessionMetadata) error {
	for _, p := range parsed {
		if err := ctx.Err(); err != nil {
			return err
		}
		vendors.ApplySessionEnrichment(p, sessionMetadata(metadata, p.Session.Agent).Lookup(p.Session.ID))
	}
	return nil
}

func resolveStatus(
	ctx context.Context,
	roots []*vendors.ParsedSession,
	metadata map[string]*vendors.SessionMetadata,
	useLiveStatus bool,
	livenessAuthoritative bool,
) error {
	var now int64
	if useLiveStatus {
		now = time.Now().UnixMilli()
	}
	for _, p := range roots {
		if err := ctx.Err(); err != nil {
			return err
		}
		s := p.Session
		if !useLiveStatus {
			if deref(s.Status) == "waiting" {
				continue
			}
			s.Status = nil
			if p.StatusHint != nil {
				status := *p.StatusHint
				s.Status = &status
			}
			continue
		}
		agentMetadata := sessionMetadata(metadata, s.Agent)
		enrichment := agentMetadata.Lookup(s.ID)
		raw := ""
		if enrichment != nil {
			raw = enrichment.Live
		}
		live := raw != ""
		waiting := deref(s.Status) == "waiting"
		if waiting && (!livenessAuthoritative || live) {
			continue
		}
		s.Status = nil
		if live {
			status := raw
			if raw == "interactive" {
				status = session.LiveStatus(p.InTurn, s.LastActivityTime, now)
			}
			s.Status = &status
		} else if p.StatusHint != nil && (!livenessAuthoritative || !agentMetadata.LivenessChecked) {
			status := *p.StatusHint
			s.Status = &status
		} else if s.Agent == vendors.AgentGrok && p.InTurn && !waiting && agentMetadata.LivenessChecked {
			status := "idle"
			s.Status = &status
		}
	}
	return nil
}

func sessionMetadata(
	metadata map[string]*vendors.SessionMetadata,
	agent string,
) *vendors.SessionMetadata {
	if value := metadata[agent]; value != nil {
		return value
	}
	return vendors.EmptySessionMetadata()
}

// drop synthesis cli sessions
// claude: drop /clear stub sessions
// codex: drop session_meta-only sessions
func servableRoots(ctx context.Context, roots []*vendors.ParsedSession) ([]*vendors.ParsedSession, error) {
	synthesisCwd := filepath.Clean(synthesis.SynthesisCwd())
	kept := []*vendors.ParsedSession{}
	for _, p := range roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s := p.Session
		if filepath.Clean(s.WorkingDirectory) == synthesisCwd {
			continue
		}
		// FirstPrompt keeps a session that recorded a prompt but never ran it.
		// Codex counts a turn on task_started, not on the prompt, so an
		// interrupted rollout reaches here with real user work and zero counters.
		if s.Status == nil && s.FirstPrompt == nil &&
			s.Turns == 0 && s.ToolUses == 0 && len(s.Tokens) == 0 && !s.TokensKnown && !s.TokensUnavailable && !s.CostUnavailable && s.UnattributedTokens == nil {
			continue
		}
		kept = append(kept, p)
	}
	return kept, nil
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// GetSessionFacts loads the facts of one root session. An empty agent searches
// every vendor.
func GetSessionFacts(ctx context.Context, agent, id string) (*session.Session, error) {
	if id == "" {
		return nil, nil
	}
	var p *vendors.ParsedSession
	var failures []error
	for _, source := range vendorSources {
		if agent != "" && source.name != agent {
			continue
		}
		candidate, err := source.loadFacts(ctx, id)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", source.name, err))
			continue
		}
		if candidate != nil && candidate.ParentID == "" {
			p = candidate
			break
		}
	}
	if p == nil {
		if len(failures) > 0 {
			return nil, errors.Join(failures...)
		}
		return nil, nil
	}
	if filepath.Clean(p.Session.WorkingDirectory) == filepath.Clean(synthesis.SynthesisCwd()) {
		return nil, nil
	}
	// No subagents here means no spawn key can resolve.
	if err := removeUnresolvedSpawnRows(ctx, p.Session); err != nil {
		return nil, err
	}
	family := []*vendors.ParsedSession{p}
	if err := applyActivityFallbacks(ctx, family, true); err != nil {
		return nil, err
	}
	if err := probeGitEnvironment(ctx, family); err != nil {
		return nil, err
	}
	return p.Session, nil
}
