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
	name    string
	collect func(since int64) ([]*vendors.ParsedTranscript, *vendors.SessionMetadata, error)
	get     func(id string) (*vendors.ParsedTranscript, error)
	health  func() SourceHealth
	fork    func(parsed []*vendors.ParsedTranscript)
}

type fileSource struct {
	name     string
	root     func() (string, error)
	files    func() ([]string, error)
	scan     func() (*vendors.SourceScan, error)
	isRoot   func(path string) (bool, error)
	id       func(path string) string // session id without parsing, for Get
	parse    func(path string) (*vendors.ParsedTranscript, error)
	metadata func() (*vendors.SessionMetadata, error) // information not extractable from logs alone
	fork     func(parsed []*vendors.ParsedTranscript)
	window   func(files []string, live map[string]string, since int64) []string
}

var vendorSources = []vendorSource{
	adaptFileVendor(fileSource{
		name: vendors.AgentClaude, root: claude.Root, files: claude.Files, scan: claude.Scan,
		isRoot: func(path string) (bool, error) { return claude.ParentIDFromPath(path) == "", nil },
		id:     claude.IDFromPath, parse: claude.Parse, metadata: claude.LoadMetadata,
		fork: claude.ApplyForkedUsage, window: claude.FilesSince,
	}),
	adaptFileVendor(fileSource{
		name: vendors.AgentCodex, root: codex.Root, files: codex.Files, scan: codex.Scan,
		isRoot: codex.IsRootRollout,
		id:     codex.SessionIDFromRollout, parse: codex.Parse, metadata: codex.LoadMetadata,
		fork: codex.ApplyForkedUsage, window: codex.FilesSince,
	}),
	{
		name: vendors.AgentOpenCode, collect: opencode.Collect, get: opencode.Get,
		health: func() SourceHealth {
			root, err := opencode.Root()
			if err != nil {
				return SourceHealth{Agent: vendors.AgentOpenCode, Err: err}
			}
			health, err := opencode.Health()
			return SourceHealth{
				Agent: vendors.AgentOpenCode, Root: root, Entries: health.Entries,
				Sessions: health.Sessions, Missing: health.Missing, Err: err,
			}
		},
		fork: func([]*vendors.ParsedTranscript) {},
	},
}

type SourceHealth struct {
	Agent        string
	Root         string
	Entries      int
	Sessions     int
	Missing      bool
	Skipped      []vendors.SkippedPath
	SkippedTotal int
	Err          error
}

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
	applyForkedUsage(parsed)
	roots := groupSubagents(parsed, metadata)
	if since > 0 {
		roots = slices.DeleteFunc(roots, func(root *vendors.ParsedTranscript) bool {
			_, live := metadata[root.Session.Agent].Live[root.Session.ID]
			return !live && root.Session.LastActivityTime < since
		})
	}
	probeEnvironment(roots)
	resolveNames(roots, metadata)
	resolveStatus(roots, metadata)
	sessions := servableRoots(roots)
	for _, s := range sessions {
		session.AttachCosts(s)
	}
	return sessions, nil
}

func applyForkedUsage(parsed []*vendors.ParsedTranscript) {
	byAgent := map[string][]*vendors.ParsedTranscript{}
	for _, p := range parsed {
		byAgent[p.Session.Agent] = append(byAgent[p.Session.Agent], p)
	}
	for _, source := range vendorSources {
		source.fork(byAgent[source.name])
	}
}

