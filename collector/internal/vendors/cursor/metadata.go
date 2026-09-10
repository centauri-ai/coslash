package cursor

import (
	"cmp"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	_ "modernc.org/sqlite"
)

const (
	entrypointIDE = "cursor-ide"
	entrypointCLI = "cursor-cli"
	entrypointSDK = "cursor-sdk"
)

func LoadMetadata() (*vendors.SessionMetadata, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return loadMetadata(home)
}

func loadMetadata(home string) (*vendors.SessionMetadata, error) {
	metadata := vendors.EmptySessionMetadata()
	lanes := map[string]map[string]bool{}
	globalStorage := filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage")
	loadCursorRows(metadata, lanes, entrypointIDE, filepath.Join(globalStorage, "state.vscdb"), `SELECT composerId, value FROM composerHeaders`, func(id, value string) (string, string, string) {
		var header struct {
			Name                string `json:"name"`
			Subtitle            string `json:"subtitle"`
			WorkspaceIdentifier struct {
				URI struct {
					FSPath string `json:"fsPath"`
				} `json:"uri"`
			} `json:"workspaceIdentifier"`
		}
		_ = json.Unmarshal([]byte(value), &header)
		if cwd := strings.TrimSpace(header.WorkspaceIdentifier.URI.FSPath); cwd != "" {
			metadata.WorkingDirectories[id] = cwd
		}
		return id, header.Name, header.Subtitle
	})
	loadIDETimes(metadata, filepath.Join(globalStorage, "state.vscdb"))
	loadIDEDiffs(metadata, filepath.Join(globalStorage, "state.vscdb"))
	loadIDEModels(metadata, filepath.Join(globalStorage, "state.vscdb"))
	loadIDERelationships(metadata, filepath.Join(globalStorage, "state.vscdb"))
	loadCursorRows(metadata, lanes, "", filepath.Join(globalStorage, "conversation-search.db"), `SELECT id, title FROM conversations ORDER BY source = 'local' DESC`, func(id, title string) (string, string, string) {
		return id, title, ""
	})

	chatStores, _ := filepath.Glob(filepath.Join(home, ".cursor", "chats", "*", "*", "store.db"))
	for _, path := range chatStores {
		loadCursorRows(metadata, lanes, entrypointCLI, path, `SELECT key, value FROM meta WHERE key = '0'`, func(_, value string) (string, string, string) {
			data, err := hex.DecodeString(value)
			if err != nil {
				return "", "", ""
			}
			var item struct {
				AgentID       string `json:"agentId"`
				Name          string `json:"name"`
				CreatedAt     int64  `json:"createdAt"`
				LastUsedModel string `json:"lastUsedModel"`
				SubagentInfo  struct {
					ParentAgentID string `json:"parentAgentId"`
					TypeName      string `json:"typeName"`
					ToolCallID    string `json:"toolCallId"`
				} `json:"subagentInfo"`
			}
			_ = json.Unmarshal(data, &item)
			if transcriptIDPattern.MatchString(item.AgentID) {
				metadata.Models[item.AgentID] = normalizeCursorModel(item.LastUsedModel)
				setCursorTimes(metadata, item.AgentID, item.CreatedAt, 0)
			}
			if transcriptIDPattern.MatchString(item.AgentID) && transcriptIDPattern.MatchString(item.SubagentInfo.ParentAgentID) {
				metadata.Relationships[item.AgentID] = vendors.SessionRelationship{
					ParentID: item.SubagentInfo.ParentAgentID,
					SpawnKey: item.SubagentInfo.ToolCallID,
					Task:     item.SubagentInfo.TypeName,
				}
			}
			return item.AgentID, item.Name, ""
		})
	}

	sdkStores, _ := filepath.Glob(filepath.Join(home, ".cursor", "projects", "*", "sdk-agent-store", "*", "index.db"))
	for _, path := range sdkStores {
		loadCursorRows(metadata, lanes, entrypointSDK, path, `SELECT agent_id, name FROM agents`, func(id, name string) (string, string, string) {
			return sdkTranscriptID(id), name, ""
		})
		loadSDKTimes(metadata, path)
		loadSDKUsage(metadata, path)
	}
	loadCursorSummaries(metadata, filepath.Join(home, ".cursor", "ai-tracking", "ai-code-tracking.db"))
	for id, matches := range lanes {
		if len(matches) != 1 {
			delete(metadata.Models, id)
			delete(metadata.PullRequests, id)
			delete(metadata.Usage, id)
			delete(metadata.WorkingDirectories, id)
			delete(metadata.StartedAt, id)
			delete(metadata.LastActivityAt, id)
			delete(metadata.FileEdits, id)
			continue
		}
		for lane := range matches {
			metadata.Entrypoints[id] = lane
		}
	}
	for id, lane := range loadLiveSessions() {
		if lane != "" && metadata.Entrypoints[id] == lane {
			metadata.Live[id] = "interactive"
		}
	}
	return metadata, nil
}

