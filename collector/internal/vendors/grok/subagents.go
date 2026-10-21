package grok

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
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

func readSubagentMetas(ctx context.Context, sessionDir string, withDetails bool) []subagentMeta {
	metas, _ := readSubagentMetasWithTranscriptLimit(ctx, sessionDir, withDetails, 0)
	return metas
}

func readSubagentMetasWithTranscriptLimit(ctx context.Context, sessionDir string, withDetails bool, maxTranscriptBytes int64) ([]subagentMeta, map[string]bool) {
	entries, _ := os.ReadDir(filepath.Join(sessionDir, "subagents"))
	metas := []subagentMeta{}
	missing := map[string]bool{}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return metas, nil
		}
		meta := subagentMeta{dir: filepath.Join(sessionDir, "subagents", entry.Name())}
		if entry.IsDir() && readJSON(filepath.Join(meta.dir, "meta.json"), &meta) == nil && meta.ChildSessionID != "" {
			if !withDetails {
				meta = subagentMeta{ChildSessionID: meta.ChildSessionID}
			}
			metas = append(metas, meta)
		} else if entry.IsDir() {
			missing[entry.Name()] = true
		}
	}
	if len(missing) == 0 {
		return metas, nil
	}
	// Grok can create the child directory but fail to write meta.json on long Windows paths.
	updatesPath := filepath.Join(sessionDir, "updates.jsonl")
	if maxTranscriptBytes > 0 {
		if info, err := os.Stat(updatesPath); err == nil && info.Size() > maxTranscriptBytes {
			return metas, missing
		}
	}
	file, err := os.Open(updatesPath)
	if err != nil {
		return metas, nil
	}
	defer file.Close()
	summary, summaryErr := readSummary(sessionDir)
	if summaryErr != nil {
		return metas, nil
	}
	metas, limited := scanSubagentUpdates(ctx, sessionDir, withDetails, entries, missing, metas, summary, file, maxTranscriptBytes)
	if limited {
		return metas, missing
	}
	return metas, nil
}

func scanSubagentUpdates(ctx context.Context, sessionDir string, withDetails bool, entries []os.DirEntry, missing map[string]bool, metas []subagentMeta, summary *summaryFile, file *os.File, maxTranscriptBytes int64) ([]subagentMeta, bool) {
	if ctx.Err() != nil {
		return metas, false
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
	var limitedReader *io.LimitedReader
	var reader io.Reader = file
	if maxTranscriptBytes > 0 {
		limitedReader = &io.LimitedReader{R: file, N: maxTranscriptBytes + 1}
		reader = limitedReader
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), int(maxGrokJSONBytes))
	for scanner.Scan() {
		if ctx.Err() != nil {
			return metas, false
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
					meta.dir = filepath.Join(sessionDir, "subagents", update.ID)
					meta.EffectiveModelID = update.Model
					if !withDetails {
						meta = subagentMeta{ChildSessionID: meta.ChildSessionID}
					}
					recovered[update.ID], attempts[update.ID] = meta, update.AttemptID
				}
			case "subagent_finished":
				meta, ok := recovered[update.ID]
				if withDetails && ok && meta.ChildSessionID == update.ChildSessionID && attempts[update.ID] == update.AttemptID {
					meta.Status, meta.DurationMs, meta.ToolCalls, meta.Output = update.Status, update.DurationMs, update.ToolCalls, update.Output
					recovered[update.ID] = meta
				}
			}
		}
	}
	if limitedReader != nil && limitedReader.N == 0 {
		return metas, true
	}
	if maxTranscriptBytes > 0 {
		if info, err := file.Stat(); err == nil && info.Size() > maxTranscriptBytes {
			return metas, true
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
	return metas, false
}

// attachSubagents links children using parent metadata or structured spawn events.
func attachSubagents(parsed []*vendors.ParsedSession, metasByDir map[string][]subagentMeta) {
	byID := make(map[string]*vendors.ParsedSession, len(parsed))
	for _, item := range parsed {
		byID[item.Session.ID] = item
	}
	for _, parent := range parsed {
		for _, meta := range metasByDir[filepath.Dir(parent.LogPath)] {
			child := byID[meta.ChildSessionID]
			if child == nil || (child.ParentID != "" && child.ParentID != parent.Session.ID) {
				continue
			}
			// The child summary often omits parent_session_id.
			summary, err := readSummary(filepath.Dir(child.LogPath))
			if err != nil || !isSubagentKind(summary.SessionKind) {
				continue
			}
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
