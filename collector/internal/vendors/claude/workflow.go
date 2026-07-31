package claude

import (
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

// WorkflowAgents indexes Dynamic Workflow agents by session id their transcript parses to, "agent-<agentId>".
func WorkflowAgents(parsed []*vendors.ParsedTranscript) map[string]*WorkflowAgent {
	agents := map[string]*WorkflowAgent{}
	finished := map[string]bool{}
	for _, p := range parsed {
		statePath, ok := workflowStatePath(p.Session.LogPath)
		if !ok {
			continue
		}
		if _, done := finished[statePath]; done {
			continue
		}
		finished[statePath] = false
		var run workflowRun
		found, err := session.ReadJSONIfValid(statePath, &run)
		if err != nil || !found {
			log.Printf("%s: unreadable workflow state: %v", statePath, err)
			continue
		}
		finished[statePath] = run.DurationMs > 0
		for i := range run.Progress {
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
		statePath, ok := workflowStatePath(p.Session.LogPath)
		if !ok || agents[p.Session.ID] != nil || !finished[statePath] {
			continue
		}
		results, cached := journals[statePath]
		if !cached {
			results = workflowJournalResults(
				filepath.Join(filepath.Dir(p.Session.LogPath), "journal.jsonl"),
			)
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
	return agents
}

func workflowJournalResults(path string) map[string]string {
	entries, err := session.ParseJSONL[workflowJournalEntry](path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("%s: unreadable workflow journal: %v", path, err)
		}
		return nil
	}
	results := map[string]string{}
	for _, entry := range entries {
		if entry.Type == "result" && entry.AgentID != "" {
			results[entry.AgentID] = journalResultText(entry.Result)
		}
	}
	return results
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
	if !strings.Contains(logPath, "/subagents/workflows/") {
		return ""
	}
	return filepath.Base(filepath.Dir(logPath))
}

func workflowStatePath(logPath string) (string, bool) {
	if !strings.Contains(logPath, "/subagents/workflows/") {
		return "", false
	}
	runDir := filepath.Dir(strings.Replace(logPath, "/subagents/workflows/", "/workflows/", 1))
	return runDir + ".json", true
}