type ideCheckpoint struct {
	Files []struct {
		URI struct {
			FSPath string `json:"_fsPath"`
			Path   string `json:"path"`
		} `json:"uri"`
		Diff []struct {
			Original struct {
				Start int `json:"startLineNumber"`
				End   int `json:"endLineNumberExclusive"`
			} `json:"original"`
			Modified []string `json:"modified"`
		} `json:"originalModelDiffWrtV0"`
	} `json:"files"`
	NewResources struct {
		Files []struct {
			FSPath string `json:"_fsPath"`
			Path   string `json:"path"`
		} `json:"files"`
	} `json:"inlineDiffNewlyCreatedResources"`
}

func loadIDEDiffs(metadata *vendors.SessionMetadata, path string) {
	db, err := openCursorDB(path)
	if err != nil {
		return
	}
	defer db.Close()
	rows, err := db.Query(`SELECT key, value FROM cursorDiskKV WHERE key LIKE 'composerData:%'`)
	if err != nil {
		return
	}
	checkpoints := map[string]string{}
	for rows.Next() {
		var key, value string
		if rows.Scan(&key, &value) != nil {
			continue
		}
		id, ok := strings.CutPrefix(key, "composerData:")
		var item struct {
			LatestCheckpointID string `json:"latestCheckpointId"`
		}
		if ok && transcriptIDPattern.MatchString(id) && json.Unmarshal([]byte(value), &item) == nil && item.LatestCheckpointID != "" {
			checkpoints[id] = item.LatestCheckpointID
		}
	}
	rows.Close()
	for id, checkpointID := range checkpoints {
		var value string
		if db.QueryRow(`SELECT value FROM cursorDiskKV WHERE key = ?`, "checkpointId:"+id+":"+checkpointID).Scan(&value) != nil {
			continue
		}
		var checkpoint ideCheckpoint
		if json.Unmarshal([]byte(value), &checkpoint) != nil {
			continue
		}
		metadata.FileEdits[id] = checkpointFileEdits(checkpoint)
	}
}

func checkpointFileEdits(checkpoint ideCheckpoint) []session.FileEdit {
	newFiles := map[string]bool{}
	for _, file := range checkpoint.NewResources.Files {
		path := strings.TrimSpace(cmp.Or(file.FSPath, file.Path))
		if path != "" {
			newFiles[path] = true
		}
	}
	edits := session.NewFileEditSet()
	seen := map[string]bool{}
	for _, file := range checkpoint.Files {
		path := strings.TrimSpace(cmp.Or(file.URI.FSPath, file.URI.Path))
		if path == "" {
			continue
		}
		additions, deletions := 0, 0
		for _, diff := range file.Diff {
			if diff.Original.End < diff.Original.Start {
				continue
			}
			additions += len(diff.Modified)
			deletions += diff.Original.End - diff.Original.Start
		}
		edits.Add(path, additions, deletions, newFiles[path])
		seen[path] = true
	}
	for _, file := range checkpoint.NewResources.Files {
		path := strings.TrimSpace(cmp.Or(file.FSPath, file.Path))
		if newFiles[path] && !seen[path] {
			edits.Add(path, 0, 0, true)
			seen[path] = true
		}
	}
	return edits.Edits
}

func loadIDETimes(metadata *vendors.SessionMetadata, path string) {
	db, err := openCursorDB(path)
	if err != nil {
		return
	}
	defer db.Close()
	rows, err := db.Query(`SELECT composerId, createdAt, lastUpdatedAt FROM composerHeaders`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var created, updated sql.NullInt64
		if rows.Scan(&id, &created, &updated) == nil {
			setCursorTimes(metadata, id, created.Int64, updated.Int64)
		}
	}
}

