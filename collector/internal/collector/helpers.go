package collector

import (
	"cmp"
	"log"
	"sync"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
)

const maxParseWorkers = 8

func (c *Collector) collectAndParseVendor(
	source vendorSource,
	files []transcriptFile,
	stats *collectionStats,
) ([]*vendors.ParsedTranscript, error) {
	// parsing can be done in parallel, but file order must be preserved
	results := make([]*vendors.ParsedTranscript, len(files))
	workers := make(chan struct{}, maxParseWorkers)
	var wg sync.WaitGroup
	for i, file := range files {
		wg.Add(1)
		go func() {
			defer wg.Done()
			workers <- struct{}{}
			defer func() { <-workers }()
			p, err := c.parseCached(source, file, stats)
			if err != nil {
				log.Printf("%s: skipping unreadable transcript: %v", file.path, err)
				return
			}
			results[i] = p
		}()
	}
	wg.Wait()
	parsed := make([]*vendors.ParsedTranscript, 0, len(files))
	for _, p := range results {
		if p != nil {
			parsed = append(parsed, p)
		}
	}
	return parsed, nil
}

func subagentFrom(
	child, parent *vendors.ParsedTranscript,
	metadata *vendors.SessionMetadata,
	claudeWorkflowAgent *claude.WorkflowAgent,
) session.Subagent {
	s := child.Session
	subagent := session.Subagent{
		ID:         s.ID,
		Name:       session.Truncate(cmp.Or(child.Name, s.ID), session.TruncateTextLimit),
		Model:      s.Model,
		Status:     subagentStatus(child, parent, metadata),
		Task:       session.Truncate(deref(s.FirstPrompt), session.TruncateTextLimit),
		Result:     session.Truncate(deref(s.Summary), session.TruncateTextLimit),
		DurationMs: s.DurationMs,
		ToolUses:   s.ToolUses,
		Commands:   child.Commands,
		Tokens:     s.Tokens,
	}
	if turn, ok := parent.SpawnTurns[child.SpawnKey]; ok {
		subagent.SpawnedAtTurn = &turn
	}
	if claudeWorkflowAgent != nil {
		subagent.Name = session.Truncate(
			cmp.Or(claudeWorkflowAgent.Label, subagent.Name),
			session.TruncateTextLimit,
		)
		subagent.Status = claudeWorkflowAgent.Status()
		result := cmp.Or(
			claudeWorkflowAgent.ResultPreview, claudeWorkflowAgent.Error, subagent.Result,
		)
		subagent.Result = session.Truncate(result, session.TruncateTextLimit)
	}
	return subagent
}

func subagentStatus(
	child, parent *vendors.ParsedTranscript,
	metadata *vendors.SessionMetadata,
) string {
	if child.Session.Agent == vendors.AgentCodex {
		if _, live := metadata.Live[child.Session.ID]; live {
			return session.SubagentRunning
		}
		if child.Stopped {
			return session.SubagentAborted
		}
		return session.SubagentReturned
	}
	if child.Stopped {
		return session.SubagentAborted
	}
	if _, ok := parent.Completed[child.SpawnKey]; ok {
		return session.SubagentReturned
	}
	return session.SubagentRunning
}
