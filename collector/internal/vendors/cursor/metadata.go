package cursor

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	_ "modernc.org/sqlite"
)

const (
	entrypointIDE = "cursor-ide"
	entrypointCLI = "cursor-cli"
)

func LoadMetadata() (*vendors.SessionMetadata, error) {
	return LoadMetadataContext(context.Background())
}

func LoadMetadataContext(ctx context.Context) (*vendors.SessionMetadata, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return loadMetadataContext(ctx, home)
}

func LoadSelectionMetadata() (*vendors.SessionMetadata, error) {
	return LoadSelectionMetadataContext(context.Background())
}

func LoadSelectionMetadataContext(ctx context.Context) (*vendors.SessionMetadata, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return loadSelectionMetadataContext(ctx, home)
}

func loadSelectionMetadata(home string) (*vendors.SessionMetadata, error) {
	return loadSelectionMetadataContext(context.Background(), home)
}

func loadSelectionMetadataContext(ctx context.Context, home string) (*vendors.SessionMetadata, error) {
	metadata := vendors.EmptySessionMetadata()
	path := filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
	db, err := openCursorDBContext(ctx, path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		defer db.Close()
		if err := loadIDERelationshipsContext(ctx, metadata, db, nil); err != nil {
			return nil, err
		}
		if err := loadIDETimesContext(ctx, metadata, db, nil); err != nil {
			return nil, err
		}
		for _, entry := range metadata.Sessions {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			entry.Entrypoint = entrypointIDE
		}
	}
	if err := applyCursorLivenessContext(ctx, metadata, loadLiveSessionsContext(ctx), true); err != nil {
		return nil, err
	}
	return metadata, nil
}

func loadMetadata(home string) (*vendors.SessionMetadata, error) {
	return loadMetadataContext(context.Background(), home)
}

func loadMetadataContext(ctx context.Context, home string) (*vendors.SessionMetadata, error) {
	return loadMetadataForSessionsContext(ctx, home, nil)
}

func LoadMetadataForSessions(ids []string) (*vendors.SessionMetadata, error) {
	return LoadMetadataForSessionsContext(context.Background(), ids)
}

func LoadMetadataForSessionsContext(ctx context.Context, ids []string) (*vendors.SessionMetadata, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return loadMetadataForSessionsContext(ctx, home, ids)
}

func LoadRelationshipMetadataForSessions(ids []string) (*vendors.SessionMetadata, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return loadRelationshipMetadataForSessions(home, ids)
}

func loadRelationshipMetadataForSessions(home string, ids []string) (*vendors.SessionMetadata, error) {
	metadata := vendors.EmptySessionMetadata()
	path := filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
	db, err := openCursorDB(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		defer db.Close()
		loadIDERelationships(metadata, db, canonicalCursorIDs(ids))
	}
	for _, path := range cursorChatStores(home, canonicalCursorIDs(ids)) {
		loadCLIRelationship(metadata, path)
	}
	return metadata, nil
}

func loadCLIRelationship(metadata *vendors.SessionMetadata, path string) {
	db, err := openCursorDB(path)
	if err != nil {
		return
	}
	defer db.Close()
	rows, err := db.Query(`SELECT value FROM meta WHERE key = '0'`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var value string
		if rows.Scan(&value) != nil {
			continue
		}
		data, err := hex.DecodeString(value)
		if err != nil {
			continue
		}
		var item struct {
			AgentID      string `json:"agentId"`
			SubagentInfo struct {
				ParentAgentID string `json:"parentAgentId"`
				TypeName      string `json:"typeName"`
				ToolCallID    string `json:"toolCallId"`
			} `json:"subagentInfo"`
		}
		if json.Unmarshal(data, &item) != nil {
			continue
		}
		id, parentID := canonicalCursorID(item.AgentID), canonicalCursorID(item.SubagentInfo.ParentAgentID)
		if transcriptIDPattern.MatchString(id) && transcriptIDPattern.MatchString(parentID) {
			metadata.Session(id).Relationship = vendors.SessionRelationship{ParentID: parentID, SpawnKey: item.SubagentInfo.ToolCallID, Task: item.SubagentInfo.TypeName}
		}
	}
}

func loadMetadataForSessions(home string, ids []string) (*vendors.SessionMetadata, error) {
	return loadMetadataForSessionsContext(context.Background(), home, ids)
}

