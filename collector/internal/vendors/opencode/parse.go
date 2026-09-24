package opencode

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

type taskLink struct {
	name   string
	status string
}

type parsedSession struct {
	transcript *vendors.ParsedSession
	tasks      map[string]taskLink
}

var errMalformedSession = errors.New("malformed OpenCode session data")

const promptOnlyBusyWindow = 2 * time.Minute

func parse(tx *sql.Tx, row storedSession) (parsedSession, error) {
	return parseContext(context.Background(), tx, row)
}

func parseContext(ctx context.Context, tx *sql.Tx, row storedSession) (parsedSession, error) {
	if err := ctx.Err(); err != nil {
		return parsedSession{}, err
	}
	modelID := ""
	if row.model.Valid {
		var model storedModel
		if err := json.Unmarshal([]byte(row.model.String), &model); err != nil {
			return parsedSession{}, fmt.Errorf("%w: decode model: %w", errMalformedSession, err)
		}
		modelID = modelName(model.ProviderID, model.ID)
	}
	var messages []storedMessage
	var err error
	if row.v2 {
		messages, err = loadV2MessagesContext(ctx, tx, row.id)
	} else {
		messages, err = loadMessagesContext(ctx, tx, row.id)
	}
	if err != nil {
		return parsedSession{}, err
	}
	todos := []session.Todo{}
	if !row.v2 {
		todos, err = loadTodosContext(ctx, tx, row.id)
		if err != nil {
			return parsedSession{}, err
		}
	}
	summaryEdits, err := loadFileEdits(row.summaryDiffs)
	if err != nil {
		return parsedSession{}, err
	}
	if err := ctx.Err(); err != nil {
		return parsedSession{}, err
	}

	tokens := map[string]session.ModelTokens{}
	var digest session.DigestLog
	var commands session.CommandLog
	spawns := map[string]vendors.SpawnState{}
	tasks := map[string]taskLink{}
	turns := 0
	toolUses := 0
	errorsCount := 0
	compactions := 0
	firstPrompt := ""
	summary := ""
	compactionSeed := ""
	contextTokens := 0
	hasContext := false
	activeDuration := int64(0)
	busy := false
	waiting := false
	promptOnly := false
	promptAt := int64(0)
	activeTasks := map[string]string{}
	commitLog := []session.CommitObservation{}
	pullRequests := 0
	fileEdits := session.NewFileEditSet()
	patchEdits := session.NewFileEditSet()
	var lastEditAt *int64
	todoStatus := map[string]string{}
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return parsedSession{}, err
		}
		if message.Role == "user" {
			for _, part := range message.parts {
				if part.Type == "compaction" {
					compactions++
					digest.Push(turns, session.DigestCompaction, "Context compacted", message.Time.Created)
				}
			}
			prompt := userText(message.parts)
			if prompt == "" {
				continue
			}
			busy = true
			waiting = false
			promptOnly = true
			promptAt = message.Time.Created
			clear(activeTasks)
			turns++
			if firstPrompt == "" {
				firstPrompt = prompt
			}
			category := session.DigestUser
			if turns == 1 {
				category = session.DigestFirstPrompt
			}
			digest.Push(turns, category, prompt, message.Time.Created)
			continue
		}
		if message.Role != "assistant" {
			continue
		}
		busy = message.Time.Created > 0 && message.Time.Completed == nil
		waiting = false
		promptOnly = false
		clear(activeTasks)
		if message.Time.Created > 0 {
			if message.Time.Completed != nil {
				if elapsed := *message.Time.Completed - message.Time.Created; elapsed > 0 {
					activeDuration += elapsed
				}
			}
		}

		if message.ModelID != "" {
			modelID = modelName(message.ProviderID, message.ModelID)
		}
		if modelID != "" {
			used := tokens[modelID]
			used.InputTokens += message.Tokens.Input
			used.OutputTokens += message.Tokens.Output + message.Tokens.Reasoning
			used.CacheReadInputTokens += message.Tokens.Cache.Read
			used.CacheCreationInputTokens += message.Tokens.Cache.Write
			used.Cost += message.Cost
			tokens[modelID] = used
		}
		currentContext := session.ContextTokens(
			message.Tokens.Input, message.Tokens.Cache.Read, message.Tokens.Cache.Write,
		)
		if currentContext > 0 {
			contextTokens = currentContext
			hasContext = true
		}
		if raw := bytes.TrimSpace(message.Error); len(raw) > 0 &&
			!bytes.Equal(raw, []byte("null")) {
			errorsCount++
		}

		texts := []string{}
		for _, part := range message.parts {
			if err := ctx.Err(); err != nil {
				return parsedSession{}, err
			}
			switch part.Type {
			case "text":
				if text := strings.TrimSpace(part.Text); text != "" {
					texts = append(texts, text)
				}
			case "patch":
				patched := false
				for _, path := range part.Files {
					if strings.TrimSpace(path) == "" {
						continue
					}
					path = normalizeFilePath(row.directory, path)
					patchEdits.Add(path, 0, 0, false)
					patched = true
				}
				if patched && part.updatedAt > 0 &&
					(lastEditAt == nil || part.updatedAt > *lastEditAt) {
					updatedAt := part.updatedAt
					lastEditAt = &updatedAt
				}
			case "tool":
				toolUses++
				if part.State.Status == "error" {
					errorsCount++
				}
				if part.Tool != "task" &&
					(part.State.Status == "pending" || part.State.Status == "running") {
					if part.Tool == "question" && part.State.Status == "running" &&
						len(part.State.Input.Questions) > 0 {
						waiting = true
					} else {
						busy = true
					}
				}
				if part.Tool == "task" {
					childID := part.State.Metadata.SessionID
					parentID := part.State.Metadata.ParentSessionID
					if childID != "" && (parentID == "" || parentID == row.id) {
						link, exists := tasks[childID]
						if !exists {
							turn := max(turns, 1)
							spawns[childID] = vendors.SpawnState{Turn: &turn}
							digest.PushSubagent(turns, childID, message.Time.Created)
						}
						if part.State.Input.Description != "" {
							link.name = part.State.Input.Description
						} else if link.name == "" {
							link.name = part.State.Input.SubagentType
						}
						link.status = part.State.Status
						tasks[childID] = link
						activeTasks[childID] = part.State.Status
						spawn := spawns[childID]
						spawn.Completed = part.State.Status == "completed"
						spawns[childID] = spawn
					}
				}
				isShell := part.Tool == "bash" || part.Tool == "shell"
				if isShell && part.State.Status == "completed" &&
					part.State.Metadata.Exit != nil && *part.State.Metadata.Exit != 0 {
					errorsCount++
				}
				if isShell && part.State.Status == "completed" && part.State.Input.Command != "" {
					commands.Note(part.State.Input.Command, part.State.Title)
					succeeded := part.State.Metadata.Exit == nil || *part.State.Metadata.Exit == 0
					commitLog = append(commitLog, session.ParseCommitObservations(
						part.State.Input.Command, part.State.Output, succeeded,
					)...)
					if succeeded {
						if session.IsPullRequestCreate(part.State.Input.Command) {
							pullRequests++
						}
					}
				}
				if part.State.Status == "completed" {
					if addCompletedToolEdits(row.directory, &part, fileEdits) &&
						part.State.Time.End != nil &&
						(lastEditAt == nil || *part.State.Time.End > *lastEditAt) {
						endedAt := *part.State.Time.End
						lastEditAt = &endedAt
					}
					if part.Tool == "todowrite" {
						if row.v2 {
							todos = make([]session.Todo, 0, len(part.State.Input.Todos))
							for _, todo := range part.State.Input.Todos {
								todos = append(todos, session.Todo{Text: todo.Content, Done: todo.Status == "completed"})
							}
						}
						for _, todo := range part.State.Input.Todos {
							if todo.Status == "completed" &&
								todoStatus[todo.Content] != "completed" {
								digest.Push(turns, session.DigestTodos, "completed: "+todo.Content, message.Time.Created)
							}
							todoStatus[todo.Content] = todo.Status
						}
					}
					if part.Tool == "question" {
						for index, question := range part.State.Input.Questions {
							answer := ""
							if index < len(part.State.Metadata.Answers) {
								answer = strings.Join(part.State.Metadata.Answers[index], ", ")
							}
							digest.PushQuestion(turns, question.Question, answer, message.Time.Created)
						}
					}
				}
			}
		}
		internalSummary := bytes.Equal(bytes.TrimSpace(message.Summary), []byte("true"))
		if internalSummary && len(texts) > 0 {
			compactionSeed = strings.Join(texts, "\n")
		}
		if text := strings.Join(texts, "\n"); message.Finish == "stop" && !internalSummary &&
			text != "" {
			summary = text
			category := session.DigestRecap
			if message.Agent == "plan" {
				category = session.DigestPlan
			}
			digest.Push(turns, category, text, message.Time.Created)
		}
	}
	for childID := range tasks {
		if _, current := activeTasks[childID]; current {
			continue
		}
		spawn := spawns[childID]
		spawn.Completed = true
		spawns[childID] = spawn
	}
	for _, status := range activeTasks {
		if status == "pending" || status == "running" {
			busy = true
			break
		}
	}
	if promptOnly && (promptAt <= 0 || time.Now().UnixMilli()-promptAt > promptOnlyBusyWindow.Milliseconds()) {
		busy = false
	}
	mergeFileEditSources(row.directory, fileEdits, summaryEdits, patchEdits)

	details := session.SessionDetails{
		Turns:          turns,
		ToolUses:       toolUses,
		Errors:         errorsCount,
		Compactions:    compactions,
		Commands:       commands.Raw(),
		Commits:        session.ReconcileCommits(commitLog, "", nil),
		PullRequests:   pullRequests,
		Todos:          todos,
		Digest:         digest.Entries(),
		FileEdits:      fileEdits.Edits,
		LastEditAt:     lastEditAt,
		CompactionSeed: compactionSeed,
	}
	if firstPrompt != "" {
		details.FirstPrompt = &firstPrompt
	}
	if modelID != "" {
		details.Model = &modelID
	}
	if hasContext {
		details.ContextTokens = &contextTokens
	}

	editedFileCount := len(details.FileEdits)
	if editedFileCount == 0 && row.summaryFiles.Valid {
		editedFileCount = int(row.summaryFiles.Int64)
	}
	entrypoint := sessionEntrypointContext(ctx, row.id, clientStateDir())
	if err := ctx.Err(); err != nil {
		return parsedSession{}, err
	}
	result := &session.Session{
		Agent:            vendors.AgentOpenCode,
		ID:               row.id,
		WorkingDirectory: row.directory,
		CommitLog:        commitLog,
		EditedFileCount:  editedFileCount,
		StartedAt:        earliestMessageTime(messages),
		LastActivityTime: row.updatedAt,
		Tokens:           tokens,
		Subagents:        []session.Subagent{},
		SessionDetails:   details,
		Entrypoint:       entrypoint,
	}
	if activeDuration > 0 {
		value := int(activeDuration)
		result.DurationMs = &value
	}
	if waiting {
		status := "waiting"
		result.Status = &status
	}
	if summary != "" {
		value := session.Truncate(summary, session.TruncateTextLimit)
		result.Summary = &value
	}
	parsed := &vendors.ParsedSession{
		Session:      result,
		ParentID:     row.parentID.String,
		Name:         row.title,
		InTurn:       busy || waiting,
		Spawns:       spawns,
		Commands:     commands.Labelled(),
		RecordedCost: &row.cost,
	}
	if busy && !waiting {
		status := "busy"
		parsed.StatusHint = &status
	}
	return parsedSession{transcript: parsed, tasks: tasks}, nil
}

