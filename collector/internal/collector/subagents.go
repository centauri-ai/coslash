package collector

import (
	"cmp"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
)

func subagentFrom(
	child, parent *vendors.ParsedSession,
	metadata *vendors.SessionMetadata,
	claudeWorkflowAgent *claude.WorkflowAgent,
	preserveText bool,
	useLiveStatus bool,
) session.Subagent {
	s := child.Session
	task, result := deref(s.FirstPrompt), deref(s.Summary)
	if !preserveText {
		task = session.Truncate(task, session.TruncateTextLimit)
		result = session.Truncate(result, session.TruncateTextLimit)
	}
	subagent := session.Subagent{
		ID:         s.ID,
		Name:       cmp.Or(child.Name, s.ID),
		Model:      s.Model,
		Status:     subagentStatus(child, parent, metadata, useLiveStatus),
		Task:       task,
		Result:     result,
		DurationMs: s.DurationMs,
		ToolUses:   s.ToolUses,
		Commands:   child.Commands,
		Tokens:     s.Tokens,
		Cost:       s.Cost,
	}
	if spawn, ok := parent.Spawns[child.SpawnKey]; ok {
		subagent.SpawnedAtTurn = spawn.Turn
	}
	if claudeWorkflowAgent != nil {
		subagent.Name = cmp.Or(claudeWorkflowAgent.Label, subagent.Name)
		subagent.Status = claudeWorkflowAgent.Status()
		result := cmp.Or(
			claudeWorkflowAgent.ResultPreview, claudeWorkflowAgent.Error, subagent.Result,
		)
		if preserveText {
			subagent.Result = result
		} else {
			subagent.Result = session.Truncate(result, session.TruncateTextLimit)
		}
	}
	return subagent
}

func subagentStatus(
	child, parent *vendors.ParsedSession,
	metadata *vendors.SessionMetadata,
	useLiveStatus bool,
) string {
	if child.Session.Agent == vendors.AgentCodex {
		if child.Stopped {
			return session.SubagentAborted
		}
		if !child.InTurn {
			return session.SubagentReturned
		}
		if useLiveStatus {
			if enrichment := metadata.Lookup(child.Session.ID); enrichment != nil && enrichment.Live != "" {
				return session.SubagentRunning
			}
		}
		return session.SubagentAborted
	}
	if child.Stopped {
		return session.SubagentAborted
	}
	if child.Session.Agent == vendors.AgentOpenCode && child.InTurn {
		return session.SubagentRunning
	}
	if parent.Spawns[child.SpawnKey].Completed {
		return session.SubagentReturned
	}
	// A forked skill is not spawned by a tool call, so its meta.json carries no
	// toolUseId and it never reaches parent.Spawns. Settle it from its own
	// transcript, which clears InTurn on a terminal stop_reason.
	if child.SpawnKey == "" {
		if !child.InTurn {
			return session.SubagentReturned
		}
		// The transcript stops mid-turn when the run dies with its parent.
		if !useLiveStatus {
			return session.SubagentAborted
		}
		if enrichment := metadata.Lookup(parent.Session.ID); enrichment == nil || enrichment.Live == "" {
			return session.SubagentAborted
		}
	}
	return session.SubagentRunning
}