func loadMetadataForSessionsContext(ctx context.Context, home string, ids []string) (*vendors.SessionMetadata, error) {
	ids, err := canonicalCursorIDsContext(ctx, ids)
	if err != nil {
		return nil, err
	}
	metadata := vendors.EmptySessionMetadata()
	lanes := map[string]map[string]bool{}
	globalStorage := filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage")
	statePath := filepath.Join(globalStorage, "state.vscdb")
	stateDB, stateErr := openCursorDBContext(ctx, statePath)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if stateErr != nil && !os.IsNotExist(stateErr) {
		log.Printf("Cursor metadata %q: %v", statePath, stateErr)
	}
	if stateDB != nil {
		defer stateDB.Close()
		if err := loadIDERelationshipsContext(ctx, metadata, stateDB, ids); err != nil {
			return nil, err
		}
		ids, err = cursorMetadataIDsContext(ctx, ids, metadata)
		if err != nil {
			return nil, err
		}
		query, args := cursorIDQuery(`SELECT composerId, value FROM composerHeaders`, "composerId", ids)
		if err := loadCursorRowsDBContext(ctx, metadata, lanes, entrypointIDE, statePath, stateDB, query, args, func(id, value string) (string, string, string) {
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
		}); err != nil {
			return nil, err
		}
		for _, load := range []func(context.Context, *vendors.SessionMetadata, *sql.DB, []string) error{
			loadIDETimesContext, loadIDEDiffsContext, loadIDECommitObservationsContext,
		} {
			if err := load(ctx, metadata, stateDB, ids); err != nil {
				return nil, err
			}
		}
		if err := loadIDEModelsDBContext(ctx, metadata, lanes, stateDB, ids); err != nil {
			return nil, err
		}
	}
	query, args := cursorIDQuery(`SELECT id, title FROM conversations`, "id", ids)
	query += ` ORDER BY source = 'local' DESC`
	if err := loadCursorRowsContext(ctx, metadata, lanes, "", filepath.Join(globalStorage, "conversation-search.db"), query, args, func(id, title string) (string, string, string) {
		return id, title, ""
	}); err != nil {
		return nil, err
	}

	chatStores, err := cursorChatStoresContext(ctx, home, ids)
	if err != nil {
		return nil, err
	}
	for _, path := range chatStores {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := loadCursorRowsContext(ctx, metadata, lanes, entrypointCLI, path, `SELECT key, value FROM meta WHERE key = '0'`, nil, func(_, value string) (string, string, string) {
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
		}); err != nil {
			return nil, err
		}
	}

	if err := loadCursorSummariesContext(ctx, metadata, filepath.Join(home, ".cursor", "ai-tracking", "ai-code-tracking.db"), ids); err != nil {
		return nil, err
	}
	for id, matches := range lanes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(matches) != 1 {
			entry := metadata.Session(id)
			entry.Model, entry.WorkingDirectory, entry.CompactionSeed = "", "", ""
			entry.PullRequests, entry.StartedAt, entry.LastActivityAt = 0, 0, 0
			entry.Usage = vendors.SessionUsage{}
			entry.FileEdits, entry.CommitObservations, entry.ObservedModels = nil, nil, nil
			continue
		}
		for lane := range matches {
			metadata.Session(id).Entrypoint = lane
		}
	}
	if err := applyCursorLivenessContext(ctx, metadata, loadLiveSessionsContext(ctx), false); err != nil {
		return nil, err
	}
	return metadata, nil
}

func applyCursorLiveness(metadata *vendors.SessionMetadata, live map[string]string, includeUnknown bool) {
	_ = applyCursorLivenessContext(context.Background(), metadata, live, includeUnknown)
}

func applyCursorLivenessContext(ctx context.Context, metadata *vendors.SessionMetadata, live map[string]string, includeUnknown bool) error {
	blockingActions := map[string]bool{}
	for id, entry := range metadata.Sessions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Live == "waiting" {
			blockingActions[id] = true
			entry.Live = ""
		}
	}
	for id, lane := range live {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := metadata.Lookup(id)
		if entry == nil && includeUnknown && lane != "" {
			entry = metadata.Session(id)
			entry.Entrypoint = lane
		}
		if entry != nil && lane != "" && entry.Entrypoint == lane {
			entry.Live = "interactive"
			if blockingActions[id] {
				entry.Live = "waiting"
			}
		}
	}
	return ctx.Err()
}

func canonicalCursorIDs(ids []string) []string {
	result, _ := canonicalCursorIDsContext(context.Background(), ids)
	return result
}

func canonicalCursorIDsContext(ctx context.Context, ids []string) ([]string, error) {
	if ids == nil {
		return nil, ctx.Err()
	}
	result := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id = canonicalCursorID(id)
		if id != "" && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result, ctx.Err()
}