func loadSDKTimes(metadata *vendors.SessionMetadata, path string) {
	db, err := openCursorDB(path)
	if err != nil {
		return
	}
	defer db.Close()
	rows, err := db.Query(`SELECT agent_id, created_at, updated_at FROM agents`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, created, updated string
		if rows.Scan(&id, &created, &updated) != nil {
			continue
		}
		var startedAt, lastActivityAt int64
		if value, ok := parseTimestamp(created); ok {
			startedAt = value.UnixMilli()
		}
		if value, ok := parseTimestamp(updated); ok {
			lastActivityAt = value.UnixMilli()
		}
		setCursorTimes(metadata, sdkTranscriptID(id), startedAt, lastActivityAt)
	}
}

func setCursorTimes(metadata *vendors.SessionMetadata, id string, startedAt, lastActivityAt int64) {
	if !transcriptIDPattern.MatchString(id) {
		return
	}
	if startedAt > 0 {
		metadata.StartedAt[id] = startedAt
	}
	if lastActivityAt > 0 && (startedAt <= 0 || lastActivityAt >= startedAt) {
		metadata.LastActivityAt[id] = lastActivityAt
	}
}

func loadIDERelationships(metadata *vendors.SessionMetadata, path string) {
	db, err := openCursorDB(path)
	if err != nil {
		return
	}
	defer db.Close()

	headers, err := db.Query(`SELECT composerId, value FROM composerHeaders`)
	if err != nil {
		return
	}
	for headers.Next() {
		var childID, value string
		if headers.Scan(&childID, &value) != nil || !transcriptIDPattern.MatchString(childID) {
			continue
		}
		var header struct {
			SubagentInfo struct {
				ParentComposerID string `json:"parentComposerId"`
				ToolCallID       string `json:"toolCallId"`
			} `json:"subagentInfo"`
		}
		if json.Unmarshal([]byte(value), &header) != nil || !transcriptIDPattern.MatchString(header.SubagentInfo.ParentComposerID) {
			continue
		}
		metadata.Relationships[childID] = vendors.SessionRelationship{
			ParentID: header.SubagentInfo.ParentComposerID,
			SpawnKey: header.SubagentInfo.ToolCallID,
		}
	}
	headers.Close()

	bubbles, err := db.Query(`SELECT key, value FROM cursorDiskKV WHERE key LIKE 'bubbleId:%'`)
	if err != nil {
		return
	}
	defer bubbles.Close()
	for bubbles.Next() {
		var key, value string
		if bubbles.Scan(&key, &value) != nil {
			continue
		}
		parts := strings.SplitN(key, ":", 3)
		if len(parts) != 3 || parts[0] != "bubbleId" || !transcriptIDPattern.MatchString(parts[1]) {
			continue
		}
		var bubble struct {
			CreatedAt      string `json:"createdAt"`
			ToolFormerData struct {
				Name       string `json:"name"`
				ToolCallID string `json:"toolCallId"`
				Status     string `json:"status"`
				Params     string `json:"params"`
				Result     string `json:"result"`
			} `json:"toolFormerData"`
		}
		if json.Unmarshal([]byte(value), &bubble) != nil || bubble.ToolFormerData.Name != "task_v2" {
			continue
		}
		var params struct {
			Description string `json:"description"`
		}
		var result struct {
			AgentID string `json:"agentId"`
		}
		if json.Unmarshal([]byte(bubble.ToolFormerData.Params), &params) != nil {
			continue
		}
		if raw := bubble.ToolFormerData.Result; raw != "" && json.Unmarshal([]byte(raw), &result) != nil {
			continue
		}
		if result.AgentID == "" {
			// A running task has no result yet. Only one exact header join
			// can identify its child; descriptions never establish parentage.
			if bubble.ToolFormerData.Status != "running" || bubble.ToolFormerData.ToolCallID == "" {
				continue
			}
			for childID, candidate := range metadata.Relationships {
				if candidate.ParentID != parts[1] || candidate.SpawnKey != bubble.ToolFormerData.ToolCallID {
					continue
				}
				if result.AgentID != "" {
					result.AgentID = ""
					break
				}
				result.AgentID = childID
			}
		}
		if !transcriptIDPattern.MatchString(result.AgentID) {
			continue
		}
		relationship, ok := metadata.Relationships[result.AgentID]
		if !ok || relationship.ParentID != parts[1] || relationship.SpawnKey != bubble.ToolFormerData.ToolCallID {
			continue
		}
		relationship.Task = params.Description
		if createdAt, ok := parseTimestamp(bubble.CreatedAt); ok {
			relationship.Time = createdAt.UnixMilli()
		}
		relationship.Completed = bubble.ToolFormerData.Status == "completed"
		relationship.Active = bubble.ToolFormerData.Status == "running"
		metadata.Relationships[result.AgentID] = relationship
	}
}

