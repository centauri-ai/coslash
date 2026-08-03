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

const maxProbeWorkers = 8

type vendorSource struct {
	name     string
	files    func() ([]string, error)
	id       func(path string) string // session id without parsing, for Get
	parse    func(path string) (*vendors.ParsedTranscript, error)
	metadata func() (*vendors.SessionMetadata, error) // information not extractable from logs alone
	fork     func(parsed []*vendors.ParsedTranscript, paths map[string]string)
}

var vendorSources = []vendorSource{
	{
		vendors.AgentClaude, claude.Files, claude.IDFromPath,
		claude.Parse, claude.LoadMetadata,
		func(parsed []*vendors.ParsedTranscript, _ map[string]string) {
			claude.ApplyForkedUsage(parsed)
		},
	},
	{
		vendors.AgentCodex, codex.Files, codex.SessionIDFromRollout,
		codex.Parse, codex.LoadMetadata, codex.ApplyForkedUsage,
	},
}

func (c *Collector) list(options ListOptions, stats *collectionStats) ([]*session.Session, error) {
	parsed, metadata, paths, err := c.collect(options, stats)
	if err != nil {
		return nil, err
	}
	applyForkedUsage(parsed, paths)
	roots := groupSubagents(parsed, metadata)
	probeEnvironment(roots)
	resolveNames(roots, metadata)
	resolveStatus(roots, metadata)
	sessions := excludeSynthesisRuns(roots)
	if options.Since > 0 {
		sessions = slices.DeleteFunc(sessions, func(s *session.Session) bool {
			return s.Status == nil && s.LastActivityTime < options.Since
		})
	}
	return sessions, nil
}

func applyForkedUsage(
	parsed []*vendors.ParsedTranscript,
	paths map[string]map[string]string,
) {
	byAgent := map[string][]*vendors.ParsedTranscript{}
	for _, p := range parsed {
		byAgent[p.Session.Agent] = append(byAgent[p.Session.Agent], p)
	}
	for _, source := range vendorSources {
		source.fork(byAgent[source.name], paths[source.name])
	}
}

func (c *Collector) collect(
	options ListOptions,
	stats *collectionStats,
) (
	[]*vendors.ParsedTranscript,
	map[string]*vendors.SessionMetadata,
	map[string]map[string]string,
	error,
) {
	parsed := []*vendors.ParsedTranscript{}
	metadata := map[string]*vendors.SessionMetadata{}
	paths := map[string]map[string]string{}
	var failures []error
	for _, source := range vendorSources {
		discovered, err := source.files()
		if err != nil {
			log.Printf("%s session discovery failed: %v; serving other vendors", source.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", source.name, err))
			continue
		}
		files := statTranscriptFiles(discovered)
		c.evictMissing(source.name, files)
		vendorMetadata, err := source.metadata()
		if err != nil {
			log.Printf("%s session metadata failed: %v; serving other vendors", source.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", source.name, err))
			continue
		}
		paths[source.name] = make(map[string]string, len(files))
		for _, file := range files {
			if id := source.id(file.path); id != "" {
				paths[source.name][id] = file.path
			}
		}
		selected := files
		if source.name == vendors.AgentCodex && options.Since > 0 {
			selected = c.selectCodexFiles(files, vendorMetadata, options.Since, stats)
		}
		vendorParsed, err := c.collectAndParseVendor(source, selected, stats)
		if err != nil {
			log.Printf("%s session collection failed: %v; serving other vendors", source.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", source.name, err))
			continue
		}
		metadata[source.name] = vendorMetadata
		parsed = append(parsed, vendorParsed...)
	}
	if len(failures) == len(vendorSources) {
		return nil, nil, nil, errors.Join(failures...)
	}
	return parsed, metadata, paths, nil
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
			name = session.Truncate(name, session.TruncateTextLimit)
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

// excludeSynthesisRuns drops the collector's own synthesis CLI sessions, which
// run in SynthesisCwd and would otherwise show up as sessions of their own.
func excludeSynthesisRuns(roots []*vendors.ParsedTranscript) []*session.Session {
	synthesisCwd := filepath.Clean(synthesis.SynthesisCwd())
	kept := []*session.Session{}
	for _, p := range roots {
		if filepath.Clean(p.Session.WorkingDirectory) == synthesisCwd {
			continue
		}
		kept = append(kept, p.Session)
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
func (c *Collector) Get(id string) (*session.Session, error) {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
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
		versioned := statTranscriptFiles([]string{path})
		if len(versioned) == 0 {
			return nil, nil
		}
		if p, err = c.parseCached(source, versioned[0], &collectionStats{}); err != nil {
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
	return p.Session, nil
}