func canonicalCursorID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func cursorMetadataIDs(ids []string, metadata *vendors.SessionMetadata) []string {
	result, _ := cursorMetadataIDsContext(context.Background(), ids, metadata)
	return result
}

func cursorMetadataIDsContext(ctx context.Context, ids []string, metadata *vendors.SessionMetadata) ([]string, error) {
	if ids == nil {
		return nil, ctx.Err()
	}
	for id, entry := range metadata.Sessions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ids = append(ids, id, entry.Relationship.ParentID)
	}
	return canonicalCursorIDsContext(ctx, ids)
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
	stores, _ := cursorChatStoresContext(context.Background(), home, ids)
	return stores
}

func cursorChatStoresContext(ctx context.Context, home string, ids []string) ([]string, error) {
	if ids == nil {
		stores, _ := filepath.Glob(filepath.Join(home, ".cursor", "chats", "*", "*", "store.db"))
		return stores, ctx.Err()
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(id, "agent-") {
			wanted[id] = true
		}
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".cursor", "chats", "*", "*", "store.db"))
	stores := make([]string, 0, len(matches))
	for _, path := range matches {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if wanted[canonicalCursorID(filepath.Base(filepath.Dir(path)))] {
			stores = append(stores, path)
		}
	}
	return stores, ctx.Err()
}

func loadIDERelationships(metadata *vendors.SessionMetadata, db *sql.DB, ids []string) {
	_ = loadIDERelationshipsContext(context.Background(), metadata, db, ids)
}

func loadIDERelationshipsContext(ctx context.Context, metadata *vendors.SessionMetadata, db *sql.DB, ids []string) error {
	familyIDs := canonicalCursorIDs(ids)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
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
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return ctx.Err()
		}
		changed := false
		known := map[string]bool{}
		for _, id := range familyIDs {
			known[id] = true
		}
		for rows.Next() {
			if err := ctx.Err(); err != nil {
				rows.Close()
				return err
			}
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
	rows2, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return ctx.Err()
	}
	defer rows2.Close()
	for rows2.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
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
	return ctx.Err()
}

var fullCommitHash = regexp.MustCompile(`^[0-9a-fA-F]{40,64}$`)

func loadIDECommitObservations(metadata *vendors.SessionMetadata, db *sql.DB, ids []string) {
	_ = loadIDECommitObservationsContext(context.Background(), metadata, db, ids)
}

func loadIDECommitObservationsContext(ctx context.Context, metadata *vendors.SessionMetadata, db *sql.DB, ids []string) error {
	query, args := cursorKeyQuery(`SELECT key, value FROM cursorDiskKV`, "bubbleId:", ids)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return ctx.Err()
	}
	defer rows.Close()
	seen := map[string]map[string]bool{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
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
	return ctx.Err()
}

func completedIDETerminalCommand(name, status string, rawArgs, params json.RawMessage) (string, bool) {
	if status != "completed" || (name != "run_terminal_cmd" && name != "run_terminal_command_v2") {
		return "", false
	}
	for _, raw := range []json.RawMessage{rawArgs, params} {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			value = string(raw)
		}
		var args struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(value), &args) == nil && args.Command != "" {
			return args.Command, true
		}
	}
	return "", false
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
			Params  json.RawMessage `json:"params"`
			Result  string          `json:"result"`
		} `json:"toolFormerData"`
	}
	if json.Unmarshal([]byte(value), &bubble) != nil {
		return nil
	}
	command, ok := completedIDETerminalCommand(bubble.Tool.Name, bubble.Tool.Status, bubble.Tool.RawArgs, bubble.Tool.Params)
	if !ok {
		return nil
	}
	output := bubble.Tool.Result
	if bubble.Tool.Name == "run_terminal_command_v2" {
		var result struct {
			Output string `json:"output"`
		}
		if json.Unmarshal([]byte(output), &result) == nil {
			output = result.Output
		}
	}
	attempts := session.ParseCommitObservations(command, output, true)
	if bubble.Tool.Name == "run_terminal_command_v2" && len(bubble.Before.CommitHashesByGitWorkspace) == 0 && len(bubble.After.CommitHashesByGitWorkspace) == 0 {
		for _, attempt := range attempts {
			if attempt.Hash == "" {
				return nil
			}
		}
		return attempts
	}
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
	_ = loadIDEDiffsContext(context.Background(), metadata, db, ids)
}

