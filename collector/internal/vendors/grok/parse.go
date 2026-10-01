package grok

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

type summaryFile struct {
	Info struct {
		ID  string `json:"id"`
		CWD string `json:"cwd"`
	} `json:"info"`
	ChatFormatVersion int      `json:"chat_format_version"`
	CreatedAt         string   `json:"created_at"`
	UpdatedAt         string   `json:"updated_at"`
	LastActiveAt      string   `json:"last_active_at"`
	CurrentModelID    string   `json:"current_model_id"`
	GeneratedTitle    string   `json:"generated_title"`
	LastTurnSummary   string   `json:"last_turn_summary"`
	GitRootDir        string   `json:"git_root_dir"`
	GitRemotes        []string `json:"git_remotes"`
	HeadBranch        string   `json:"head_branch"`
	ParentSessionID   string   `json:"parent_session_id"`
	SessionKind       string   `json:"session_kind"`
	ContextWindow     int      `json:"context_window"`
}

type signalsFile struct {
	TurnCount           int `json:"turnCount"`
	ErrorCount          int `json:"errorCount"`
	CompactionCount     int `json:"compactionCount"`
	ContextTokensUsed   int `json:"contextTokensUsed"`
	ContextWindowTokens int `json:"contextWindowTokens"`
	ToolCallCount       int `json:"toolCallCount"`
}

type usageCounts struct {
	InputTokens         int    `json:"inputTokens"`
	OutputTokens        int    `json:"outputTokens"`
	CachedReadTokens    int    `json:"cachedReadTokens"`
	CacheCreationTokens int    `json:"cacheCreationTokens"`
	CostUSDTicks        *int64 `json:"costUsdTicks"`
	CostIsPartial       bool   `json:"costIsPartial"`
	UsageIsIncomplete   bool   `json:"usageIsIncomplete"`
}

type usageFile struct {
	Session struct {
		usageCounts
		ModelUsage map[string]usageCounts `json:"modelUsage"`
	} `json:"session"`
}

type updateLine struct {
	Params struct {
		Update struct {
			Kind    string          `json:"sessionUpdate"`
			Status  string          `json:"status"`
			Content json.RawMessage `json:"content"`
			Meta    struct {
				HideFromScrollback bool `json:"hideFromScrollback"`
			} `json:"_meta"`
		} `json:"update"`
	} `json:"params"`
}

// chunkContent is a message chunk's content object.
type chunkContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Meta struct {
		BashCommand *string `json:"bash_command"`
	} `json:"_meta"`
}

