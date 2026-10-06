package grok

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

type subagentMeta struct {
	dir              string
	ChildSessionID   string `json:"child_session_id"`
	Description      string `json:"description"`
	Prompt           string `json:"prompt"`
	Status           string `json:"status"`
	DurationMs       *int   `json:"duration_ms"`
	ToolCalls        int    `json:"tool_calls"`
	EffectiveModelID string `json:"effective_model_id"`
	Output           string `json:"output"`
}

func readSubagentMetas(ctx context.Context, sessionDir string) []subagentMeta {
	entries, _ := os.ReadDir(filepath.Join(sessionDir, "subagents"))
	metas := []subagentMeta{}
	missing := map[string]bool{}
	for _, entry := range entries {
		meta := subagentMeta{dir: filepath.Join(sessionDir, "subagents", entry.Name())}
		if entry.IsDir() && readJSON(filepath.Join(meta.dir, "meta.json"), &meta) == nil && meta.ChildSessionID != "" {
			metas = append(metas, meta)
		} else if entry.IsDir() {
			missing[entry.Name()] = true
		}
	}
	if len(missing) == 0 {
		return metas
	}
	// Grok can create the child directory but fail to write meta.json on long Windows paths.
	body, err := readBounded(filepath.Join(sessionDir, "updates.jsonl"), maxGrokJSONBytes)
	summary, summaryErr := readSummary(sessionDir)
	if err != nil || summaryErr != nil {
		return metas
	}
	type eventLine struct {
		Params struct {
			SessionID string `json:"sessionId"`
			Update    struct {
				subagentMeta
				Kind      string `json:"sessionUpdate"`
				ID        string `json:"subagent_id"`
				ParentID  string `json:"parent_session_id"`
				AttemptID string `json:"attempt_id"`
				Model     string `json:"model"`
			} `json:"update"`
		} `json:"params"`
	}
	recovered := map[string]subagentMeta{}
	attempts := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), int(maxGrokJSONBytes))
	for scanner.Scan() {
		if ctx.Err() != nil {
			return metas
		}
		var line eventLine
		if json.Unmarshal(scanner.Bytes(), &line) != nil {
			continue
		}
		update := line.Params.Update
		if line.Params.SessionID == summary.Info.ID && missing[update.ID] && update.ChildSessionID != "" {
			switch update.Kind {
			case "subagent_spawned":
				if update.ParentID == summary.Info.ID {
					meta := update.subagentMeta
					meta.EffectiveModelID = update.Model
					recovered[update.ID], attempts[update.ID] = meta, update.AttemptID
				}
			case "subagent_finished":
				meta, ok := recovered[update.ID]
				if ok && meta.ChildSessionID == update.ChildSessionID && attempts[update.ID] == update.AttemptID {
					meta.Status, meta.DurationMs, meta.ToolCalls, meta.Output = update.Status, update.DurationMs, update.ToolCalls, update.Output
					recovered[update.ID] = meta
				}
			}
		}
	}
	if scanner.Err() == nil {
		seen := map[string]bool{}
		for _, meta := range metas {
			seen[meta.ChildSessionID] = true
		}
		for _, entry := range entries {
			if meta, ok := recovered[entry.Name()]; ok && !seen[meta.ChildSessionID] {
				metas = append(metas, meta)
				seen[meta.ChildSessionID] = true
			}
		}
	}
	return metas
}

// attachSubagents links children using parent metadata or structured spawn events.
func attachSubagents(parsed []*vendors.ParsedSession, metasByDir map[string][]subagentMeta) {
	byID := make(map[string]*vendors.ParsedSession, len(parsed))
	for _, item := range parsed {
		byID[item.Session.ID] = item
	}
	for _, parent := range parsed {
		if parent.ParentID != "" {
			continue
		}
		for _, meta := range metasByDir[filepath.Dir(parent.LogPath)] {
			child := byID[meta.ChildSessionID]
			if child == nil || (child.ParentID != "" && child.ParentID != parent.Session.ID) {
				continue
			}
			// The child summary often omits parent_session_id.
			child.ParentID = parent.Session.ID
			if parent.Spawns == nil {
				parent.Spawns = map[string]vendors.SpawnState{}
			}
			parent.Spawns[child.Session.ID] = vendors.SpawnState{Task: meta.Prompt, Completed: meta.Status == "completed"}
			child.SpawnKey = child.Session.ID
			child.Name = meta.Description
			child.Stopped = meta.Status == "failed" || meta.Status == "cancelled"
			var output struct {
				Output string `json:"output"`
			}
			output.Output = meta.Output
			if meta.dir != "" {
				readOptionalJSON(filepath.Join(meta.dir, "output.json"), &output)
			}
			child.Result = output.Output
			if model := nonEmpty(meta.EffectiveModelID); model != nil {
				child.Session.Model = model
			}
			if meta.DurationMs != nil {
				child.Session.DurationMs = meta.DurationMs
			}
			child.Session.ToolUses = max(child.Session.ToolUses, meta.ToolCalls)
			parent.Session.Digest = append(parent.Session.Digest, session.DigestEntry{
				Turn: digestTurn(parent.Session.Digest), Category: session.DigestSubagent, Description: meta.Description, SpawnKey: child.Session.ID,
			})
		}
	}
}