func loadIDEModels(metadata *vendors.SessionMetadata, path string) {
	db, err := openCursorDB(path)
	if err != nil {
		return
	}
	defer db.Close()
	rows, err := db.Query(`SELECT key, value FROM cursorDiskKV WHERE key LIKE 'bubbleId:%' OR key LIKE 'composerData:%'`)
	if err != nil {
		return
	}
	defer rows.Close()
	type observed struct {
		model string
		time  string
	}
	bubbles := map[string]observed{}
	fallbacks := map[string]string{}
	pullRequests := map[string]map[string]struct{}{}
	for rows.Next() {
		var key, value string
		if rows.Scan(&key, &value) != nil {
			continue
		}
		var item struct {
			CreatedAt any `json:"createdAt"`
			ModelInfo struct {
				ModelName string `json:"modelName"`
			} `json:"modelInfo"`
			ModelConfig struct {
				ModelName string `json:"modelName"`
			} `json:"modelConfig"`
			ToolFormerData struct {
				Status string `json:"status"`
				Result string `json:"result"`
			} `json:"toolFormerData"`
			UsageData map[string]struct {
				CostInCents *float64 `json:"costInCents"`
			} `json:"usageData"`
			ContextTokensUsed *int `json:"contextTokensUsed"`
			ContextTokenLimit *int `json:"contextTokenLimit"`
		}
		if json.Unmarshal([]byte(value), &item) != nil {
			continue
		}
		if parts := strings.SplitN(key, ":", 3); len(parts) == 3 && parts[0] == "bubbleId" && transcriptIDPattern.MatchString(parts[1]) {
			id := parts[1]
			model := strings.TrimSpace(item.ModelInfo.ModelName)
			createdAt, _ := item.CreatedAt.(string)
			if model != "" && createdAt >= bubbles[id].time {
				bubbles[id] = observed{model: model, time: createdAt}
			}
			if item.ToolFormerData.Status == "completed" {
				if pullRequests[id] == nil {
					pullRequests[id] = map[string]struct{}{}
				}
				for _, url := range session.PullRequestURLs(item.ToolFormerData.Result) {
					pullRequests[id][url] = struct{}{}
				}
			}
		} else if id, ok := strings.CutPrefix(key, "composerData:"); ok && transcriptIDPattern.MatchString(id) {
			fallbacks[id] = strings.TrimSpace(item.ModelConfig.ModelName)
			usage := metadata.Usage[id]
			if item.ContextTokensUsed != nil && *item.ContextTokensUsed >= 0 {
				usage.ContextTokens = item.ContextTokensUsed
			}
			if item.ContextTokenLimit != nil && *item.ContextTokenLimit > 0 {
				usage.ContextWindow = item.ContextTokenLimit
			}
			cost, valid := 0.0, len(item.UsageData) > 0
			for _, usage := range item.UsageData {
				if usage.CostInCents == nil || *usage.CostInCents < 0 {
					valid = false
					break
				}
				cost += *usage.CostInCents / 100
			}
			if valid {
				usage.RecordedCost = &cost
			}
			metadata.Usage[id] = usage
		}
	}
	for id, model := range fallbacks {
		if bubbles[id].model == "" && model != "" {
			metadata.Models[id] = normalizeCursorModel(model)
		}
	}
	for id, value := range bubbles {
		metadata.Models[id] = normalizeCursorModel(value.model)
	}
	for id, usage := range metadata.Usage {
		model := metadata.Models[id]
		if usage.ContextTokens == nil || model == "" || session.ContextWindowFor(model) == nil || len(usage.Tokens) > 0 {
			continue
		}
		usage.Tokens = map[string]session.ModelTokens{
			model: {InputTokens: *usage.ContextTokens},
		}
		metadata.Usage[id] = usage
	}
	for id, urls := range pullRequests {
		metadata.PullRequests[id] = len(urls)
	}
}

