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
	"unicode/utf8"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
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
	TurnCount              int `json:"turnCount"`
	ErrorCount             int `json:"errorCount"`
	CompactionCount        int `json:"compactionCount"`
	ContextTokensUsed      int `json:"contextTokensUsed"`
	ContextWindowTokens    int `json:"contextWindowTokens"`
	ToolCallCount          int `json:"toolCallCount"`
	SessionDurationSeconds int `json:"sessionDurationSeconds"`
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
			Kind       string          `json:"sessionUpdate"`
			Status     string          `json:"status"`
			ToolCallID string          `json:"toolCallId"`
			Title      string          `json:"title"`
			Content    json.RawMessage `json:"content"`
			RawOutput  json.RawMessage `json:"rawOutput"`
			Meta       struct {
				HideFromScrollback bool `json:"hideFromScrollback"`
			} `json:"_meta"`
		} `json:"update"`
		Meta struct {
			UpdateParams struct {
				Status string `json:"status"`
			} `json:"updateParams"`
		} `json:"_meta"`
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

// bashOutput is the rawOutput of a completed terminal command.
type bashOutput struct {
	Type     string `json:"type"`
	Command  string `json:"command"`
	Output   string `json:"output_for_prompt"`
	ExitCode *int   `json:"exit_code"`
	TimedOut bool   `json:"timed_out"`
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
	s.Commands = updates.commands.Raw()
	s.CommitLog = updates.commitLog
	s.PullRequests = updates.pullRequests
	s.Todos = readTodos(filepath.Join(dir, "plan.json"))
	var goal struct {
		Objective string `json:"objective"`
	}
	readOptionalJSON(filepath.Join(dir, "goal", "state.json"), &goal)
	s.DeclaredGoal = nonEmpty(goal.Objective)

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
	if signals.SessionDurationSeconds > 0 {
		ms := signals.SessionDurationSeconds * 1000
		s.DurationMs = &ms
	}
	s.CompactionSeed = readCompactionSeed(filepath.Join(dir, "compaction_checkpoints"))
	turn := max(updates.userTurn, 1)
	if summary.LastTurnSummary != "" {
		updates.digest.Push(turn, session.DigestRecap, summary.LastTurnSummary, 0)
	}
	if s.CompactionSeed != "" {
		updates.digest.Push(turn, session.DigestCompaction, s.CompactionSeed, 0)
	}
	if len(s.Todos) > 0 {
		texts := make([]string, len(s.Todos))
		for i, todo := range s.Todos {
			texts[i] = todo.Text
		}
		updates.digest.Push(turn, session.DigestTodos, strings.Join(texts, "; "), 0)
	}
	if plan := readOptionalText(filepath.Join(dir, "plan.md")); plan != "" {
		updates.digest.Push(turn, session.DigestPlan, plan, 0)
	}
	s.Digest = updates.digest.Entries()
	s.Entrypoint = grokEntrypoint(summary.SessionKind, promptNonInteractive(dir))
	if updates.waitingForUser() || planAwaitingApproval(dir) {
		status := "waiting"
		s.Status = &status
	}
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

	parsed := &vendors.ParsedSession{
		Session: s, LogPath: filepath.Join(dir, "updates.jsonl"), InTurn: updates.inTurn, Commands: updates.commands.Labelled(),
	}
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
	pendingTools  map[string]struct{}
	edits         *session.FileEditSet
	commands      session.CommandLog
	commitLog     []session.CommitObservation
	pullRequests  int
	digest        session.DigestLog
	userTurn      int
	userBuf       string
	assistantBuf  strings.Builder
	sawUser       bool
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
		if update.Status == "" {
			update.Status = line.Params.Meta.UpdateParams.Status
		}
		update.Status = strings.ToLower(update.Status)
		if update.Kind == "tool_call_update" {
			result.noteTool(update.ToolCallID, update.Title, update.Status)
			// The diff repeats on the update that completes the call, so count only that one.
			var items []toolContent
			var shell bashOutput
			if update.Status == "completed" && json.Unmarshal(update.RawOutput, &shell) == nil && shell.Type == "Bash" {
				result.noteCommand(shell)
			}
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
		if update.Kind != "user_message_chunk" {
			result.flushUser()
		}
		switch update.Kind {
		case "user_message_chunk":
			result.inTurn = true
			// A hidden chunk is an injected system reminder, not the user's prompt.
			if !update.Meta.HideFromScrollback && content.Type == "text" {
				if !firstPromptDone {
					result.firstPrompt += content.Text
				}
				result.userBuf += content.Text
			}
		case "tool_call":
			result.inTurn = true
			result.openToolCalls++
			result.noteTool(update.ToolCallID, update.Title, update.Status)
			result.assistantBuf.Reset()
		case "agent_message_chunk":
			if content.Type == "text" {
				remaining := fullsessionv1.MaxStringBytes + utf8.UTFMax - result.assistantBuf.Len()
				result.assistantBuf.WriteString(content.Text[:min(len(content.Text), remaining)])
			}
		case "turn_completed":
			if text := strings.TrimSpace(result.assistantBuf.String()); text != "" {
				result.digest.Push(result.userTurn, session.DigestRecap, text, 0)
			}
			result.assistantBuf.Reset()
			result.finishedTurns++
			result.openToolCalls = 0
			result.inTurn = false
			result.pendingTools = nil
		}
	}
	result.flushUser()
	result.firstPrompt = strings.TrimSpace(result.firstPrompt)
	if err := ctx.Err(); err != nil {
		return updatesSummary{}, err
	}
	return result, scanner.Err()
}