func loadV2MessagesContext(ctx context.Context, tx *sql.Tx, sessionID string) ([]storedMessage, error) {
	rows, err := tx.QueryContext(ctx, `SELECT type, data, time_created FROM session_message WHERE session_id = ? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query v2 messages: %w", err)
	}
	defer rows.Close()
	messages := []storedMessage{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var kind, raw string
		var created int64
		if err := rows.Scan(&kind, &raw, &created); err != nil {
			return nil, err
		}
		var value struct {
			Text    string            `json:"text"`
			Content []json.RawMessage `json:"content"`
			Model   struct {
				ProviderID string `json:"providerID"`
				ID         string `json:"id"`
			} `json:"model"`
			Cost    float64         `json:"cost"`
			Tokens  storedTokens    `json:"tokens"`
			Finish  string          `json:"finish"`
			Summary json.RawMessage `json:"summary"`
			Error   json.RawMessage `json:"error"`
			Time    struct {
				Created   int64  `json:"created"`
				Completed *int64 `json:"completed"`
			} `json:"time"`
		}
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("%w: decode v2 message: %w", errMalformedSession, err)
		}
		message := storedMessage{Role: kind, ProviderID: value.Model.ProviderID, ModelID: value.Model.ID,
			Cost: value.Cost, Tokens: value.Tokens, Finish: value.Finish, Summary: value.Summary, Error: value.Error}
		message.Time.Created = value.Time.Created
		if message.Time.Created == 0 {
			message.Time.Created = created
		}
		message.Time.Completed = value.Time.Completed
		switch kind {
		case "user":
			message.parts = append(message.parts, storedPart{Type: "text", Text: value.Text})
		case "assistant":
			for _, content := range value.Content {
				var part storedPart
				if err := json.Unmarshal(content, &part); err != nil {
					return nil, fmt.Errorf("%w: decode v2 content: %w", errMalformedSession, err)
				}
				var extra struct {
					Name string `json:"name"`
					Time struct {
						Completed *int64 `json:"completed"`
					} `json:"time"`
				}
				if err := json.Unmarshal(content, &extra); err != nil {
					return nil, fmt.Errorf("%w: decode v2 content: %w", errMalformedSession, err)
				}
				part.Tool = extra.Name
				part.State.Time.End = extra.Time.Completed
				message.parts = append(message.parts, part)
			}
		case "idle":
			message.Role = "assistant"
			message.Time.Completed = &message.Time.Created
		case "compaction":
			message.Role = "user"
			message.parts = append(message.parts, storedPart{Type: "compaction"})
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return messages, nil
}

func earliestMessageTime(messages []storedMessage) int64 {
	earliest := int64(0)
	for _, message := range messages {
		created := message.Time.Created
		if created > 0 && (earliest == 0 || created < earliest) {
			earliest = created
		}
	}
	return earliest
}

func addCompletedToolEdits(cwd string, part *storedPart, edits *session.FileEditSet) bool {
	if part.Tool == "write" {
		file := storedToolFile{}
		if len(part.State.Metadata.Files) > 0 {
			file = part.State.Metadata.Files[0]
		}
		path := file.RelativePath
		if path == "" {
			path = file.FilePath
		}
		if path == "" {
			path = part.State.Metadata.FilePath
		}
		if path == "" {
			path = part.State.Input.FilePath
		}
		if path == "" {
			return false
		}
		isNew := file.Type == "add" || file.Type == "added" ||
			(part.State.Metadata.Exists != nil && !*part.State.Metadata.Exists)
		additions := file.Additions
		if isNew && additions == 0 {
			additions = session.CountLines(part.State.Input.Content)
		}
		path = normalizeFilePath(cwd, path)
		edits.Add(path, additions, file.Deletions, isNew)
		edits.Write(path, part.State.Input.Content)
		return true
	}

	edited := false
	for _, file := range part.State.Metadata.Files {
		path := file.RelativePath
		if path == "" {
			path = file.FilePath
		}
		if path == "" {
			continue
		}
		path = normalizeFilePath(cwd, path)
		edits.Add(
			path,
			file.Additions,
			file.Deletions,
			file.Type == "add" || file.Type == "added",
		)
		patch := file.Patch
		if patch == "" {
			patch = applyPatchForFile(part.State.Input.PatchText, cwd, path)
		}
		if patch == "" && part.Tool == "edit" {
			patch = part.State.Metadata.FileDiff.Patch
		}
		if patch != "" {
			edits.Patch(path, patch)
		} else if part.Tool == "edit" {
			edits.Change(path, part.State.Input.OldString, part.State.Input.NewString)
		}
		edited = true
	}
	if part.Tool == "edit" && len(part.State.Metadata.Files) == 0 {
		file := part.State.Metadata.FileDiff
		path := file.File
		if path == "" {
			path = part.State.Input.FilePath
		}
		if path != "" {
			path = normalizeFilePath(cwd, path)
			edits.Add(path, file.Additions, file.Deletions, false)
			if file.Patch != "" {
				edits.Patch(path, file.Patch)
			} else {
				edits.Change(path, part.State.Input.OldString, part.State.Input.NewString)
			}
			edited = true
		}
	}
	return edited
}

func applyPatchForFile(patchText, cwd, path string) string {
	var patch strings.Builder
	active := false
	for rawLine := range strings.Lines(patchText) {
		line := strings.TrimSuffix(rawLine, "\n")
		marker, marked := strings.CutPrefix(line, "*** Update File: ")
		if !marked {
			marker, marked = strings.CutPrefix(line, "*** Add File: ")
		}
		if !marked {
			marker, marked = strings.CutPrefix(line, "*** Delete File: ")
		}
		if marked {
			active = normalizeFilePath(cwd, marker) == path
			continue
		}
		if strings.HasPrefix(line, "*** ") {
			active = false
			continue
		}
		if active {
			patch.WriteString(rawLine)
		}
	}
	return patch.String()
}

func mergeFileEditSources(
	cwd string,
	edits *session.FileEditSet,
	summaryEdits []session.FileEdit,
	patchEdits *session.FileEditSet,
) {
	// Detailed tool metadata takes precedence over summary diffs and patch-only paths.
	seen := make(map[string]struct{}, len(edits.Edits)+len(summaryEdits)+len(patchEdits.Edits))
	for _, edit := range edits.Edits {
		seen[edit.Path] = struct{}{}
	}
	for _, edit := range summaryEdits {
		edit.Path = normalizeFilePath(cwd, edit.Path)
		if edit.Path == "" {
			continue
		}
		if _, exists := seen[edit.Path]; exists {
			continue
		}
		edits.Add(edit.Path, edit.Additions, edit.Deletions, edit.IsNew)
		seen[edit.Path] = struct{}{}
	}
	for _, edit := range patchEdits.Edits {
		if _, exists := seen[edit.Path]; exists {
			continue
		}
		for range edit.Edits {
			edits.Add(edit.Path, 0, 0, false)
		}
		seen[edit.Path] = struct{}{}
	}
}

func modelName(provider, model string) string {
	if provider == "" || model == "" {
		return model
	}
	return provider + "/" + model
}

func normalizeFilePath(cwd, path string) string {
	path = filepath.Clean(path)
	if cwd == "" || !filepath.IsAbs(path) {
		return path
	}
	relative, err := filepath.Rel(cwd, path)
	if err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return relative
	}
	return path
}

func loadMessagesContext(ctx context.Context, tx *sql.Tx, sessionID string) ([]storedMessage, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT message.id, message.data, part.data, part.time_updated
		FROM message
		LEFT JOIN part ON part.message_id = message.id
		WHERE message.session_id = ?
		ORDER BY message.time_created, message.id, part.id
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	messages := []storedMessage{}
	lastID := ""
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var id string
		var messageJSON string
		var partJSON sql.NullString
		var partUpdatedAt sql.NullInt64
		if err := rows.Scan(&id, &messageJSON, &partJSON, &partUpdatedAt); err != nil {
			return nil, fmt.Errorf("read message: %w", err)
		}
		if id != lastID {
			var message storedMessage
			if err := json.Unmarshal([]byte(messageJSON), &message); err != nil {
				return nil, fmt.Errorf("%w: decode message %q: %w", errMalformedSession, id, err)
			}
			messages = append(messages, message)
			lastID = id
		}
		if partJSON.Valid {
			var part storedPart
			if err := json.Unmarshal([]byte(partJSON.String), &part); err != nil {
				return nil, fmt.Errorf(
					"%w: decode part for message %q: %w",
					errMalformedSession,
					id,
					err,
				)
			}
			part.updatedAt = partUpdatedAt.Int64
			messages[len(messages)-1].parts = append(messages[len(messages)-1].parts, part)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read messages: %w", err)
	}
	return messages, nil
}

func loadTodosContext(ctx context.Context, tx *sql.Tx, sessionID string) ([]session.Todo, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT content, status
		FROM todo
		WHERE session_id = ?
		ORDER BY position
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query todos: %w", err)
	}
	defer rows.Close()

	todos := []session.Todo{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var todo session.Todo
		var status string
		if err := rows.Scan(&todo.Text, &status); err != nil {
			return nil, fmt.Errorf("read todo: %w", err)
		}
		todo.Done = status == "completed"
		todos = append(todos, todo)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read todos: %w", err)
	}
	return todos, nil
}

func loadFileEdits(raw sql.NullString) ([]session.FileEdit, error) {
	if !raw.Valid || raw.String == "" {
		return []session.FileEdit{}, nil
	}
	var diffs []storedDiff
	if err := json.Unmarshal([]byte(raw.String), &diffs); err != nil {
		return nil, fmt.Errorf("%w: decode file summary: %w", errMalformedSession, err)
	}
	edits := make([]session.FileEdit, 0, len(diffs))
	for _, diff := range diffs {
		if diff.File == "" {
			continue
		}
		edits = append(edits, session.FileEdit{
			Path: diff.File, Additions: diff.Additions, Deletions: diff.Deletions,
			Edits: 1, IsNew: diff.Status == "added",
		})
	}
	return edits, nil
}

func userText(parts []storedPart) string {
	texts := []string{}
	for _, part := range parts {
		if part.Type == "text" && !part.Synthetic {
			if text := strings.TrimSpace(part.Text); text != "" {
				texts = append(texts, text)
			}
		}
	}
	return strings.Join(texts, "\n")
}