func loadSDKUsage(metadata *vendors.SessionMetadata, path string) {
	db, err := openCursorDB(path)
	if err != nil {
		return
	}
	defer db.Close()
	rows, err := db.Query(`SELECT agent_id, COALESCE(model, ''), usage_json FROM runs ORDER BY turn_number`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, model string
		var raw sql.NullString
		if rows.Scan(&id, &model, &raw) != nil {
			continue
		}
		id = sdkTranscriptID(id)
		if id == "" {
			continue
		}
		rawModel := strings.TrimSpace(model)
		model = normalizeCursorModel(rawModel)
		if rawModel != "" {
			metadata.Models[id] = model
		}
		var usage struct {
			Input      int `json:"inputTokens"`
			Output     int `json:"outputTokens"`
			CacheRead  int `json:"cacheReadTokens"`
			CacheWrite int `json:"cacheWriteTokens"`
		}
		if !raw.Valid || json.Unmarshal([]byte(raw.String), &usage) != nil || usage.Input < 0 || usage.Output < 0 || usage.CacheRead < 0 || usage.CacheWrite < 0 {
			continue
		}
		value := metadata.Usage[id]
		context := session.ContextTokens(usage.Input, usage.CacheRead, usage.CacheWrite)
		value.ContextTokens = &context
		if model != "" {
			if value.Tokens == nil {
				value.Tokens = map[string]session.ModelTokens{}
			}
			tokens := value.Tokens[model]
			tokens.InputTokens += usage.Input
			tokens.OutputTokens += usage.Output
			tokens.CacheReadInputTokens += usage.CacheRead
			tokens.CacheCreationInputTokens += usage.CacheWrite
			value.Tokens[model] = tokens
		}
		metadata.Usage[id] = value
	}
}

func sdkTranscriptID(id string) string {
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, "agent-") {
		id = "agent-" + id
	}
	if !transcriptIDPattern.MatchString(id) {
		return ""
	}
	return id
}

func normalizeCursorModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "default" {
		return ""
	}
	if !strings.HasPrefix(model, "claude-") && !strings.HasPrefix(model, "gpt-") && !strings.HasPrefix(model, "grok-") {
		return model
	}

	parts := strings.Split(model, "-")
	for len(parts) > 1 && isCursorModelModifier(parts[len(parts)-1]) {
		parts = parts[:len(parts)-1]
	}
	if parts[0] == "claude" && len(parts) >= 3 && parts[1] != "" && parts[1][0] >= '0' && parts[1][0] <= '9' {
		parts[1], parts[2] = parts[2], strings.ReplaceAll(parts[1], ".", "-")
	}
	model = strings.Join(parts, "-")
	if strings.HasPrefix(model, "grok-") {
		return "xai/" + model
	}
	return model
}

func isCursorModelModifier(part string) bool {
	return part == "none" || part == "low" || part == "medium" || part == "high" || part == "xhigh" || part == "thinking"
}

func loadCursorRows(metadata *vendors.SessionMetadata, lanes map[string]map[string]bool, lane, path, query string, decode func(string, string) (string, string, string)) {
	db, err := openCursorDB(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		log.Printf("Cursor metadata %q: %v", path, err)
		return
	}
	defer db.Close()
	rows, err := db.Query(query)
	if err != nil {
		log.Printf("Cursor metadata %q: %v", path, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var first, second string
		if rows.Scan(&first, &second) != nil {
			continue
		}
		id, name, summary := decode(first, second)
		if transcriptIDPattern.MatchString(id) && lane != "" {
			if lanes[id] == nil {
				lanes[id] = map[string]bool{}
			}
			lanes[id][lane] = true
		}
		name = strings.TrimSpace(name)
		summary = strings.TrimSpace(summary)
		if !transcriptIDPattern.MatchString(id) {
			continue
		}
		if summary != "" && metadata.Summaries[id] == "" {
			metadata.Summaries[id] = summary
		}
		if name == "" || strings.EqualFold(name, "New Agent") || metadata.Names[id] != "" {
			continue
		}
		metadata.Names[id] = name
	}
}

func loadCursorSummaries(metadata *vendors.SessionMetadata, path string) {
	db, err := openCursorDB(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		log.Printf("Cursor metadata %q: %v", path, err)
		return
	}
	defer db.Close()
	rows, err := db.Query(`SELECT conversationId, tldr, overview FROM conversation_summaries`)
	if err != nil {
		log.Printf("Cursor metadata %q: %v", path, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var tldr, overview sql.NullString
		if rows.Scan(&id, &tldr, &overview) != nil || !transcriptIDPattern.MatchString(id) {
			continue
		}
		if summary := strings.TrimSpace(tldr.String); tldr.Valid && summary != "" {
			metadata.Summaries[id] = summary
		} else if summary := strings.TrimSpace(overview.String); overview.Valid && summary != "" {
			metadata.Summaries[id] = summary
		}
	}
}

func openCursorDB(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: url.Values{
		"mode": {"ro"}, "_query_only": {"1"}, "_busy_timeout": {"1000"},
	}.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
