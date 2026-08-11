package opencode

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const activeRootsQuery = `
SELECT id, directory, title, summary_files, summary_diffs, model, time_created, time_updated
FROM session
WHERE parent_id IS NULL AND time_archived IS NULL`

func Collect(since int64) ([]*vendors.ParsedTranscript, *vendors.SessionMetadata, error) {
	db, err := open()
	if errors.Is(err, os.ErrNotExist) {
		return []*vendors.ParsedTranscript{}, emptyMetadata(), nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()

	query := activeRootsQuery
	var args []any
	if since > 0 {
		query += " AND time_updated >= ?"
		args = append(args, since)
	}
	query += " ORDER BY time_updated DESC, id"
	parsed, err := load(db, query, args...)
	return parsed, emptyMetadata(), err
}

func Get(id string) (*vendors.ParsedTranscript, error) {
	if id == "" {
		return nil, nil
	}
	db, err := open()
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()

	parsed, err := load(db, activeRootsQuery+" AND id = ?", id)
	if err != nil || len(parsed) == 0 {
		return nil, err
	}
	return parsed[0], nil
}

func Health() (SourceHealth, error) {
	db, err := open()
	if errors.Is(err, os.ErrNotExist) {
		return SourceHealth{Missing: true}, nil
	}
	if err != nil {
		return SourceHealth{}, err
	}
	defer db.Close()

	var health SourceHealth
	err = db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(parent_id IS NULL), 0)
		FROM session
		WHERE time_archived IS NULL
	`).Scan(&health.Entries, &health.Sessions)
	return health, err
}

func emptyMetadata() *vendors.SessionMetadata {
	return &vendors.SessionMetadata{Names: map[string]string{}, Live: map[string]string{}}
}

func load(db *sql.DB, query string, args ...any) ([]*vendors.ParsedTranscript, error) {
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query OpenCode sessions: %w", err)
	}
	stored := []storedSession{}
	for rows.Next() {
		var row storedSession
		if err := rows.Scan(
			&row.id, &row.directory, &row.title, &row.summaryFiles, &row.summaryDiffs,
			&row.model, &row.createdAt, &row.updatedAt,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read OpenCode session: %w", err)
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("read OpenCode sessions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	parsed := make([]*vendors.ParsedTranscript, 0, len(stored))
	for _, row := range stored {
		item, err := parse(tx, row)
		if err != nil {
			return nil, fmt.Errorf("parse OpenCode session %q: %w", row.id, err)
		}
		parsed = append(parsed, item)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return parsed, nil
}

func parse(tx *sql.Tx, row storedSession) (*vendors.ParsedTranscript, error) {
	modelID := ""
	if row.model.Valid {
		var model storedModel
		if err := json.Unmarshal([]byte(row.model.String), &model); err != nil {
			return nil, fmt.Errorf("decode model: %w", err)
		}
		modelID = model.ID
	}
	messages, err := loadMessages(tx, row.id)
	if err != nil {
		return nil, err
	}
	todos, err := loadTodos(tx, row.id)
	if err != nil {
		return nil, err
	}
	fileEdits, err := loadFileEdits(row.summaryDiffs)
	if err != nil {
		return nil, err
	}

	tokens := map[string]session.ModelTokens{}
	var digest session.DigestLog
	var commands session.CommandLog
	turns := 0
	toolUses := 0
	errorsCount := 0
	compactions := 0
	firstPrompt := ""
	summary := ""
	contextTokens := 0
	hasContext := false
	for _, message := range messages {
		if message.Role == "user" {
			for _, part := range message.parts {
				if part.Type == "compaction" {
					compactions++
					digest.Push(turns, session.DigestCompaction, "Context compacted")
				}
			}
			prompt := userText(message.parts)
			if prompt == "" {
				continue
			}
			turns++
			if firstPrompt == "" {
				firstPrompt = prompt
			}
			category := session.DigestUser
			if turns == 1 {
				category = session.DigestFirstPrompt
			}
			digest.Push(turns, category, prompt)
			continue
		}
		if message.Role != "assistant" {
			continue
		}

		if message.ModelID != "" {
			modelID = message.ModelID
		}
		if modelID != "" {
			used := tokens[modelID]
			used.InputTokens += message.Tokens.Input
			used.OutputTokens += message.Tokens.Output + message.Tokens.Reasoning
			used.CacheReadInputTokens += message.Tokens.Cache.Read
			used.CacheCreationInputTokens += message.Tokens.Cache.Write
			tokens[modelID] = used
		}
		currentContext := session.ContextTokens(
			message.Tokens.Input, message.Tokens.Cache.Read, message.Tokens.Cache.Write,
		)
		if currentContext > 0 {
			contextTokens = currentContext
			hasContext = true
		}
		if raw := bytes.TrimSpace(message.Error); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
			errorsCount++
		}

		texts := []string{}
		for _, part := range message.parts {
			switch part.Type {
			case "text":
				if text := strings.TrimSpace(part.Text); text != "" {
					texts = append(texts, text)
				}
			case "tool":
				toolUses++
				if part.State.Status == "error" {
					errorsCount++
				}
				if part.Tool == "bash" && part.State.Input.Command != "" {
					commands.Note(part.State.Input.Command, part.State.Title)
				}
			}
		}
		internalSummary := bytes.Equal(bytes.TrimSpace(message.Summary), []byte("true"))
		if text := strings.Join(texts, "\n"); message.Finish == "stop" && !internalSummary && text != "" {
			summary = text
			digest.Push(turns, session.DigestRecap, text)
		}
	}

	details := session.SessionDetails{
		Turns:        turns,
		ToolUses:     toolUses,
		Errors:       errorsCount,
		Compactions:  compactions,
		Commands:     commands.Raw(),
		CommandCount: commands.Count(),
		Commits:      []string{},
		Todos:        todos,
		Digest:       digest.Entries(),
		FileEdits:    fileEdits,
	}
	if firstPrompt != "" {
		details.FirstPrompt = &firstPrompt
	}
	if modelID != "" {
		details.Model = &modelID
		details.ContextWindow = session.ContextWindowFor(modelID)
	}
	if hasContext {
		details.ContextTokens = &contextTokens
	}

	editedFileCount := len(fileEdits)
	if row.summaryFiles.Valid {
		editedFileCount = int(row.summaryFiles.Int64)
	}
	result := &session.Session{
		Agent:            vendors.AgentOpenCode,
		ID:               row.id,
		WorkingDirectory: row.directory,
		EditedFileCount:  editedFileCount,
		LastActivityTime: row.updatedAt,
		Tokens:           tokens,
		Subagents:        []session.Subagent{},
		SessionDetails:   details,
	}
	if duration := row.updatedAt - row.createdAt; duration > 0 {
		value := int(duration)
		result.DurationMs = &value
	}
	if summary != "" {
		value := session.Truncate(summary, session.TruncateTextLimit)
		result.Summary = &value
	}
	return &vendors.ParsedTranscript{
		Session:    result,
		Name:       row.title,
		SpawnTurns: map[string]int{},
		Completed:  map[string]struct{}{},
		Commands:   []session.SubagentCommand{},
	}, nil
}

func loadMessages(tx *sql.Tx, sessionID string) ([]storedMessage, error) {
	rows, err := tx.Query(`
		SELECT message.id, message.data, part.data
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
		var id string
		var messageJSON string
		var partJSON sql.NullString
		if err := rows.Scan(&id, &messageJSON, &partJSON); err != nil {
			return nil, fmt.Errorf("read message: %w", err)
		}
		if id != lastID {
			var message storedMessage
			if err := json.Unmarshal([]byte(messageJSON), &message); err != nil {
				return nil, fmt.Errorf("decode message %q: %w", id, err)
			}
			messages = append(messages, message)
			lastID = id
		}
		if partJSON.Valid {
			var part storedPart
			if err := json.Unmarshal([]byte(partJSON.String), &part); err != nil {
				return nil, fmt.Errorf("decode part for message %q: %w", id, err)
			}
			messages[len(messages)-1].parts = append(messages[len(messages)-1].parts, part)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read messages: %w", err)
	}
	return messages, nil
}

func loadTodos(tx *sql.Tx, sessionID string) ([]session.Todo, error) {
	rows, err := tx.Query(`
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
		return nil, fmt.Errorf("decode file summary: %w", err)
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
