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
)

const (
	maxProbeWorkers     = 8
	windowContextBuffer = 24 * time.Hour
)

type vendorSource struct {
	name     string
	root     func() (string, error)
	files    func() ([]string, error)
	scan     func() (*vendors.SourceScan, error)
	id       func(path string) string // session id without parsing, for Get
	parse    func(path string) (*vendors.ParsedTranscript, error)
	metadata func() (*vendors.SessionMetadata, error) // information not extractable from logs alone
	fork     func(parsed []*vendors.ParsedTranscript)
	window   func(files []string, live map[string]string, since int64) []string
}

var vendorSources = []vendorSource{
	{
		vendors.AgentClaude, claude.Root, claude.Files, claude.Scan, claude.IDFromPath,
		claude.Parse, claude.LoadMetadata, claude.ApplyForkedUsage, claude.FilesSince,
	},
	{
		vendors.AgentCodex, codex.Root, codex.Files, codex.Scan, codex.SessionIDFromRollout,
		codex.Parse, codex.LoadMetadata, codex.ApplyForkedUsage, codex.FilesSince,
	},
}

type SourceHealth struct {
	Agent   string
	Root    string
	Scan    *vendors.SourceScan
	ScanErr error
}

func Sources() []SourceHealth {
	health := make([]SourceHealth, 0, len(vendorSources))
	for _, source := range vendorSources {
		root, rootErr := source.root()
		if rootErr != nil {
			health = append(health, SourceHealth{Agent: source.name, ScanErr: rootErr})
			continue
		}
		scan, scanErr := source.scan()
		health = append(health, SourceHealth{Agent: source.name, Root: root, Scan: scan, ScanErr: scanErr})
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

func collect(since int64) ([]*vendors.ParsedTranscript, map[string]*vendors.SessionMetadata, error) {
	parsed := []*vendors.ParsedTranscript{}
	metadata := map[string]*vendors.SessionMetadata{}
	var failures []error
	for _, source := range vendorSources {
		vendorParsed, vendorMetadata, err := collectAndParseVendor(source, since)
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
	repoByCwd := map[string]string{}
	drifts := map[driftKey]*driftSlot{}
	for _, p := range roots {
		s := p.Session
		if s.WorkingDirectory == "" {
			continue
		}
		repoByCwd[s.WorkingDirectory] = ""
		drifts[driftKey{s.WorkingDirectory, deref(s.Branch)}] = &driftSlot{}
	}
	for cwd := range repoByCwd {
		repoByCwd[cwd] = session.CanonicalRepositoryName(cwd)
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
		s.Repository = &repo
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

// Get returns one session by id: a directory walk, one file parse, and its
// probes. No fork pass, no subagents, no name/status resolution — synthesis (BuildInput,
// Eligible) and launch read none of those. Sessions in the synthesis cwd stay
// invisible, as in the list. Returns nil when the id is unknown.
func Get(id string) (*session.Session, error) {
	if id == "" {
		return nil, nil
	}
	var p *vendors.ParsedTranscript
	for _, source := range vendorSources {
		files, err := source.files()
		if err != nil {
			return nil, err
		}
		path := ""
		for _, file := range files {
			if source.id(file) == id {
				path = file
				break
			}
		}
		if path == "" {
			continue
		}
		if p, err = source.parse(path); err != nil {
			return nil, err
		}
		break
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
