package grok

import (
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
}

func readSubagentMetas(sessionDir string) []subagentMeta {
	entries, _ := os.ReadDir(filepath.Join(sessionDir, "subagents"))
	metas := []subagentMeta{}
	for _, entry := range entries {
		meta := subagentMeta{dir: filepath.Join(sessionDir, "subagents", entry.Name())}
		if entry.IsDir() && readJSON(filepath.Join(meta.dir, "meta.json"), &meta) == nil && meta.ChildSessionID != "" {
			metas = append(metas, meta)
		}
	}
	return metas
}

// attachSubagents links each child session to the parent meta.json that spawned it.
func attachSubagents(parsed []*vendors.ParsedSession) {
	byID := make(map[string]*vendors.ParsedSession, len(parsed))
	for _, item := range parsed {
		byID[item.Session.ID] = item
	}
	for _, parent := range parsed {
		if parent.ParentID != "" {
			continue
		}
		for _, meta := range readSubagentMetas(filepath.Dir(parent.LogPath)) {
			child := byID[meta.ChildSessionID]
			if child == nil || (child.ParentID != "" && child.ParentID != parent.Session.ID) {
				continue
			}
			// The child summary often omits parent_session_id. The parent's meta.json is the link.
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
			readOptionalJSON(filepath.Join(meta.dir, "output.json"), &output)
			child.Result = output.Output
			if model := nonEmpty(meta.EffectiveModelID); model != nil {
				child.Session.Model = model
			}
			if meta.DurationMs != nil {
				child.Session.DurationMs = meta.DurationMs
			}
			child.Session.ToolUses = max(child.Session.ToolUses, meta.ToolCalls)
			parent.Session.Digest = append(parent.Session.Digest, session.DigestEntry{
				Category: session.DigestSubagent, Description: meta.Description, SpawnKey: child.Session.ID,
			})
		}
	}
}