func loadIDEDiffsContext(ctx context.Context, metadata *vendors.SessionMetadata, db *sql.DB, ids []string) error {
	query, args := cursorKeyQuery(`SELECT key, value FROM cursorDiskKV`, "composerData:", ids)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return ctx.Err()
	}
	checkpoints := map[string]string{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			rows.Close()
			return err
		}
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
		if err := ctx.Err(); err != nil {
			return err
		}
		var value string
		if db.QueryRowContext(ctx, `SELECT value FROM cursorDiskKV WHERE LOWER(key) = ?`, strings.ToLower("checkpointId:"+id+":"+checkpointID)).Scan(&value) != nil {
			continue
		}
		var checkpoint ideCheckpoint
		if json.Unmarshal([]byte(value), &checkpoint) != nil {
			continue
		}
		metadata.Session(id).FileEdits = checkpointFileEdits(checkpoint)
	}
	return ctx.Err()
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
	_ = loadIDETimesContext(context.Background(), metadata, db, ids)
}

func loadIDETimesContext(ctx context.Context, metadata *vendors.SessionMetadata, db *sql.DB, ids []string) error {
	query, args := cursorIDQuery(`SELECT composerId, createdAt, lastUpdatedAt FROM composerHeaders`, "composerId", ids)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return ctx.Err()
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var id string
		var created, updated sql.NullInt64
		if rows.Scan(&id, &created, &updated) == nil {
			setCursorTimes(metadata, id, created.Int64, updated.Int64)
		}
	}
	return ctx.Err()
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

const (
	// Each ID adds two OR terms; stay below SQLite's default expression depth.
	maxIDEModelQueryIDs   = 400
	maxObservedModels     = 16
	maxObservedModelBytes = 120
)

func loadIDEModelsDB(metadata *vendors.SessionMetadata, lanes map[string]map[string]bool, db *sql.DB, ids []string) {
	_ = loadIDEModelsDBContext(context.Background(), metadata, lanes, db, ids)
}

func loadIDEModelsDBContext(ctx context.Context, metadata *vendors.SessionMetadata, lanes map[string]map[string]bool, db *sql.DB, ids []string) error {
	if len(ids) > maxIDEModelQueryIDs {
		for start := 0; start < len(ids); start += maxIDEModelQueryIDs {
			if err := loadIDEModelsDBContext(ctx, metadata, lanes, db, ids[start:min(start+maxIDEModelQueryIDs, len(ids))]); err != nil {
				return err
			}
		}
		return nil
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
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return ctx.Err()
	}
	defer rows.Close()
	type observed struct {
		model string
		time  float64
		key   string
	}
	bubbles := map[string]observed{}
	fallbacks := map[string]string{}
	observedModels := map[string]map[string]observed{}
	pullRequests := map[string]map[string]struct{}{}
	newer := func(candidate, current observed) bool {
		return candidate.time > current.time || candidate.time == current.time && candidate.key > current.key
	}
	observeModel := func(id, model string, observation observed) string {
		model = strings.TrimSpace(model)
		if model == "" || len(model) > maxObservedModelBytes {
			return ""
		}
		model = normalizeCursorModel(model)
		if model == "" || len(model) > maxObservedModelBytes {
			return ""
		}
		models := observedModels[id]
		if models == nil {
			models = map[string]observed{}
			observedModels[id] = models
		}
		observation.model = model
		if previous, ok := models[model]; !ok || newer(observation, previous) {
			models[model] = observation
		}
		if len(models) > maxObservedModels {
			oldestModel := model
			for candidateModel, candidate := range models {
				if newer(models[oldestModel], candidate) {
					oldestModel = candidateModel
				}
			}
			delete(models, oldestModel)
		}
		return model
	}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var key, value string
		if rows.Scan(&key, &value) != nil {
			continue
		}
		composerID, composerData := strings.CutPrefix(key, "composerData:")
		validComposerData := composerData && transcriptIDPattern.MatchString(composerID)
		if validComposerData {
			composerID = canonicalCursorID(composerID)
			if lanes != nil {
				if lanes[composerID] == nil {
					lanes[composerID] = map[string]bool{}
				}
				lanes[composerID][entrypointIDE] = true
			}
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
				Name           string          `json:"name"`
				Status         string          `json:"status"`
				RawArgs        json.RawMessage `json:"rawArgs"`
				Params         json.RawMessage `json:"params"`
				Result         string          `json:"result"`
				AdditionalData struct {
					Status string `json:"status"`
				} `json:"additionalData"`
			} `json:"toolFormerData"`
			UsageData map[string]struct {
				CostInCents *float64 `json:"costInCents"`
			} `json:"usageData"`
			ContextTokensUsed         *int `json:"contextTokensUsed"`
			ContextTokenLimit         *int `json:"contextTokenLimit"`
			LatestConversationSummary struct {
				Summary struct {
					Text string `json:"summary"`
				} `json:"summary"`
			} `json:"latestConversationSummary"`
		}
		if json.Unmarshal([]byte(value), &item) != nil {
			continue
		}
		if parts := strings.SplitN(key, ":", 3); len(parts) == 3 && parts[0] == "bubbleId" && transcriptIDPattern.MatchString(parts[1]) {
			id := canonicalCursorID(parts[1])
			if item.ToolFormerData.Name == "ask_question" && item.ToolFormerData.AdditionalData.Status == "pending" {
				metadata.Session(id).Live = "waiting"
			}
			createdAt := cursorBubbleTime(item.CreatedAt)
			observation := observed{time: createdAt, key: key}
			model := observeModel(id, item.ModelInfo.ModelName, observation)
			previous := bubbles[id]
			observation.model = model
			if model != "" && (previous.model == "" || newer(observation, previous)) {
				bubbles[id] = observation
			}
			command, completed := completedIDETerminalCommand(item.ToolFormerData.Name, item.ToolFormerData.Status, item.ToolFormerData.RawArgs, item.ToolFormerData.Params)
			if completed && session.IsPullRequestCreate(command) {
				if pullRequests[id] == nil {
					pullRequests[id] = map[string]struct{}{}
				}
				for _, url := range session.PullRequestURLs(item.ToolFormerData.Result) {
					pullRequests[id][url] = struct{}{}
				}
			}
		} else if validComposerData {
			id := composerID
			fallbacks[id] = strings.TrimSpace(item.ModelConfig.ModelName)
			metadata.Session(id).CompactionSeed = strings.TrimSpace(item.LatestConversationSummary.Summary.Text)
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
		if err := ctx.Err(); err != nil {
			return err
		}
		if bubbles[id].model == "" && model != "" {
			metadata.Session(id).Model = observeModel(id, model, observed{})
		}
	}
	for id, value := range bubbles {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata.Session(id).Model = value.model
	}
	for id, models := range observedModels {
		entry := metadata.Session(id)
		entry.ObservedModels = make([]string, 0, len(models))
		for model := range models {
			entry.ObservedModels = append(entry.ObservedModels, model)
		}
		slices.Sort(entry.ObservedModels)
	}
	for id, urls := range pullRequests {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata.Session(id).PullRequests = len(urls)
	}
	return ctx.Err()
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
	_ = loadCursorRowsContext(context.Background(), metadata, lanes, lane, path, query, args, decode)
}