// toolContent is one item of a tool_call_update's content array.
type toolContent struct {
	Type    string `json:"type"`
	Path    string `json:"path"`
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

const ticksPerUSD = 1e10

// maxGrokJSONBytes matches the update-line buffer. Summary, signals, and usage
// files stay under it; a larger file is rejected before it is decoded.
const maxGrokJSONBytes int64 = 64 << 20

func isSubagentKind(kind string) bool {
	return kind == "subagent" || kind == "subagent_resume" || kind == "subagent_fork"
}

func readSummary(dir string) (*summaryFile, error) {
	var summary summaryFile
	if err := readJSON(filepath.Join(dir, "summary.json"), &summary); err != nil {
		return nil, err
	}
	return &summary, nil
}

// parseSession returns nil for a chat_format_version other than 1.
func parseSession(dir string) (*vendors.ParsedSession, error) {
	return parseSessionContext(context.Background(), dir)
}

func parseSessionContext(ctx context.Context, dir string) (*vendors.ParsedSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	summary, err := readSummary(dir)
	if err != nil {
		return nil, err
	}
	if summary.ChatFormatVersion != 1 || summary.Info.ID == "" {
		return nil, nil
	}
	s := &session.Session{
		Agent:            vendors.AgentGrok,
		ID:               summary.Info.ID,
		ParentSessionID:  summary.ParentSessionID,
		WorkingDirectory: summary.Info.CWD,
		StartedAt:        parseTime(summary.CreatedAt),
		LastActivityTime: max(parseTime(summary.LastActiveAt), parseTime(summary.UpdatedAt)),
	}
	s.Name = nonEmpty(summary.GeneratedTitle)
	s.Summary = nonEmpty(summary.LastTurnSummary)
	s.Model = nonEmpty(summary.CurrentModelID)
	s.Branch = nonEmpty(summary.HeadBranch)
	for _, remote := range summary.GitRemotes {
		if name := session.CanonicalOriginURL(remote); name != "" {
			s.Repository = &name
			break
		}
	}
	s.RepositoryLocalOnly = summary.GitRootDir != "" && len(summary.GitRemotes) == 0

	updates, err := readUpdates(ctx, filepath.Join(dir, "updates.jsonl"))
	if err != nil {
		return nil, err
	}
	s.FirstPrompt = nonEmpty(updates.firstPrompt)
	s.FileEdits = updates.edits.Edits
	s.EditedFileCount = len(s.FileEdits)
	s.Todos = readTodos(filepath.Join(dir, "plan.json"))

	var signals signalsFile
	readOptionalJSON(filepath.Join(dir, "signals.json"), &signals)
	// signals.json is flushed at turn end, so an open turn adds its own tool calls.
	s.ToolUses = signals.ToolCallCount + updates.openToolCalls
	s.Turns = signals.TurnCount
	if updates.inTurn {
		s.Turns++
	}
	s.Errors = signals.ErrorCount
	s.Compactions = signals.CompactionCount
	if updates.finishedTurns > 0 && signals.ContextTokensUsed > 0 {
		s.ContextTokens = &signals.ContextTokensUsed
	}
	switch {
	case summary.ContextWindow > 0:
		s.ContextWindow = &summary.ContextWindow
	case signals.ContextWindowTokens > 0:
		s.ContextWindow = &signals.ContextWindowTokens
	case s.Model != nil:
		s.ContextWindow = session.ContextWindowFor(*s.Model)
	}

	parsed := &vendors.ParsedSession{Session: s, LogPath: filepath.Join(dir, "updates.jsonl"), InTurn: updates.inTurn}
	if isSubagentKind(summary.SessionKind) {
		// A fork or worktree session keeps its parent link but stays a top-level row.
		parsed.ParentID = summary.ParentSessionID
	}
	applyUsage(parsed, filepath.Join(dir, "usage.json"))
	return parsed, nil
}

func applyUsage(parsed *vendors.ParsedSession, path string) {
	var usage *usageFile
	if readOptionalJSON(path, &usage); usage == nil {
		return
	}
	tokens := make(map[string]session.ModelTokens, len(usage.Session.ModelUsage))
	for model, used := range usage.Session.ModelUsage {
		value := session.ModelTokens{
			InputTokens:              used.InputTokens,
			OutputTokens:             used.OutputTokens,
			CacheCreationInputTokens: used.CacheCreationTokens,
			CacheReadInputTokens:     used.CachedReadTokens,
		}
		if cost, ok := used.cost(); ok {
			value.Cost = cost
		}
		tokens[model] = value
	}
	if len(tokens) == 0 && hasTokenCounts(usage.Session.usageCounts) {
		used := usage.Session.usageCounts
		parsed.Session.UnattributedTokens = &session.ModelTokens{
			InputTokens:              used.InputTokens,
			OutputTokens:             used.OutputTokens,
			CacheCreationInputTokens: used.CacheCreationTokens,
			CacheReadInputTokens:     used.CachedReadTokens,
		}
	}
	parsed.Session.Tokens = tokens
	parsed.Session.ObservedModels = slices.Sorted(maps.Keys(tokens))
	if cost, ok := usage.Session.cost(); ok {
		parsed.RecordedCost = &cost
	}
}

func hasTokenCounts(used usageCounts) bool {
	return used.InputTokens != 0 || used.OutputTokens != 0 || used.CachedReadTokens != 0 || used.CacheCreationTokens != 0
}

func (u usageCounts) cost() (float64, bool) {
	if u.CostUSDTicks == nil || u.CostIsPartial || u.UsageIsIncomplete {
		return 0, false
	}
	return float64(*u.CostUSDTicks) / ticksPerUSD, true
}

type updatesSummary struct {
	firstPrompt   string
	finishedTurns int
	openToolCalls int
	inTurn        bool
	edits         *session.FileEditSet
}

func readUpdates(ctx context.Context, path string) (updatesSummary, error) {
	result := updatesSummary{edits: session.NewFileEditSet()}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer file.Close()
	firstPromptDone := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return updatesSummary{}, err
		}
		var line updateLine
		if json.Unmarshal(scanner.Bytes(), &line) != nil {
			continue
		}
		update := line.Params.Update
		if update.Kind == "tool_call_update" {
			// The diff repeats on the update that completes the call, so count only that one.
			var items []toolContent
			if update.Status == "completed" && json.Unmarshal(update.Content, &items) == nil {
				for _, item := range items {
					if item.Type == "diff" && item.Path != "" {
						result.edits.Add(item.Path, session.CountLines(item.NewText), session.CountLines(item.OldText), item.OldText == "")
						result.edits.Change(item.Path, item.OldText, item.NewText)
					}
				}
			}
			continue
		}
		var content chunkContent
		_ = json.Unmarshal(update.Content, &content)
		if content.Meta.BashCommand != nil {
			// A "!cmd" shell row is not a prompt and starts no model turn.
			continue
		}
		if update.Kind != "user_message_chunk" && result.firstPrompt != "" {
			firstPromptDone = true
		}
		switch update.Kind {
		case "user_message_chunk":
			result.inTurn = true
			// A hidden chunk is an injected system reminder, not the user's prompt.
			if !firstPromptDone && !update.Meta.HideFromScrollback && content.Type == "text" {
				result.firstPrompt += content.Text
			}
		case "tool_call":
			result.inTurn = true
			result.openToolCalls++
		case "turn_completed":
			result.finishedTurns++
			result.openToolCalls = 0
			result.inTurn = false
		}
	}
	result.firstPrompt = strings.TrimSpace(result.firstPrompt)
	if err := ctx.Err(); err != nil {
		return updatesSummary{}, err
	}
	return result, scanner.Err()
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("grok: %s exceeds %d bytes", filepath.Base(path), limit)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("grok: %s exceeds %d bytes", filepath.Base(path), limit)
	}
	return data, nil
}

