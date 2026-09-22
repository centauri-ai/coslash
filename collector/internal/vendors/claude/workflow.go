package claude

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"path/filepath"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// Dynamic Workflow agent's label, lifecycle and returned value live in the run's state file
type WorkflowAgent struct {
	Label         string `json:"label"`
	State         string `json:"state"` // "progress" until it settles on "done" | "error"
	ResultPreview string `json:"resultPreview"`
	Error         string `json:"error"` // set instead of resultPreview when State is "error"
	AgentID       string `json:"agentId"`
	Type          string `json:"type"` // "workflow_agent" | "workflow_phase"
	runFinished   bool
}

// <parent>/workflows/<run-id>.json, written once when the run stops
type workflowRun struct {
	// DurationMs is stamped when run finished
	DurationMs int             `json:"durationMs"`
	Progress   []WorkflowAgent `json:"workflowProgress"`
}

// one line per agent start and per agent result, in the run's transcript dir
type workflowJournalEntry struct {
	Type    string          `json:"type"` // "started" | "result"
	AgentID string          `json:"agentId"`
	Result  json.RawMessage `json:"result"`
}

func WorkflowAgentsSource(
	source vendors.ReadSource,
	parsed []*vendors.ParsedSession,
) map[string]*WorkflowAgent {
	agents, _ := WorkflowAgentsSourceContext(context.Background(), source, parsed)
	return agents
}

func WorkflowAgentsSourceContext(
	ctx context.Context,
	source vendors.ReadSource,
	parsed []*vendors.ParsedSession,
) (map[string]*WorkflowAgent, error) {
	agents := map[string]*WorkflowAgent{}
	finished := map[string]bool{}
	for _, p := range parsed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		statePath, ok := workflowStatePathSource(source, p.LogPath)
		if !ok {
			continue
		}
		if _, seen := finished[statePath]; seen {
			continue
		}
		finished[statePath] = false
		var run workflowRun
		found, err := vendors.ReadJSONSource(source, statePath, &run)
		if err != nil {
			log.Printf("%s: unreadable workflow state: %v", statePath, err)
			continue
		}
		if !found {
			continue
		}
		finished[statePath] = run.DurationMs > 0
		for i := range run.Progress {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			agent := &run.Progress[i]
			if agent.Type != "workflow_agent" {
				continue
			}
			agent.runFinished = run.DurationMs > 0
			agents["agent-"+agent.AgentID] = agent
		}
	}
	journals := map[string]map[string]string{}
	for _, p := range parsed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		statePath, ok := workflowStatePathSource(source, p.LogPath)
		if !ok || agents[p.Session.ID] != nil || !finished[statePath] {
			continue
		}
		results, cached := journals[statePath]
		if !cached {
			var err error
			results, err = workflowJournalResultsSourceContext(
				ctx,
				source,
				vendors.SourcePathJoin(source, vendors.SourcePathDir(source, p.LogPath), "journal.jsonl"),
			)
			if err != nil {
				return nil, err
			}
			journals[statePath] = results
		}
		agent := &WorkflowAgent{
			AgentID:     strings.TrimPrefix(p.Session.ID, "agent-"),
			runFinished: true,
		}
		if result, returned := results[agent.AgentID]; returned {
			agent.State = "done"
			agent.ResultPreview = result
		}
		agents[p.Session.ID] = agent
	}
	return agents, nil
}

func workflowJournalResultsSource(source vendors.ReadSource, path string) map[string]string {
	results, _ := workflowJournalResultsSourceContext(context.Background(), source, path)
	return results
}

func workflowJournalResultsSourceContext(ctx context.Context, source vendors.ReadSource, path string) (map[string]string, error) {
	entries, err := vendors.ParseJSONLSourceContext[workflowJournalEntry](ctx, source, path)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("%s: unreadable workflow journal: %v", path, err)
		}
		return nil, nil
	}
	results := map[string]string{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.Type == "result" && entry.AgentID != "" {
			results[entry.AgentID] = journalResultText(entry.Result)
		}
	}
	return results, nil
}

func journalResultText(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return string(raw)
}

// run killed mid-flight leaves its unfinished agents on "progress" forever
func (agent *WorkflowAgent) Status() string {
	switch {
	case agent.State == "done":
		return session.SubagentReturned
	case agent.State == "error":
		return session.SubagentAborted
	case agent.runFinished:
		return session.SubagentAborted
	default:
		return session.SubagentRunning
	}
}

func workflowRunID(logPath string) string {
	normalized := filepath.ToSlash(logPath)
	if !strings.Contains(normalized, "/subagents/workflows/") {
		return ""
	}
	return filepath.Base(filepath.Dir(normalized))
}

func workflowStatePathSource(source vendors.ReadSource, logPath string) (string, bool) {
	normalized := filepath.ToSlash(logPath)
	if !strings.Contains(normalized, "/subagents/workflows/") {
		return "", false
	}
	runDir := vendors.SourcePathDir(
		source,
		strings.Replace(normalized, "/subagents/workflows/", "/workflows/", 1),
	)
	return runDir + ".json", true
}
