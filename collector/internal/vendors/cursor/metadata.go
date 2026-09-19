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
	"strconv"
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

func LoadSelectionMetadata() (*vendors.SessionMetadata, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return loadSelectionMetadata(home)
}

func loadSelectionMetadata(home string) (*vendors.SessionMetadata, error) {
	metadata := vendors.EmptySessionMetadata()
	path := filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
	db, err := openCursorDB(path)
	if err != nil {
		if os.IsNotExist(err) {
			return metadata, nil
		}
		return nil, err
	}
	defer db.Close()
	loadIDERelationships(metadata, db, nil)
	loadIDETimes(metadata, db, nil)
	return metadata, nil
}

func loadMetadata(home string) (*vendors.SessionMetadata, error) {
	return loadMetadataForSessions(home, nil, nil)
}

func LoadMetadataForSessions(ids, transcriptPaths []string) (*vendors.SessionMetadata, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return loadMetadataForSessions(home, ids, transcriptPaths)
}

func loadMetadataForSessions(home string, ids, transcriptPaths []string) (*vendors.SessionMetadata, error) {
	ids = canonicalCursorIDs(ids)
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
		loadIDERelationships(metadata, stateDB, ids)
		ids = cursorMetadataIDs(ids, metadata)
		query, args := cursorIDQuery(`SELECT composerId, value FROM composerHeaders`, "composerId", ids)
		loadCursorRowsDB(metadata, lanes, entrypointIDE, statePath, stateDB, query, args, func(id, value string) (string, string, string) {
			id = canonicalCursorID(id)
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
		loadIDETimes(metadata, stateDB, ids)
		loadIDEDiffs(metadata, stateDB, ids)
		loadIDECommitObservations(metadata, stateDB, ids)
		loadIDEModelsDB(metadata, stateDB, ids)
	}
	query, args := cursorIDQuery(`SELECT id, title FROM conversations`, "id", ids)
	query += ` ORDER BY source = 'local' DESC`
	loadCursorRows(metadata, lanes, "", filepath.Join(globalStorage, "conversation-search.db"), query, args, func(id, title string) (string, string, string) {
		return id, title, ""
	})

	chatStores := cursorChatStores(home, ids)
	for _, path := range chatStores {
		loadCursorRows(metadata, lanes, entrypointCLI, path, `SELECT key, value FROM meta WHERE key = '0'`, nil, func(_, value string) (string, string, string) {
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
			id := canonicalCursorID(item.AgentID)
			if transcriptIDPattern.MatchString(id) {
				metadata.Session(id).Model = normalizeCursorModel(item.LastUsedModel)
				setCursorTimes(metadata, id, item.CreatedAt, 0)
			}
			parentID := canonicalCursorID(item.SubagentInfo.ParentAgentID)
			if transcriptIDPattern.MatchString(id) && transcriptIDPattern.MatchString(parentID) {
				metadata.Session(id).Relationship = vendors.SessionRelationship{ParentID: parentID, SpawnKey: item.SubagentInfo.ToolCallID, Task: item.SubagentInfo.TypeName}
			}
			return id, item.Name, ""
		})
	}

	sdkStores := cursorSDKStores(home, ids, transcriptPaths)
	sdkIDs := rawSDKIDs(ids)
	for _, path := range sdkStores {
		db, err := openCursorDB(path)
		if err != nil {
			continue
		}
		query, args := cursorIDQuery(`SELECT agent_id, name FROM agents`, "agent_id", sdkIDs)
		loadCursorRowsDB(metadata, lanes, entrypointSDK, path, db, query, args, func(id, name string) (string, string, string) {
			return sdkTranscriptID(id), name, ""
		})
		loadSDKTimes(metadata, db, sdkIDs)
		loadSDKUsage(metadata, db, sdkIDs)
		db.Close()
	}
	loadCursorSummaries(metadata, filepath.Join(home, ".cursor", "ai-tracking", "ai-code-tracking.db"), ids)
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

func canonicalCursorIDs(ids []string) []string {
	if ids == nil {
		return nil
	}
	result := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = canonicalCursorID(id)
		if id != "" && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result
}

func canonicalCursorID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func cursorMetadataIDs(ids []string, metadata *vendors.SessionMetadata) []string {
	if ids == nil {
		return nil
	}
	for id, entry := range metadata.Sessions {
		ids = append(ids, id, entry.Relationship.ParentID)
	}
	return canonicalCursorIDs(ids)
}

func cursorIDQuery(query, column string, ids []string) (string, []any) {
	if ids == nil {
		return query, nil
	}
	if len(ids) == 0 {
		return query + " WHERE 0", nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return query + " WHERE LOWER(" + column + ") IN (" + placeholders + ")", args
}

func cursorKeyQuery(query, prefix string, ids []string) (string, []any) {
	if ids == nil {
		return query + " WHERE LOWER(key) LIKE ?", []any{strings.ToLower(prefix) + "%"}
	}
	if len(ids) == 0 {
		return query + " WHERE 0", nil
	}
	clauses := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		clauses[i] = "LOWER(key) LIKE ?"
		args[i] = strings.ToLower(prefix) + id + "%"
	}
	return query + " WHERE " + strings.Join(clauses, " OR "), args
}

func cursorChatStores(home string, ids []string) []string {
	if ids == nil {
		stores, _ := filepath.Glob(filepath.Join(home, ".cursor", "chats", "*", "*", "store.db"))
		return stores
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		if !strings.HasPrefix(id, "agent-") {
			wanted[id] = true
		}
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".cursor", "chats", "*", "*", "store.db"))
	stores := make([]string, 0, len(matches))
	for _, path := range matches {
		if wanted[canonicalCursorID(filepath.Base(filepath.Dir(path)))] {
			stores = append(stores, path)
		}
	}
	return stores
}

func rawSDKIDs(ids []string) []string {
	if ids == nil {
		return nil
	}
	result := []string{}
	for _, id := range ids {
		if raw, ok := strings.CutPrefix(id, "agent-"); ok {
			result = append(result, raw)
		}
	}
	return result
}

func cursorSDKStores(home string, ids, transcriptPaths []string) []string {
	if ids == nil {
		stores, _ := filepath.Glob(filepath.Join(home, ".cursor", "projects", "*", "sdk-agent-store", "*", "index.db"))
		return stores
	}
	if len(rawSDKIDs(ids)) == 0 {
		return nil
	}
	seen := map[string]bool{}
	stores := []string{}
	for _, path := range transcriptPaths {
		for directory := filepath.Dir(path); directory != filepath.Dir(directory); directory = filepath.Dir(directory) {
			if filepath.Base(directory) != "agent-transcripts" {
				continue
			}
			matches, _ := filepath.Glob(filepath.Join(filepath.Dir(directory), "sdk-agent-store", "*", "index.db"))
			for _, match := range matches {
				if !seen[match] {
					seen[match] = true
					stores = append(stores, match)
				}
			}
			break
		}
	}
	return stores
}

func loadIDERelationships(metadata *vendors.SessionMetadata, db *sql.DB, ids []string) {
	familyIDs := canonicalCursorIDs(ids)
	for {
		query := `SELECT composerId, value FROM composerHeaders`
		args := []any(nil)
		if familyIDs != nil {
			if len(familyIDs) == 0 {
				query += ` WHERE 0`
			} else {
				placeholders := strings.TrimSuffix(strings.Repeat("?,", len(familyIDs)), ",")
				query += ` WHERE LOWER(composerId) IN (` + placeholders + `) OR LOWER(CASE WHEN json_valid(value) THEN json_extract(value, '$.subagentInfo.parentComposerId') ELSE '' END) IN (` + placeholders + `)`
				for _, id := range familyIDs {
					args = append(args, id)
				}
				for _, id := range familyIDs {
					args = append(args, id)
				}
			}
		}
		rows, err := db.Query(query, args...)
		if err != nil {
			return
		}
		changed := false
		known := map[string]bool{}
		for _, id := range familyIDs {
			known[id] = true
		}
		for rows.Next() {
			var childID, value string
			if rows.Scan(&childID, &value) != nil {
				continue
			}
			var header struct {
				SubagentInfo struct {
					ParentComposerID string `json:"parentComposerId"`
					ToolCallID       string `json:"toolCallId"`
				} `json:"subagentInfo"`
			}
			if json.Unmarshal([]byte(value), &header) != nil {
				continue
			}
			childID = canonicalCursorID(childID)
			parentID := canonicalCursorID(header.SubagentInfo.ParentComposerID)
			if !transcriptIDPattern.MatchString(childID) || !transcriptIDPattern.MatchString(parentID) {
				continue
			}
			metadata.Session(childID).Relationship = vendors.SessionRelationship{ParentID: parentID, SpawnKey: header.SubagentInfo.ToolCallID}
			metadata.Session(parentID)
			for _, id := range []string{childID, parentID} {
				if !known[id] {
					known[id], changed = true, true
					familyIDs = append(familyIDs, id)
				}
			}
		}
		rows.Close()
		if ids == nil || !changed {
			break
		}
	}
	parentIDs := append([]string(nil), familyIDs...)
	for _, entry := range metadata.Sessions {
		if entry.Relationship.ParentID != "" {
			parentIDs = append(parentIDs, entry.Relationship.ParentID)
		}
	}
	bubbleIDs := canonicalCursorIDs(parentIDs)
	if ids == nil {
		bubbleIDs = nil
	}
	query, args := cursorKeyQuery(`SELECT key, value FROM cursorDiskKV`, "bubbleId:", bubbleIDs)
	rows2, err := db.Query(query, args...)
	if err != nil {
		return
	}
	defer rows2.Close()
	for rows2.Next() {
		var key, value string
		if rows2.Scan(&key, &value) != nil {
			continue
		}
		parts := strings.SplitN(key, ":", 3)
		if len(parts) != 3 || !transcriptIDPattern.MatchString(parts[1]) {
			continue
		}
		parentID := canonicalCursorID(parts[1])
		var bubble struct {
			CreatedAt      string                                                    `json:"createdAt"`
			ToolFormerData struct{ Name, ToolCallID, Status, Params, Result string } `json:"toolFormerData"`
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
		if bubble.ToolFormerData.Result != "" && json.Unmarshal([]byte(bubble.ToolFormerData.Result), &result) != nil {
			continue
		}
		childID := canonicalCursorID(result.AgentID)
		if !transcriptIDPattern.MatchString(childID) && bubble.ToolFormerData.Status == "running" {
			for candidateID, entry := range metadata.Sessions {
				rel := entry.Relationship
				if rel.ParentID == parentID && rel.SpawnKey == bubble.ToolFormerData.ToolCallID {
					if childID != "" {
						childID = ""
						break
					}
					childID = candidateID
				}
			}
		}
		if !transcriptIDPattern.MatchString(childID) {
			continue
		}
		rel := metadata.Session(childID).Relationship
		if rel.ParentID != parentID || rel.SpawnKey != bubble.ToolFormerData.ToolCallID {
			continue
		}
		rel.Task = params.Description
		if at, ok := parseTimestamp(bubble.CreatedAt); ok {
			rel.Time = at.UnixMilli()
		}
		rel.Completed = bubble.ToolFormerData.Status == "completed"
		rel.Active = bubble.ToolFormerData.Status == "running"
		metadata.Session(childID).Relationship = rel
	}
}

var fullCommitHash = regexp.MustCompile(`^[0-9a-fA-F]{40,64}$`)

func loadIDECommitObservations(metadata *vendors.SessionMetadata, db *sql.DB, ids []string) {
	query, args := cursorKeyQuery(`SELECT key, value FROM cursorDiskKV`, "bubbleId:", ids)
	rows, err := db.Query(query, args...)
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
		id := canonicalCursorID(parts[1])
		for _, observation := range commitObservationsFromIDEBubble(value) {
			if seen[id] == nil {
				seen[id] = map[string]bool{}
			}
			if !seen[id][observation.Hash] {
				seen[id][observation.Hash] = true
				entry := metadata.Session(id)
				entry.CommitObservations = append(entry.CommitObservations, observation)
			}
		}
	}
}

func completedIDETerminalCommand(name, status string, raw json.RawMessage) (string, bool) {
	if status != "completed" || (name != "run_terminal_cmd" && name != "run_terminal_command_v2") {
		return "", false
	}
	var rawArgs string
	if json.Unmarshal(raw, &rawArgs) != nil {
		rawArgs = string(raw)
	}
	var args struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(rawArgs), &args) != nil {
		return "", false
	}
	return args.Command, true
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
		Tool   struct {
			Name    string          `json:"name"`
			Status  string          `json:"status"`
			RawArgs json.RawMessage `json:"rawArgs"`
			Result  string          `json:"result"`
		} `json:"toolFormerData"`
	}
	if json.Unmarshal([]byte(value), &bubble) != nil {
		return nil
	}
	command, ok := completedIDETerminalCommand(bubble.Tool.Name, bubble.Tool.Status, bubble.Tool.RawArgs)
	if !ok {
		return nil
	}
	attempts := session.ParseCommitObservations(command, bubble.Tool.Result, true)
	observations := []session.CommitObservation{}
	for workspace, after := range bubble.After.CommitHashesByGitWorkspace {
		before, ok := bubble.Before.CommitHashesByGitWorkspace[workspace]
		if !ok || before.CommitHash == after.CommitHash || !fullCommitHash.MatchString(after.CommitHash) {
			continue
		}
		for _, attempt := range attempts {
			if attempt.Hash != "" && strings.HasPrefix(strings.ToLower(after.CommitHash), strings.ToLower(attempt.Hash)) {
				attempt.Hash = after.CommitHash
				observations = append(observations, attempt)
				break
			}
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

func loadIDEDiffs(metadata *vendors.SessionMetadata, db *sql.DB, ids []string) {
	query, args := cursorKeyQuery(`SELECT key, value FROM cursorDiskKV`, "composerData:", ids)
	rows, err := db.Query(query, args...)
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
		id = canonicalCursorID(id)
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
		if db.QueryRow(`SELECT value FROM cursorDiskKV WHERE LOWER(key) = ?`, strings.ToLower("checkpointId:"+id+":"+checkpointID)).Scan(&value) != nil {
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

func loadIDETimes(metadata *vendors.SessionMetadata, db *sql.DB, ids []string) {
	query, args := cursorIDQuery(`SELECT composerId, createdAt, lastUpdatedAt FROM composerHeaders`, "composerId", ids)
	rows, err := db.Query(query, args...)
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

func loadSDKTimes(metadata *vendors.SessionMetadata, db *sql.DB, ids []string) {
	query, args := cursorIDQuery(`SELECT agent_id, created_at, updated_at FROM agents`, "agent_id", ids)
	rows, err := db.Query(query, args...)
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
	id = canonicalCursorID(id)
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

// Each ID adds two OR terms; stay below SQLite's default expression depth.
const maxIDEModelQueryIDs = 400

func loadIDEModelsDB(metadata *vendors.SessionMetadata, db *sql.DB, ids []string) {
	if len(ids) > maxIDEModelQueryIDs {
		for start := 0; start < len(ids); start += maxIDEModelQueryIDs {
			loadIDEModelsDB(metadata, db, ids[start:min(start+maxIDEModelQueryIDs, len(ids))])
		}
		return
	}
	query := `SELECT key, value FROM cursorDiskKV WHERE key LIKE 'bubbleId:%' OR key LIKE 'composerData:%'`
	args := []any(nil)
	if ids != nil {
		if len(ids) == 0 {
			query = `SELECT key, value FROM cursorDiskKV WHERE 0`
		}
		clauses := make([]string, 0, len(ids)*2)
		for _, id := range ids {
			clauses = append(clauses, "LOWER(key) LIKE ?", "LOWER(key) = ?")
			args = append(args, "bubbleid:"+id+":%", "composerdata:"+id)
		}
		if len(clauses) > 0 {
			query = `SELECT key, value FROM cursorDiskKV WHERE ` + strings.Join(clauses, " OR ")
		}
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return
	}
	defer rows.Close()
	type observed struct {
		model string
		time  float64
		key   string
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
			CreatedAt json.RawMessage `json:"createdAt"`
			ModelInfo struct {
				ModelName string `json:"modelName"`
			} `json:"modelInfo"`
			ModelConfig struct {
				ModelName string `json:"modelName"`
			} `json:"modelConfig"`
			ToolFormerData struct {
				Name    string          `json:"name"`
				Status  string          `json:"status"`
				RawArgs json.RawMessage `json:"rawArgs"`
				Result  string          `json:"result"`
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
			id := canonicalCursorID(parts[1])
			model := strings.TrimSpace(item.ModelInfo.ModelName)
			createdAt := cursorBubbleTime(item.CreatedAt)
			previous := bubbles[id]
			if model != "" && (previous.model == "" || createdAt > previous.time || createdAt == previous.time && key > previous.key) {
				bubbles[id] = observed{model: model, time: createdAt, key: key}
			}
			command, completed := completedIDETerminalCommand(item.ToolFormerData.Name, item.ToolFormerData.Status, item.ToolFormerData.RawArgs)
			if completed && session.IsPullRequestCreate(command) {
				if pullRequests[id] == nil {
					pullRequests[id] = map[string]struct{}{}
				}
				for _, url := range session.PullRequestURLs(item.ToolFormerData.Result) {
					pullRequests[id][url] = struct{}{}
				}
			}
		} else if id, ok := strings.CutPrefix(key, "composerData:"); ok && transcriptIDPattern.MatchString(id) {
			id = canonicalCursorID(id)
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

func cursorBubbleTime(raw json.RawMessage) float64 {
	var number float64
	if json.Unmarshal(raw, &number) == nil {
		return number
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return 0
	}
	if parsed, ok := parseTimestamp(value); ok {
		return float64(parsed.UnixMilli())
	}
	number, _ = strconv.ParseFloat(strings.TrimSpace(value), 64)
	return number
}

func loadSDKUsage(metadata *vendors.SessionMetadata, db *sql.DB, ids []string) {
	query, args := cursorIDQuery(`SELECT agent_id, COALESCE(model, ''), usage_json FROM runs`, "agent_id", ids)
	query += ` ORDER BY turn_number`
	rows, err := db.Query(query, args...)
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
	id = canonicalCursorID(id)
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

func loadCursorRows(metadata *vendors.SessionMetadata, lanes map[string]map[string]bool, lane, path, query string, args []any, decode func(string, string) (string, string, string)) {
	db, err := openCursorDB(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		log.Printf("Cursor metadata %q: %v", path, err)
		return
	}
	defer db.Close()
	loadCursorRowsDB(metadata, lanes, lane, path, db, query, args, decode)
}

func loadCursorRowsDB(metadata *vendors.SessionMetadata, lanes map[string]map[string]bool, lane, path string, db *sql.DB, query string, args []any, decode func(string, string) (string, string, string)) {
	rows, err := db.Query(query, args...)
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
		id = canonicalCursorID(id)
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

func loadCursorSummaries(metadata *vendors.SessionMetadata, path string, ids []string) {
	db, err := openCursorDB(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		log.Printf("Cursor metadata %q: %v", path, err)
		return
	}
	defer db.Close()
	query, args := cursorIDQuery(`SELECT conversationId, tldr, overview FROM conversation_summaries`, "conversationId", ids)
	rows, err := db.Query(query, args...)
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
		id = canonicalCursorID(id)
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
