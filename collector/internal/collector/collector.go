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

func List(since int64) ([]*session.Session, error) {
	parsed, metadata, err := collect(max(0, since-windowContextBuffer.Milliseconds()))
	if err != nil {
		return nil, err
	}
	roots := finalizeSessions(parsed, metadata)
	if since > 0 {
		roots = slices.DeleteFunc(roots, func(root *vendors.ParsedSession) bool {
			_, live := sessionMetadata(metadata, root.Session.Agent).Live[root.Session.ID]
			return !live && root.Session.LastActivityTime < since
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
	if id == "" {
		return nil, nil
	}
	var failures []error
	for _, source := range vendorSources {
		parsed, metadata, err := source.loadFamily(id)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", source.name, err))
			continue
		}
		roots := servableRoots(finalizeSessions(parsed, map[string]*vendors.SessionMetadata{source.name: metadata}))
		probeLastEdits(roots)
		probeGitEnvironment(roots)
		for _, candidate := range roots {
			if candidate.Session.ID == id {
				return candidate.Session, nil
			}
		}
	}
	if len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	return nil, nil
}

func finalizeSessions(
	parsed []*vendors.ParsedSession,
	metadata map[string]*vendors.SessionMetadata,
) []*vendors.ParsedSession {
	return finalizeSessionsSource(parsed, metadata, vendors.LocalReadSource)
}

func finalizeSessionsSource(
	parsed []*vendors.ParsedSession,
	metadata map[string]*vendors.SessionMetadata,
	source vendors.ReadSource,
) []*vendors.ParsedSession {
	applyActivityFallbacks(parsed)
	enrichModelsAndCosts(parsed)
	composition := composeSessions(parsed)
	promoteFamilyActivity(composition)
	enrichSubagents(composition, metadata, claude.WorkflowAgentsSource(source, composition.parsed))
	for _, p := range composition.parsed {
		removeUnresolvedSpawnRows(p.Session)
	}
	resolveNames(composition.roots, metadata)
	resolveStatus(composition.roots, metadata, source == vendors.LocalReadSource)
	return composition.roots
}

func promoteFamilyActivity(composition sessionComposition) {
	parents := make(map[*vendors.ParsedSession]*vendors.ParsedSession, len(composition.children))
	for _, link := range composition.children {
		parents[link.child] = link.parent
	}
	for child, parent := range parents {
		activity := child.Session.LastActivityTime
		for parent != nil {
			parent.Session.LastActivityTime = max(parent.Session.LastActivityTime, activity)
			parent = parents[parent]
		}
	}
}

func applyActivityFallbacks(parsed []*vendors.ParsedSession) {
	collectedAt := time.Now().UnixMilli()
	for _, item := range parsed {
		s := item.Session
		if s.LastActivityTime == 0 && item.LogModifiedAtMs > 0 {
			s.LastActivityTime = item.LogModifiedAtMs
		} else if s.LastActivityTime == 0 && item.LogPath != "" {
			s.LastActivityTime = session.FileModificationTime(item.LogPath)
		}
		// The export contract requires a positive start. Prefer last activity,
		// then collection time when the source and its log provide no timestamp.
		if s.StartedAt == 0 {
			s.StartedAt = s.LastActivityTime
			if s.StartedAt == 0 {
				s.StartedAt = collectedAt
				s.LastActivityTime = collectedAt
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
) {
	for _, link := range composition.children {
		p, parent := link.child, link.parent
		subagent := subagentFrom(
			p,
			parent,
			sessionMetadata(metadata, p.Session.Agent),
			claudeDynamicWorkflows[p.Session.ID],
		)
		linkSpawnDigest(parent.Session, p.SpawnKey, subagent)
		parent.Session.Subagents = append(parent.Session.Subagents, subagent)
	}
}

// ListRemote composes already-read Claude and Codex facts without probing the
// remote filesystem as if it were local.
func ListRemote(
	source vendors.ReadSource,
	collections map[string]vendors.RemoteCollection,
	since int64,
) []*session.Session {
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
	roots := finalizeSessionsSource(parsed, metadata, source)
	if since > 0 {
		roots = slices.DeleteFunc(roots, func(root *vendors.ParsedSession) bool {
			_, live := sessionMetadata(metadata, root.Session.Agent).Live[root.Session.ID]
			return !live && root.Session.LastActivityTime < since
		})
	}
	roots = servableRoots(roots)
	sessions := make([]*session.Session, 0, len(roots))
	for _, root := range roots {
		sessions = append(sessions, root.Session)
	}
	return sessions
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
	reconcileCommits := session.NewCommitReconciler()
	for _, p := range roots {
		s := p.Session
		s.Commits = reconcileCommits(
			s.CommitLog, s.WorkingDirectory, s.Branch,
		)
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
		if name := cmp.Or(sessionMetadata(metadata, s.Agent).Names[s.ID], p.Name); name != "" {
			s.Name = &name
		}
	}
}

func resolveStatus(
	roots []*vendors.ParsedSession,
	metadata map[string]*vendors.SessionMetadata,
	livenessAuthoritative bool,
) {
	now := time.Now().UnixMilli()
	for _, p := range roots {
		s := p.Session
		raw, live := sessionMetadata(metadata, s.Agent).Live[s.ID]
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
	if id == "" {
		return nil, nil
	}
	var p *vendors.ParsedSession
	var failures []error
	for _, source := range vendorSources {
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
	applyActivityFallbacks([]*vendors.ParsedSession{p})
	probeGitEnvironment([]*vendors.ParsedSession{p})
	return p.Session, nil
}
