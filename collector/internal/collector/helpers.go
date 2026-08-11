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

func adaptFileVendor(source fileSource) vendorSource {
	return vendorSource{
		name: source.name,
		collect: func(since int64) ([]*vendors.ParsedTranscript, *vendors.SessionMetadata, error) {
			return collectAndParseFiles(source, since)
		},
		get: func(id string) (*vendors.ParsedTranscript, error) {
			files, err := source.files()
			if err != nil {
				return nil, err
			}
			for _, file := range files {
				if source.id(file) == id {
					return source.parse(file)
				}
			}
			return nil, nil
		},
		health: func() SourceHealth {
			root, err := source.root()
			if err != nil {
				return SourceHealth{Agent: source.name, Err: err}
			}
			scan, err := source.scan()
			if err != nil {
				return SourceHealth{Agent: source.name, Root: root, Err: err}
			}
			sessions := 0
			for _, file := range scan.Files {
				isRoot, err := source.isRoot(file)
				if err != nil {
					scan.RecordSkipped(file, err)
					continue
				}
				if isRoot {
					sessions++
				}
			}
			return SourceHealth{
				Agent:        source.name,
				Root:         root,
				Entries:      len(scan.Files),
				Sessions:     sessions,
				Missing:      scan.RootMissing,
				Skipped:      scan.Skipped,
				SkippedTotal: max(scan.SkippedTotal, len(scan.Skipped)),
			}
		},
		fork: source.fork,
	}
}

func collectAndParseFiles(
	source fileSource,
	since int64,
) ([]*vendors.ParsedTranscript, *vendors.SessionMetadata, error) {
	files, err := source.files()
	if err != nil {
		return nil, nil, err
	}
	metadata, err := source.metadata()
	if err != nil {
		return nil, nil, err
	}
	if since > 0 {
		files = source.window(files, metadata.Live, since)
	}
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
			p, err := source.parse(file)
			if err != nil {
				log.Printf("%s: skipping unreadable transcript: %v", file, err)
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
	return parsed, metadata, nil
}

func subagentFrom(
	child, parent *vendors.ParsedTranscript,
	metadata *vendors.SessionMetadata,
	claudeWorkflowAgent *claude.WorkflowAgent,
) session.Subagent {
	s := child.Session
	subagent := session.Subagent{
		ID:           s.ID,
		Name:         cmp.Or(child.Name, s.ID),
		Model:        s.Model,
		Status:       subagentStatus(child, parent, metadata),
		Task:         session.Truncate(deref(s.FirstPrompt), session.TruncateTextLimit),
		Result:       session.Truncate(deref(s.Summary), session.TruncateTextLimit),
		DurationMs:   s.DurationMs,
		ToolUses:     s.ToolUses,
		Commands:     child.Commands,
		Tokens:       s.Tokens,
		Cost:         s.Cost,
		CostRecorded: s.CostRecorded,
	}
	if turn, ok := parent.SpawnTurns[child.SpawnKey]; ok {
		subagent.SpawnedAtTurn = &turn
	}
	if claudeWorkflowAgent != nil {
		subagent.Name = cmp.Or(claudeWorkflowAgent.Label, subagent.Name)
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
	// A forked skill is not spawned by a tool call, so its meta.json carries no
	// toolUseId and it never reaches parent.Completed. Settle it from its own
	// transcript, which clears InTurn on a terminal stop_reason.
	if child.SpawnKey == "" {
		if !child.InTurn {
			return session.SubagentReturned
		}
		// The transcript stops mid-turn when the run dies with its parent.
		if _, live := metadata.Live[parent.Session.ID]; !live {
			return session.SubagentAborted
		}
	}
	return session.SubagentRunning
}