func (r *updatesSummary) noteTool(id, title, status string) {
	if id == "" {
		return
	}
	switch status {
	case "completed", "failed", "cancelled", "in_progress", "inprogress":
		delete(r.pendingTools, id)
		return
	case "pending":
		if r.pendingTools == nil {
			r.pendingTools = map[string]struct{}{}
		}
		r.pendingTools[id] = struct{}{}
		return
	}
	if status == "" && isGrokUserQuestion(title) {
		if r.pendingTools == nil {
			r.pendingTools = map[string]struct{}{}
		}
		r.pendingTools[id] = struct{}{}
	}
}

func (r updatesSummary) waitingForUser() bool {
	return r.inTurn && len(r.pendingTools) > 0
}

func isGrokUserQuestion(title string) bool {
	title = strings.ToLower(strings.TrimSpace(title))
	return title == "ask_user_question" || title == "askuserquestion"
}

func grokEntrypoint(kind string, nonInteractive bool) *string {
	value := "grok-cli"
	switch {
	case isSubagentKind(kind):
		value = "grok-subagent"
	case kind == "headless" || nonInteractive:
		value = "grok-headless"
	}
	return &value
}

func promptNonInteractive(dir string) bool {
	var prompt struct {
		NonInteractive bool `json:"is_non_interactive"`
	}
	readOptionalJSON(filepath.Join(dir, "prompt_context.json"), &prompt)
	return prompt.NonInteractive
}

func planAwaitingApproval(dir string) bool {
	var plan struct {
		Awaiting bool `json:"awaiting_plan_approval"`
	}
	readOptionalJSON(filepath.Join(dir, "plan_mode.json"), &plan)
	return plan.Awaiting
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

func (r *updatesSummary) flushUser() {
	text := strings.TrimSpace(r.userBuf)
	r.userBuf = ""
	if text == "" {
		return
	}
	category := session.DigestUser
	if !r.sawUser {
		category = session.DigestFirstPrompt
		r.sawUser = true
	}
	r.userTurn++
	r.digest.Push(r.userTurn, category, text, 0)
}

func readOptionalText(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(body)
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

// commandHasOption reports whether option is a shell word outside quotes.
func commandHasOption(command, option string) bool {
	for _, field := range strings.Fields(maskQuotedShell(command)) {
		if field == option || strings.HasPrefix(field, option+"=") {
			return true
		}
	}
	return false
}

// maskQuotedShell blanks quoted text and escaped characters so option checks ignore commit messages.
func maskQuotedShell(command string) string {
	masked := []byte(command)
	var quote byte
	for i := 0; i < len(masked); i++ {
		char := masked[i]
		if quote != '\'' && char == '\\' {
			masked[i] = ' '
			if i+1 < len(masked) {
				i++
				masked[i] = ' '
			}
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			masked[i] = ' '
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			masked[i] = ' '
		}
	}
	return string(masked)
}

func (result *updatesSummary) noteCommand(shell bashOutput) {
	if shell.Command == "" {
		return
	}
	result.commands.Note(shell.Command, "")
	if commandHasOption(shell.Command, "--dry-run") {
		return
	}
	succeeded := shell.ExitCode != nil && *shell.ExitCode == 0 && !shell.TimedOut
	result.commitLog = append(result.commitLog, session.ParseCommitObservations(shell.Command, shell.Output, succeeded)...)
	if succeeded && session.IsPullRequestCreate(shell.Command) && len(session.PullRequestURLs(shell.Output)) > 0 {
		result.pullRequests++
	}
}

// compactionSummaryPrefix opens the summary item that Grok puts in compacted_history.
const compactionSummaryPrefix = "This session is being continued from a previous conversation"

// readCompactionSeed returns the summary text of the newest compaction checkpoint.
func readCompactionSeed(dir string) string {
	entries, _ := os.ReadDir(dir)
	seed, newest := "", int64(-1)
	for _, entry := range entries {
		var checkpoint struct {
			CreatedAt        string `json:"created_at"`
			CompactedHistory []struct {
				Type    string          `json:"type"`
				Content json.RawMessage `json:"content"`
			} `json:"compacted_history"`
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" ||
			readJSON(filepath.Join(dir, entry.Name()), &checkpoint) != nil {
			continue
		}
		createdAt := parseTime(checkpoint.CreatedAt)
		if createdAt < newest {
			continue
		}
		for _, item := range checkpoint.CompactedHistory {
			if text := itemText(item.Content); item.Type == "user" && strings.HasPrefix(text, compactionSummaryPrefix) {
				seed, newest = text, createdAt
			}
		}
	}
	return seed
}

// itemText reads a chat item's content, which is a string or a list of text blocks.
func itemText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return strings.TrimSpace(text)
	}
	var blocks []chunkContent
	_ = json.Unmarshal(content, &blocks)
	parts := []string{}
	for _, block := range blocks {
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
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
	// Validate into a temporary first. A truncated file must not leave a partial
	// value behind, and a later decode into target keeps fields json ignores.
	scratch := reflect.New(value.Elem().Type())
	if err := json.Unmarshal(data, scratch.Interface()); err != nil {
		return err
	}
	return json.Unmarshal(data, target)
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