func collect(
	since int64,
) ([]*vendors.ParsedTranscript, map[string]*vendors.SessionMetadata, error) {
	parsed := []*vendors.ParsedTranscript{}
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

func groupSubagents(
	parsed []*vendors.ParsedTranscript,
	metadata map[string]*vendors.SessionMetadata,
) []*vendors.ParsedTranscript {
	byID := make(map[string]*vendors.ParsedTranscript, len(parsed))
	for _, p := range parsed {
		byID[p.Session.ID] = p
	}
	claudeDynamicWorkflows := claude.WorkflowAgents(parsed)
	roots := []*vendors.ParsedTranscript{}
	for _, p := range parsed {
		if p.ParentID == "" {
			roots = append(roots, p)
			continue
		}
		parent, ok := byID[p.ParentID]
		if !ok {
			log.Printf("%s: parent %s not found, dropping child", p.Session.ID, p.ParentID)
			continue
		}
		subagent := subagentFrom(
			p,
			parent,
			metadata[p.Session.Agent],
			claudeDynamicWorkflows[p.Session.ID],
		)
		linkSpawnDigest(parent.Session, p.SpawnKey, subagent)
		parent.Session.Subagents = append(parent.Session.Subagents, subagent)
	}
	for _, p := range parsed {
		p.Session.Digest = slices.DeleteFunc(p.Session.Digest, unresolvedSpawn)
	}
	return roots
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

func unresolvedSpawn(entry session.DigestEntry) bool {
	return entry.Category == session.DigestSubagent && entry.SubagentID == ""
}

func probeEnvironment(roots []*vendors.ParsedTranscript) {
	// Drift is measured for the session's recorded branch against the repo's
	// base branch, so it memoizes per (cwd, branch); the repo name is a pure
	// filesystem lookup, per cwd.
	type driftKey struct{ cwd, branch string }
	type driftSlot struct{ drift *session.GitDrift }
	type repository struct {
		name      string
		localOnly bool
	}
	repoByCwd := map[string]repository{}
	drifts := map[driftKey]*driftSlot{}
	for _, p := range roots {
		s := p.Session
		if s.WorkingDirectory == "" {
			continue
		}
		repoByCwd[s.WorkingDirectory] = repository{}
		drifts[driftKey{s.WorkingDirectory, deref(s.Branch)}] = &driftSlot{}
	}
	for cwd := range repoByCwd {
		name, localOnly := session.CanonicalRepositoryName(cwd)
		repoByCwd[cwd] = repository{name: name, localOnly: localOnly}
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
	for _, p := range roots {
		s := p.Session
		s.LastEditAt = session.LatestFileModificationTime(s.FileEdits)
		s.GitProbed = true // synthesis's lazy probe must not redo this
		if s.WorkingDirectory == "" {
			continue
		}
		s.Git = drifts[driftKey{s.WorkingDirectory, deref(s.Branch)}].drift
		repo := repoByCwd[s.WorkingDirectory]
		s.Repository = &repo.name
		s.RepositoryLocalOnly = repo.localOnly
	}
}

func resolveNames(roots []*vendors.ParsedTranscript, metadata map[string]*vendors.SessionMetadata) {
	for _, p := range roots {
		s := p.Session
		if name := cmp.Or(metadata[s.Agent].Names[s.ID], p.Name); name != "" {
			s.Name = &name
		}
	}
}

func resolveStatus(
	roots []*vendors.ParsedTranscript,
	metadata map[string]*vendors.SessionMetadata,
) {
	now := time.Now().UnixMilli()
	for _, p := range roots {
		s := p.Session
		if raw, live := metadata[s.Agent].Live[s.ID]; live {
			status := raw
			if raw == "interactive" {
				status = session.LiveStatus(p.InTurn, s.LastActivityTime, now)
			}
			s.Status = &status
		}
	}
}

// drop synthesis cli sessions
// claude: drop /clear stub sessions
// codex: drop session_meta-only sessions
func servableRoots(roots []*vendors.ParsedTranscript) []*session.Session {
	synthesisCwd := filepath.Clean(synthesis.SynthesisCwd())
	kept := []*session.Session{}
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
		kept = append(kept, s)
	}
	return kept
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// Get returns one session by id using the vendor's native lookup and its probes.
// No fork pass, no subagents, no name/status resolution — synthesis (BuildInput,
// Eligible) and launch read none of those. Sessions in the synthesis cwd stay
// invisible, as in the list. Returns nil when the id is unknown.
func Get(id string) (*session.Session, error) {
	if id == "" {
		return nil, nil
	}
	var p *vendors.ParsedTranscript
	for _, source := range vendorSources {
		var err error
		if p, err = source.get(id); err != nil {
			return nil, err
		}
		if p != nil {
			break
		}
	}
	if p == nil {
		return nil, nil
	}
	if filepath.Clean(p.Session.WorkingDirectory) == filepath.Clean(synthesis.SynthesisCwd()) {
		return nil, nil
	}
	// No subagents here means no spawn key can resolve
	p.Session.Digest = slices.DeleteFunc(p.Session.Digest, unresolvedSpawn)
	probeEnvironment([]*vendors.ParsedTranscript{p})
	session.AttachCosts(p.Session)
	return p.Session, nil
}
