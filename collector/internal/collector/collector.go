package collector

import (
	"cmp"
	"errors"
	"fmt"
	"log"
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
	"github.com/centauri-ai/coslash/collector/internal/vendors/opencode"
)

const (
	maxProbeWorkers     = 8
	windowContextBuffer = 24 * time.Hour
)

type vendorSource struct {
	name       string
	collect    func(since int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error)
	loadFacts  func(id string) (*vendors.ParsedSession, error)
	loadFamily func(id string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error)
	health     func() vendors.SourceHealth
}

var vendorSources = []vendorSource{
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
}

type SourceHealth = vendors.SourceHealth

func Sources() []SourceHealth {
	health := make([]SourceHealth, 0, len(vendorSources))
	for _, source := range vendorSources {
		health = append(health, source.health())
	}
	return health
}

func SessionIDIndex() (map[string]map[string]bool, error) {
	index := make(map[string]map[string]bool, len(vendorSources))
	for _, source := range vendorSources {
		parsed, _, err := source.collect(0)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source.name, err)
		}
		ids := make(map[string]bool)
		for _, candidate := range parsed {
			if candidate.Session != nil && candidate.ParentID == "" &&
				filepath.Clean(candidate.Session.WorkingDirectory) != filepath.Clean(synthesis.SynthesisCwd()) {
				ids[candidate.Session.ID] = true
			}
		}
		index[source.name] = ids
	}
	return index, nil
}

func List(since int64) ([]*session.Session, error) {
	parsed, metadata, err := collect(max(0, since-windowContextBuffer.Milliseconds()))
	if err != nil {
		return nil, err
	}
	roots := finalizeSessions(parsed, metadata)
	if since > 0 {
		roots = slices.DeleteFunc(roots, func(root *vendors.ParsedSession) bool {
			live := sessionMetadata(metadata, root.Session.Agent).Lookup(root.Session.ID)
			isLive := live != nil && live.Live != ""
			return !isLive && root.Session.LastActivityTime < since
		})
	}
	roots = servableRoots(roots)
	probeLastEdits(roots)
	probeGitEnvironment(roots)
	sessions := make([]*session.Session, 0, len(roots))
	for _, root := range roots {
		sessions = append(sessions, root.Session)
	}
	return sessions, nil
}

// GetSessionForPreview returns the selected fully composed session family.
func GetSessionForPreview(id string, _ int64) (*session.Session, error) {
	return getSessionForPreview("", id)
}

func GetSessionForPreviewByAgent(agent, id string, _ int64) (*session.Session, error) {
	return getSessionForPreview(agent, id)
}