func loadCursorRowsContext(ctx context.Context, metadata *vendors.SessionMetadata, lanes map[string]map[string]bool, lane, path, query string, args []any, decode func(string, string) (string, string, string)) error {
	db, err := openCursorDBContext(ctx, path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		log.Printf("Cursor metadata %q: %v", path, err)
		return ctx.Err()
	}
	defer db.Close()
	return loadCursorRowsDBContext(ctx, metadata, lanes, lane, path, db, query, args, decode)
}

func loadCursorRowsDB(metadata *vendors.SessionMetadata, lanes map[string]map[string]bool, lane, path string, db *sql.DB, query string, args []any, decode func(string, string) (string, string, string)) {
	_ = loadCursorRowsDBContext(context.Background(), metadata, lanes, lane, path, db, query, args, decode)
}

func loadCursorRowsDBContext(ctx context.Context, metadata *vendors.SessionMetadata, lanes map[string]map[string]bool, lane, path string, db *sql.DB, query string, args []any, decode func(string, string) (string, string, string)) error {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		log.Printf("Cursor metadata %q: %v", path, err)
		return ctx.Err()
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
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
	return ctx.Err()
}

func loadCursorSummaries(metadata *vendors.SessionMetadata, path string, ids []string) {
	_ = loadCursorSummariesContext(context.Background(), metadata, path, ids)
}

func loadCursorSummariesContext(ctx context.Context, metadata *vendors.SessionMetadata, path string, ids []string) error {
	db, err := openCursorDBContext(ctx, path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		log.Printf("Cursor metadata %q: %v", path, err)
		return ctx.Err()
	}
	defer db.Close()
	query, args := cursorIDQuery(`SELECT conversationId, tldr, overview FROM conversation_summaries`, "conversationId", ids)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		log.Printf("Cursor metadata %q: %v", path, err)
		return ctx.Err()
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
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
	return ctx.Err()
}

func openCursorDB(path string) (*sql.DB, error) {
	return openCursorDBContext(context.Background(), path)
}

func openCursorDBContext(ctx context.Context, path string) (*sql.DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
