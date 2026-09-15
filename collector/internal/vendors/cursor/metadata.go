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
	"regexp"
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
	statePath := filepath.Join(globalStorage, "state.vscdb")
	stateDB, stateErr := openCursorDB(statePath)
	if stateErr != nil && !os.IsNotExist(stateErr) {
		log.Printf("Cursor metadata %q: %v", statePath, stateErr)
	}
	if stateDB != nil {
		defer stateDB.Close()
		loadCursorRowsDB(metadata, lanes, entrypointIDE, statePath, stateDB, `SELECT composerId, value FROM composerHeaders`, func(id, value string) (string, string, string) {
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
				metadata.Session(id).WorkingDirectory = cwd
			}
			return id, header.Name, header.Subtitle
		})
		loadIDETimes(metadata, stateDB)
		loadIDEDiffs(metadata, stateDB)
		loadIDECommitObservations(metadata, stateDB)
		loadIDEModelsDB(metadata, stateDB)
	}
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
				metadata.Session(item.AgentID).Model = normalizeCursorModel(item.LastUsedModel)
				setCursorTimes(metadata, item.AgentID, item.CreatedAt, 0)
			}
			return item.AgentID, item.Name, ""
		})
	}

	sdkStores, _ := filepath.Glob(filepath.Join(home, ".cursor", "projects", "*", "sdk-agent-store", "*", "index.db"))
	for _, path := range sdkStores {
		db, err := openCursorDB(path)
		if err != nil {
			continue
		}
		loadCursorRowsDB(metadata, lanes, entrypointSDK, path, db, `SELECT agent_id, name FROM agents`, func(id, name string) (string, string, string) {
			return sdkTranscriptID(id), name, ""
		})
		loadSDKTimes(metadata, db)
		loadSDKUsage(metadata, db)
		db.Close()
	}
	loadCursorSummaries(metadata, filepath.Join(home, ".cursor", "ai-tracking", "ai-code-tracking.db"))
	for id, matches := range lanes {
		if len(matches) != 1 {
			entry := metadata.Session(id)
			entry.Model, entry.WorkingDirectory = "", ""
			entry.PullRequests, entry.StartedAt, entry.LastActivityAt = 0, 0, 0
			entry.Usage = vendors.SessionUsage{}
			entry.FileEdits, entry.CommitObservations = nil, nil
			continue
		}
		for lane := range matches {
			metadata.Session(id).Entrypoint = lane
		}
	}
	return metadata, nil
}

var fullCommitHash = regexp.MustCompile(`^[0-9a-fA-F]{40,64}$`)

func loadIDECommitObservations(metadata *vendors.SessionMetadata, db *sql.DB) {
	rows, err := db.Query(`SELECT key, value FROM cursorDiskKV WHERE key LIKE 'bubbleId:%'`)
	if err != nil {
		return
	}
	defer rows.Close()
	seen := map[string]map[string]bool{}
	for rows.Next() {
		var key, value string
		if rows.Scan(&key, &value) != nil {
			continue
		}
		parts := strings.SplitN(key, ":", 3)
		if len(parts) != 3 || !transcriptIDPattern.MatchString(parts[1]) {
			continue
		}
		for _, observation := range commitObservationsFromIDEBubble(value) {
			if seen[parts[1]] == nil {
				seen[parts[1]] = map[string]bool{}
			}
			if !seen[parts[1]][observation.Hash] {
				seen[parts[1]][observation.Hash] = true
				entry := metadata.Session(parts[1])
				entry.CommitObservations = append(entry.CommitObservations, observation)
			}
		}
	}
}

func commitObservationsFromIDEBubble(value string) []session.CommitObservation {
	type checkpoint struct {
		CommitHashesByGitWorkspace map[string]struct {
			CommitHash string `json:"commitHash"`
		} `json:"commitHashesByGitWorkspace"`
	}
	var bubble struct {
		Before checkpoint `json:"gitCheckpoint"`
		After  checkpoint `json:"afterGitCheckpoint"`
	}
	if json.Unmarshal([]byte(value), &bubble) != nil {
		return nil
	}
	observations := []session.CommitObservation{}
	for workspace, after := range bubble.After.CommitHashesByGitWorkspace {
		before, ok := bubble.Before.CommitHashesByGitWorkspace[workspace]
		if ok && before.CommitHash != after.CommitHash && fullCommitHash.MatchString(after.CommitHash) {
			observations = append(observations, session.CommitObservation{Hash: after.CommitHash, Subject: "(commit)"})
		}
	}
	return observations
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

func loadIDEDiffs(metadata *vendors.SessionMetadata, db *sql.DB) {
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
		metadata.Session(id).FileEdits = checkpointFileEdits(checkpoint)
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

func loadIDETimes(metadata *vendors.SessionMetadata, db *sql.DB) {
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

func loadSDKTimes(metadata *vendors.SessionMetadata, db *sql.DB) {
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
		metadata.Session(id).StartedAt = startedAt
	}
	if lastActivityAt > 0 && (startedAt <= 0 || lastActivityAt >= startedAt) {
		metadata.Session(id).LastActivityAt = lastActivityAt
	}
}

func loadIDEModelsDB(metadata *vendors.SessionMetadata, db *sql.DB) {
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
			usage := metadata.Session(id).Usage
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
			metadata.Session(id).Usage = usage
		}
	}
	for id, model := range fallbacks {
		if bubbles[id].model == "" && model != "" {
			metadata.Session(id).Model = normalizeCursorModel(model)
		}
	}
	for id, value := range bubbles {
		metadata.Session(id).Model = normalizeCursorModel(value.model)
	}
	for id, urls := range pullRequests {
		metadata.Session(id).PullRequests = len(urls)
	}
}

func loadSDKUsage(metadata *vendors.SessionMetadata, db *sql.DB) {
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
			metadata.Session(id).Model = model
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
		value := metadata.Session(id).Usage
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
		metadata.Session(id).Usage = value
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
	loadCursorRowsDB(metadata, lanes, lane, path, db, query, decode)
}

func loadCursorRowsDB(metadata *vendors.SessionMetadata, lanes map[string]map[string]bool, lane, path string, db *sql.DB, query string, decode func(string, string) (string, string, string)) {
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
		if summary != "" && metadata.Session(id).Summary == "" {
			metadata.Session(id).Summary = summary
		}
		if name == "" || strings.EqualFold(name, "New Agent") || metadata.Session(id).Name != "" {
			continue
		}
		metadata.Session(id).Name = name
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
			metadata.Session(id).Summary = summary
		} else if summary := strings.TrimSpace(overview.String); overview.Valid && summary != "" {
			metadata.Session(id).Summary = summary
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
