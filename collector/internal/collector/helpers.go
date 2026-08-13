package collector

import (
	"cmp"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
)

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
		if child.Stopped {
			return session.SubagentAborted
		}
		if !child.InTurn {
			return session.SubagentReturned
		}
		if _, live := metadata.Live[child.Session.ID]; live {
			return session.SubagentRunning
		}
		return session.SubagentAborted
	}
	if child.Stopped {
		return session.SubagentAborted
	}
	if child.Session.Agent == vendors.AgentOpenCode && child.InTurn {
		return session.SubagentRunning
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