func getSessionForPreview(agent, id string) (*session.Session, error) {
	if id == "" {
		return nil, nil
	}
	var failures []error
	for _, source := range vendorSources {
		if agent != "" && source.name != agent {
			continue
		}
		parsed, metadata, err := source.loadFamily(id)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", source.name, err))
			continue
		}
		roots := servableRoots(finalizeSessions(parsed, map[string]*vendors.SessionMetadata{source.name: metadata}))
		probeLastEdits(roots)
		probeGitEnvironment(roots)
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
func GetSessionDetail(agent, id string) (*session.Session, error) {
	return getSessionDetail(agent, id, true)
}

// GetSessionChanges loads one exact vendor family for an exact diff without
// probing display-only filesystem and Git environment fields.
func GetSessionChanges(agent, id string) (*session.Session, error) {
	return getSessionDetail(agent, id, false)
}

func getSessionDetail(agent, id string, probeEnvironment bool) (*session.Session, error) {
	if id == "" {
		return nil, nil
	}
	for _, source := range vendorSources {
		if source.name != agent {
			continue
		}
		parsed, metadata, err := source.loadFamily(id)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source.name, err)
		}
		roots := servableRoots(finalizeLocalSessions(parsed, map[string]*vendors.SessionMetadata{source.name: metadata}, true))
		if probeEnvironment {
			probeLastEdits(roots)
			probeGitEnvironment(roots)
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

func finalizeSessions(
	parsed []*vendors.ParsedSession,
	metadata map[string]*vendors.SessionMetadata,
) []*vendors.ParsedSession {
	return finalizeLocalSessions(parsed, metadata, false)
}

func finalizeLocalSessions(
	parsed []*vendors.ParsedSession,
	metadata map[string]*vendors.SessionMetadata,
	preserveSubagentText bool,
) []*vendors.ParsedSession {
	roots := finalizeSessionsSource(parsed, metadata, vendors.LocalReadSource, true, true, true)
	for _, root := range roots {
		revision, err := session.LocalDetailRevision(*root.Session)
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
	return roots
}

func finalizeSessionsSource(
	parsed []*vendors.ParsedSession,
	metadata map[string]*vendors.SessionMetadata,
	source vendors.ReadSource,
	useLiveStatus bool,
	allowLocalActivityFallbacks bool,
	preserveSubagentText bool,
) []*vendors.ParsedSession {
	applySessionEnrichment(parsed, metadata)
	applyActivityFallbacks(parsed, allowLocalActivityFallbacks)
	enrichModelsAndCosts(parsed)
	composition := composeSessions(parsed)
	promoteFamilyActivity(composition)
	resolveNames(composition.parsed, metadata)
	enrichSubagents(composition, metadata, claude.WorkflowAgentsSource(source, composition.parsed), preserveSubagentText, useLiveStatus)
	for _, p := range composition.parsed {
		removeUnresolvedSpawnRows(p.Session)
	}
	resolveStatus(composition.roots, metadata, useLiveStatus, source == vendors.LocalReadSource)
	return composition.roots
}

func promoteFamilyActivity(composition sessionComposition) {
	parents := make(map[*vendors.ParsedSession]*vendors.ParsedSession, len(composition.children))
	for _, link := range composition.children {
		parents[link.child] = link.parent
	}
	for child, parent := range parents {
		activity := child.Session.LastActivityTime
		seen := map[*vendors.ParsedSession]bool{child: true}
		for parent != nil && !seen[parent] {
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
}

func applyActivityFallbacks(parsed []*vendors.ParsedSession, allowLocalFallbacks bool) {
	var collectedAt int64
	if allowLocalFallbacks {
		collectedAt = time.Now().UnixMilli()
	}
	for _, item := range parsed {
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
}

func enrichModelsAndCosts(parsed []*vendors.ParsedSession) {
	for _, item := range parsed {
		s := item.Session
		if s.ContextWindow == nil && s.Model != nil {
			s.ContextWindow = session.ContextWindowFor(*s.Model)
		}
		session.AttachCost(s, item.RecordedCost)
	}
}

func collect(
	since int64,
) ([]*vendors.ParsedSession, map[string]*vendors.SessionMetadata, error) {
	parsed := []*vendors.ParsedSession{}
	metadata := map[string]*vendors.SessionMetadata{}
	var failures []error
	for _, source := range vendorSources {
		vendorParsed, vendorMetadata, err := source.collect(since)
		if err != nil {
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

func composeSessions(parsed []*vendors.ParsedSession) sessionComposition {
	byID := make(map[sessionKey]*vendors.ParsedSession, len(parsed))
	for _, p := range parsed {
		byID[sessionKey{agent: p.Session.Agent, id: p.Session.ID}] = p
	}
	composition := sessionComposition{parsed: parsed, roots: []*vendors.ParsedSession{}}
	for _, p := range parsed {
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
	return composition
}

func enrichSubagents(
	composition sessionComposition,
	metadata map[string]*vendors.SessionMetadata,
	claudeDynamicWorkflows map[string]*claude.WorkflowAgent,
	preserveText bool,
	useLiveStatus bool,
) {
	parents := make(map[*vendors.ParsedSession]*vendors.ParsedSession, len(composition.children))
	for _, link := range composition.children {
		parents[link.child] = link.parent
	}
	for _, link := range composition.children {
		p, parent := link.child, link.parent
		subagent := subagentFrom(
			p,
			parent,
			sessionMetadata(metadata, p.Session.Agent),
			claudeDynamicWorkflows[p.Session.ID],
			preserveText,
			useLiveStatus,
		)
		linkSpawnDigest(parent.Session, p.SpawnKey, subagent)
		if p.Session.Agent != vendors.AgentCursor {
			parent.Session.Subagents = append(parent.Session.Subagents, subagent)
			continue
		}
		seen := map[*vendors.ParsedSession]bool{p: true}
		for owner := parent; owner != nil && !seen[owner]; owner = parents[owner] {
			seen[owner] = true
			owner.Session.Subagents = append(owner.Session.Subagents, subagent)
		}
	}
}

// ListRemote composes already-read Claude and Codex facts without probing the
// remote filesystem as if it were local.
func ListRemote(
	source vendors.ReadSource,
	collections map[string]vendors.RemoteCollection,
	since int64,
) []*session.Session {
	return listRemote(source, collections, since, false, true)
}

type PortableSession struct {
	ParentSessionID string
	Session         *session.Session
}

// ComposePortable returns every complete transcript-backed family member for
// immutable records. Unlike ListRemote's display projection, child text is not
// truncated and live status is not applied.
func ComposePortable(
	source vendors.ReadSource,
	collections map[string]vendors.RemoteCollection,
) []PortableSession {
	parsed, metadata := remoteInputs(collections)
	roots := servableRoots(finalizeSessionsSource(parsed, metadata, source, false, false, true))
	rootKeys := make(map[sessionKey]bool, len(roots))
	byKey := make(map[sessionKey]*vendors.ParsedSession, len(parsed))
	for _, root := range roots {
		rootKeys[sessionKey{agent: root.Session.Agent, id: root.Session.ID}] = true
	}
	for _, item := range parsed {
		byKey[sessionKey{agent: item.Session.Agent, id: item.Session.ID}] = item
	}
	portable := make([]PortableSession, 0, len(parsed))
	for _, item := range parsed {
		if belongsToRoot(item, byKey, rootKeys) {
			portable = append(portable, PortableSession{ParentSessionID: item.ParentID, Session: item.Session})
		}
	}
	return portable
}

func listRemote(
	source vendors.ReadSource,
	collections map[string]vendors.RemoteCollection,
	since int64,
	preserveSubagentText bool,
	useLiveStatus bool,
) []*session.Session {
	parsed, metadata := remoteInputs(collections)
	roots := finalizeSessionsSource(parsed, metadata, source, useLiveStatus, false, preserveSubagentText)
	if since > 0 {
		roots = slices.DeleteFunc(roots, func(root *vendors.ParsedSession) bool {
			live := sessionMetadata(metadata, root.Session.Agent).Lookup(root.Session.ID)
			isLive := live != nil && live.Live != ""
			return !isLive && root.Session.LastActivityTime < since
		})
	}
	roots = servableRoots(roots)
	sessions := make([]*session.Session, 0, len(roots))
	for _, root := range roots {
		sessions = append(sessions, root.Session)
	}
	return sessions
}

func remoteInputs(collections map[string]vendors.RemoteCollection) ([]*vendors.ParsedSession, map[string]*vendors.SessionMetadata) {
	parsed := []*vendors.ParsedSession{}
	metadata := map[string]*vendors.SessionMetadata{}
	for _, agent := range []string{vendors.AgentClaude, vendors.AgentCodex} {
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

func linkSpawnDigest(parent *session.Session, spawnKey string, subagent session.Subagent) {
	claimed := -1
	for index, entry := range parent.Digest {
		if entry.Category != session.DigestSubagent || entry.SpawnKey != spawnKey {
			continue
		}
		if entry.SubagentID == "" {
			parent.Digest[index].SubagentID = subagent.ID
			parent.Digest[index].Description = subagent.Name
			return
		}
		claimed = index
	}
	if claimed >= 0 {
		row := parent.Digest[claimed]
		row.SubagentID = subagent.ID
		row.Description = subagent.Name
		parent.Digest = slices.Insert(parent.Digest, claimed+1, row)
		return
	}
	log.Printf("%s: subagent %s has no spawn row in the parent transcript, "+
		"showing it in the rail but not the digest", parent.ID, subagent.ID)
}

func removeUnresolvedSpawnRows(s *session.Session) {
	s.Digest = slices.DeleteFunc(s.Digest, func(entry session.DigestEntry) bool {
		return entry.Category == session.DigestSubagent && entry.SubagentID == ""
	})
}

func probeLastEdits(roots []*vendors.ParsedSession) {
	for _, p := range roots {
		s := p.Session
		if s.LastEditAt == nil {
			s.LastEditAt = session.LatestFileModificationTime(s.WorkingDirectory, s.FileEdits)
		}
	}
}

func probeGitEnvironment(roots []*vendors.ParsedSession) {
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
		name, localOnly := session.CanonicalRepositoryName(cwd)
		repoByCwd[cwd] = repository{name: name, localOnly: localOnly}
		if _, needsBranchProbe := branchByCwd[cwd]; needsBranchProbe {
			branchByCwd[cwd] = session.CurrentBranch(cwd)
		}
	}
	for _, p := range roots {
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
	for key, slot := range drifts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			workers <- struct{}{}
			defer func() { <-workers }()
			branch := key.branch
			slot.drift = session.BranchDrift(key.cwd, &branch)
		}()
	}
	wg.Wait()
	reconcileCommitFacts := session.NewCommitFactsReconciler()
	for _, p := range roots {
		s := p.Session
		facts := reconcileCommitFacts(s.CommitLog, s.WorkingDirectory, s.Branch)
		s.Commits, s.CommitSHAs = facts.Subjects, facts.SHAs
		s.GitProbed = true // synthesis's lazy probe must not redo this
		if s.WorkingDirectory == "" {
			continue
		}
		s.Git = drifts[driftKey{cwd: s.WorkingDirectory, branch: deref(s.Branch)}].drift
		repo := repoByCwd[s.WorkingDirectory]
		s.Repository = &repo.name
		s.RepositoryLocalOnly = repo.localOnly
	}
}

func resolveNames(roots []*vendors.ParsedSession, metadata map[string]*vendors.SessionMetadata) {
	for _, p := range roots {
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
}

func applySessionEnrichment(parsed []*vendors.ParsedSession, metadata map[string]*vendors.SessionMetadata) {
	for _, p := range parsed {
		vendors.ApplySessionEnrichment(p, sessionMetadata(metadata, p.Session.Agent).Lookup(p.Session.ID))
	}
}

func resolveStatus(
	roots []*vendors.ParsedSession,
	metadata map[string]*vendors.SessionMetadata,
	useLiveStatus bool,
	livenessAuthoritative bool,
) {
	var now int64
	if useLiveStatus {
		now = time.Now().UnixMilli()
	}
	for _, p := range roots {
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
		enrichment := sessionMetadata(metadata, s.Agent).Lookup(s.ID)
		raw := ""
		if enrichment != nil {
			raw = enrichment.Live
		}
		live := raw != ""
		if deref(s.Status) == "waiting" && (!livenessAuthoritative || live) {
			continue
		}
		s.Status = nil
		if live {
			status := raw
			if raw == "interactive" {
				status = session.LiveStatus(p.InTurn, s.LastActivityTime, now)
			}
			s.Status = &status
		} else if p.StatusHint != nil {
			status := *p.StatusHint
			s.Status = &status
		}
	}
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
func servableRoots(roots []*vendors.ParsedSession) []*vendors.ParsedSession {
	synthesisCwd := filepath.Clean(synthesis.SynthesisCwd())
	kept := []*vendors.ParsedSession{}
	for _, p := range roots {
		s := p.Session
		if filepath.Clean(s.WorkingDirectory) == synthesisCwd {
			continue
		}
		// FirstPrompt keeps a session that recorded a prompt but never ran it.
		// Codex counts a turn on task_started, not on the prompt, so an
		// interrupted rollout reaches here with real user work and zero counters.
		if s.Status == nil && s.FirstPrompt == nil &&
			s.Turns == 0 && s.ToolUses == 0 && len(s.Tokens) == 0 {
			continue
		}
		kept = append(kept, p)
	}
	return kept
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func GetSessionFacts(id string) (*session.Session, error) {
	return getSessionFacts("", id)
}

func GetSessionFactsByAgent(agent, id string) (*session.Session, error) {
	return getSessionFacts(agent, id)
}

func getSessionFacts(agent, id string) (*session.Session, error) {
	if id == "" {
		return nil, nil
	}
	var p *vendors.ParsedSession
	var failures []error
	for _, source := range vendorSources {
		if agent != "" && source.name != agent {
			continue
		}
		candidate, err := source.loadFacts(id)
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
	removeUnresolvedSpawnRows(p.Session)
	applyActivityFallbacks([]*vendors.ParsedSession{p}, true)
	probeGitEnvironment([]*vendors.ParsedSession{p})
	return p.Session, nil
}