// readTodos keeps plan.json order. A cancelled todo is left out, and only completed is done.
func readTodos(path string) []session.Todo {
	todos := []session.Todo{}
	var plan struct {
		Todos json.RawMessage `json:"todos"`
	}
	if readJSON(path, &plan) != nil || len(plan.Todos) == 0 {
		return todos
	}
	decoder := json.NewDecoder(bytes.NewReader(plan.Todos))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return todos
	}
	for decoder.More() {
		var item struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		}
		if _, err := decoder.Token(); err != nil || decoder.Decode(&item) != nil {
			return []session.Todo{}
		}
		if text := strings.TrimSpace(item.Content); text != "" && item.Status != "cancelled" {
			todos = append(todos, session.Todo{Text: text, Done: item.Status == "completed"})
		}
	}
	return todos
}

func readJSON(path string, target any) error {
	data, err := readBounded(path, maxGrokJSONBytes)
	if err != nil {
		return err
	}
	value := reflect.ValueOf(target)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return errors.New("grok: json target must be a pointer")
	}
	scratch := reflect.New(value.Elem().Type())
	if err := json.Unmarshal(data, scratch.Interface()); err != nil {
		return err
	}
	value.Elem().Set(scratch.Elem())
	return nil
}

// readOptionalJSON leaves target unset when the file is absent or caught mid-rewrite.
func readOptionalJSON(path string, target any) {
	_ = readJSON(path, target)
}

func parseTime(value string) int64 {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return 0
	}
	return parsed.UnixMilli()
}

func nonEmpty(value string) *string {
	if value = strings.TrimSpace(value); value == "" {
		return nil
	}
	return &value
}
