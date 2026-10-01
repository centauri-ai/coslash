package pi

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

type projectedPayload struct {
	ModelID          string `json:"modelId"`
	Name             string `json:"name"`
	Summary          string `json:"summary"`
	TargetID         string `json:"targetId"`
	FirstKeptEntryID string `json:"firstKeptEntryId"`
	Replacement      *struct {
		Content json.RawMessage `json:"content"`
	} `json:"replacement"`
	Message struct {
		Role        string          `json:"role"`
		Content     json.RawMessage `json:"content"`
		IsError     bool            `json:"isError"`
		StopReason  string          `json:"stopReason"`
		ToolCallID  string          `json:"toolCallId"`
		NestedCalls *struct {
			Calls []contentBlock `json:"calls"`
		} `json:"nestedCalls"`
	} `json:"message"`
}

type contentBlock struct {
	Status    string `json:"status"`
	ID        string `json:"id"`
	Type      string `json:"type"`
	Text      string `json:"text"`
	Name      string `json:"name"`
	Arguments struct {
		Command string         `json:"command"`
		Path    string         `json:"path"`
		Content string         `json:"content"`
		OldText string         `json:"oldText"`
		NewText string         `json:"newText"`
		Todos   []session.Todo `json:"todos"`
	} `json:"arguments"`
}

func contentText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var texts []string
	for _, b := range blocks {
		if b.Type == "text" {
			texts = append(texts, b.Text)
		}
	}
	return strings.Join(texts, "\n")
}
func entryTime(e entry) int64 {
	t, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

func ancestry(t *transcript, leaf string) ([]int, bool) {
	var reverse []int
	seen := map[string]bool{}
	for leaf != "" {
		i, exists := t.ByID[leaf]
		if !exists || seen[leaf] {
			return nil, false
		}
		seen[leaf] = true
		reverse = append(reverse, i)
		e := t.Entries[i]
		if e.ParentID == nil {
			break
		}
		leaf = *e.ParentID
	}
	path := make([]int, len(reverse))
	for i := range reverse {
		path[len(reverse)-1-i] = reverse[i]
	}
	return path, true
}

func project(t *transcript, parent *transcript) (*vendors.ParsedSession, error) {
	return projectContext(context.Background(), t, parent)
}
func projectContext(ctx context.Context, t *transcript, parent *transcript) (*vendors.ParsedSession, error) {
	leaf := ""
	if len(t.Entries) > 0 {
		leaf = t.Entries[len(t.Entries)-1].ID
	}
	liveLeaf, live := runtimeLeafContext(ctx, t.Header.ID, t.Path)
	if live {
		if _, ok := t.ByID[liveLeaf]; ok {
			leaf = liveLeaf
		} else {
			live = false
		}
	}
	return projectLeafContext(ctx, t, parent, leaf, live)
}

func projectLeaf(t *transcript, parent *transcript, leaf string, live bool) (*vendors.ParsedSession, error) {
	return projectLeafContext(context.Background(), t, parent, leaf, live)
}
func projectLeafContext(ctx context.Context, t *transcript, parent *transcript, leaf string, live bool) (*vendors.ParsedSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := &session.Session{Agent: vendors.AgentPi, ID: t.Header.ID, WorkingDirectory: t.Header.CWD, TranscriptPath: t.Path, Tokens: map[string]session.ModelTokens{}, Subagents: []session.Subagent{}}
	s.Todos = []session.Todo{}
	s.Commits = []string{}
	unknown := "unknown"
	result := &vendors.ParsedSession{Session: s, LogPath: t.Path, StatusHint: &unknown}
	if stamp, err := time.Parse(time.RFC3339Nano, t.Header.Timestamp); err == nil {
		s.StartedAt = stamp.UnixMilli()
	}
	s.LastActivityTime = s.StartedAt
	inherited, verified := inheritedEntries(t, parent)
	path, pathOK := ancestry(t, leaf)
	selected := map[string]bool{}
	for _, i := range path {
		selected[t.Entries[i].ID] = true
	}
	if !pathOK {
		verified = false
	}
	payloads := make([]projectedPayload, len(t.Entries))
	for i, e := range t.Entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch e.Type {
		case "message", "model_change", "session_info", "compaction", "branch_summary", "context_edit":
			_ = json.Unmarshal(e.Raw, &payloads[i])
			if e.Type == "session_info" {
				result.Name = payloads[i].Name
			}
		}
	}
	completedTools := map[string]bool{}
	nearestAssistant := map[string]string{}
	for i, e := range t.Entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p := payloads[i]
		if e.ParentID != nil {
			nearestAssistant[e.ID] = nearestAssistant[*e.ParentID]
		}
		if p.Message.Role == "assistant" {
			nearestAssistant[e.ID] = e.ID
		}
		if p.Message.Role == "toolResult" && !p.Message.IsError && p.Message.ToolCallID != "" && nearestAssistant[e.ID] != "" {
			completedTools[nearestAssistant[e.ID]+"\x00"+p.Message.ToolCallID] = true
		}
	}

	turns, segments := map[string]int{}, map[string]string{}
	firstChild := map[string]string{}
	for _, e := range t.Entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.Type == "label" {
			continue
		}
		parentID, ok := withoutLabels(t, e.ParentID)
		if ok && parentID != "" && firstChild[parentID] == "" {
			firstChild[parentID] = e.ID
		}
	}

	commands := session.CommandLog{}
	toolCommands := map[string]string{}
	edits := session.NewFileEditSet()
	cost := 0.0
	s.TokensUnavailable, s.CostUnavailable = !verified || t.Incomplete, !verified || t.Incomplete
	for i, e := range t.Entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.NestedDetailIncomplete {
			s.DetailsIncomplete = true
		}

		p := payloads[i]
		stamp := entryTime(e)
		if stamp > s.LastActivityTime {
			s.LastActivityTime = stamp
		}
		turn, segment := 0, e.ID
		if e.Type == "label" {
			segment = ""
		}
		if e.ParentID != nil && *e.ParentID != "" {
			if parentIndex, ok := t.ByID[*e.ParentID]; ok && parentIndex < i {
				turn = turns[*e.ParentID]
				if e.Type == "label" {
					segment = segments[*e.ParentID]
				} else if parentID, parentOK := withoutLabels(t, e.ParentID); parentOK && firstChild[parentID] == e.ID {
					segment = segments[parentID]
				}
			} else {
				verified = false
				s.TokensUnavailable, s.CostUnavailable = true, true
			}
		}
		if p.Message.Role == "user" {
			turn++
			s.Turns++
		}
		turns[e.ID], segments[e.ID] = turn, segment
		u := e.Usage
		if u != nil && !inherited[e.ID] {
			optionalAbsent := (u.Source == "tool_result" || u.Source == "compaction" || u.Source == "branch_summary") && (len(u.Raw) == 0 || string(u.Raw) == "null")
			if !optionalAbsent {
				if u.TokensMissing || u.ZeroTokens {
					s.TokensUnavailable = true
				} else {
					bucket := session.ModelTokens{InputTokens: *u.Input, OutputTokens: *u.Output, CacheReadInputTokens: *u.CacheRead, CacheCreationInputTokens: *u.CacheWrite}
					if u.CacheWrite1h != nil {
						bucket.CacheCreation1hInputTokens = *u.CacheWrite1h
						bucket.CacheCreationInputTokens -= *u.CacheWrite1h
						if bucket.CacheCreationInputTokens < 0 {
							s.TokensUnavailable = true
						}
					}
					if u.Model == "" {
						if s.UnattributedTokens == nil {
							s.UnattributedTokens = &session.ModelTokens{}
						}
						if !addTokens(s.UnattributedTokens, bucket) {
							s.TokensUnavailable = true
						}
					} else {
						old := s.Tokens[u.Model]
						if !addTokens(&old, bucket) {
							s.TokensUnavailable = true
						}
						s.Tokens[u.Model] = old
					}
				}
				if u.CostMissing || u.ZeroCost {
					s.CostUnavailable = true
				} else {
					cost += *u.Cost.Total
				}
			}
		}
		if p.Message.Role == "toolResult" {
			if command := toolCommands[nearestAssistant[e.ID]+"\x00"+p.Message.ToolCallID]; command != "" {
				s.CommitLog = append(s.CommitLog, session.ParseCommitObservations(command, contentText(p.Message.Content), !p.Message.IsError)...)
				if !p.Message.IsError && session.IsPullRequestCreate(command) {
					s.PullRequests++
				}
			}
		}
		if e.Type == "compaction" {
			s.Compactions++
		}
		if p.Message.IsError || p.Message.StopReason == "error" || p.Message.StopReason == "aborted" {
			s.Errors++
		}
		var blocks []contentBlock
		_ = json.Unmarshal(p.Message.Content, &blocks)
		if p.Message.NestedCalls != nil {
			for _, b := range p.Message.NestedCalls.Calls {
				b.Type = "toolCall"
				blocks = append(blocks, b)
				if b.Status == "error" {
					s.Errors++
				}
			}
		}
		for _, b := range blocks {
			if b.Type != "toolCall" {
				continue
			}
			s.ToolUses++
			if b.Arguments.Command != "" {
				commands.Note(b.Arguments.Command, "")
				toolCommands[e.ID+"\x00"+b.ID] = b.Arguments.Command
			}
			a := b.Arguments
			if a.Path != "" && (b.Name == "write" || b.Name == "edit") && (b.Status == "ok" || completedTools[e.ID+"\x00"+b.ID]) {
				if b.Name == "write" {
					edits.Add(a.Path, session.CountLines(a.Content), 0, false)
					edits.Write(a.Path, a.Content)
				} else {
					edits.Add(a.Path, session.CountLines(a.NewText), session.CountLines(a.OldText), false)
					edits.Change(a.Path, a.OldText, a.NewText)
				}
				if stamp != 0 {
					last := stamp
					s.LastEditAt = &last
				}
			}
		}
	}
	if math.IsInf(cost, 0) {
		s.CostUnavailable = true
	}
	if s.TokensUnavailable {
		s.Tokens = map[string]session.ModelTokens{}
		s.UnattributedTokens = nil
	}
	if !s.CostUnavailable {
		result.RecordedCost = &cost
		s.Cost = &cost
	}
	s.TokensKnown = !s.TokensUnavailable
	s.Commands, s.FileEdits, s.EditedFileCount = commands.Raw(), edits.Edits, len(edits.Edits)
	result.Commands = commands.Labelled()
	if s.StartedAt > 0 && s.LastActivityTime >= s.StartedAt {
		duration := int(s.LastActivityTime - s.StartedAt)
		s.DurationMs = &duration
	}
	// Current state follows ancestry, not the chronology used by the inspector.
	for _, i := range path {
		e, p := t.Entries[i], payloads[i]
		if e.Type == "compaction" {
			s.ContextTokens = nil
		}
		if p.ModelID != "" && e.Type == "model_change" {
			model := p.ModelID
			s.Model = &model
		}
		if e.Usage != nil && e.Usage.Source == "assistant" {
			if e.Usage.Model != "" {
				model := e.Usage.Model
				s.Model = &model
			}
			if !e.Usage.TokensMissing && !e.Usage.ZeroTokens {
				n := 0
				for _, count := range []*int{e.Usage.Input, e.Usage.Output, e.Usage.CacheRead, e.Usage.CacheWrite} {
					if *count > int(^uint(0)>>1)-n {
						n = -1
						break
					}
					n += *count
				}
				if n >= 0 {
					s.ContextTokens = &n
				} else {
					s.ContextTokens = nil
				}
			} else {
				s.ContextTokens = nil
			}
		}
	}
	// Compaction/context edits affect selected conversational content, never recorded accounting.
	context := append([]int(nil), path...)
	for n, i := range path {
		if t.Entries[i].Type == "compaction" {
			s.CompactionSeed = payloads[i].Summary
			context = []int{i}
			keep := false
			for _, j := range path[:n] {
				if t.Entries[j].ID == payloads[i].FirstKeptEntryID {
					keep = true
				}
				if keep && t.Entries[j].Type != "compaction" {
					context = append(context, j)
				}
			}
			context = append(context, path[n+1:]...)
		}
	}
	contextEdits := map[string]*struct {
		Content json.RawMessage `json:"content"`
	}{}
	for _, i := range context {
		if t.Entries[i].Type == "context_edit" {
			contextEdits[payloads[i].TargetID] = payloads[i].Replacement
		}
	}
	for _, i := range context {
		e, p := t.Entries[i], payloads[i]
		raw := p.Message.Content
		if replacement, ok := contextEdits[e.ID]; ok {
			if replacement == nil {
				continue
			}
			raw = replacement.Content
		}
		text := strings.TrimSpace(contentText(raw))
		if p.Message.Role == "user" && text != "" && s.FirstPrompt == nil {
			prompt := text
			if runes := []rune(prompt); len(runes) > session.TruncateTextLimit {
				prompt = string(runes[:session.TruncateTextLimit-1]) + "…"
			}
			s.FirstPrompt = &prompt
		}
		if p.Message.Role == "assistant" && text != "" {
			summary := session.Truncate(text, session.TruncateTextLimit)
			s.Summary = &summary
		}
		if (e.Type == "branch_summary" || e.Type == "compaction") && p.Summary != "" {
			summary := session.Truncate(p.Summary, session.TruncateTextLimit)
			s.Summary = &summary
		}
		var blocks []contentBlock
		_ = json.Unmarshal(raw, &blocks)
		for _, b := range blocks {
			if b.Type == "toolCall" && b.Arguments.Todos != nil {
				s.Todos = b.Arguments.Todos
			}
		}
	}
	// A total sort key keeps missing timestamps deterministic and comparator transitive.
	order := make([]int, len(t.Entries))
	keys := make([]int64, len(order))
	previous := s.StartedAt
	for i, e := range t.Entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		order[i] = i
		stamp := entryTime(e)
		if stamp != 0 {
			previous = stamp
		}
		keys[i] = previous
	}
	sort.SliceStable(order, func(i, j int) bool { return keys[order[i]] < keys[order[j]] })
	s.Digest = []session.DigestEntry{}
	contextIDs := map[string]bool{}
	for _, i := range context {
		contextIDs[t.Entries[i].ID] = true
	}
	for _, i := range order {
		e, p := t.Entries[i], payloads[i]
		category, text := "", ""
		switch {
		case p.Message.Role == "user":
			category, text = session.DigestUser, contentText(p.Message.Content)
			if turns[e.ID] == 1 {
				category = session.DigestFirstPrompt
			}
		case p.Message.Role == "assistant":
			category, text = session.DigestRecap, contentText(p.Message.Content)
		case e.Type == "compaction":
			category, text = session.DigestCompaction, p.Summary
		case e.Type == "branch_summary":
			category, text = session.DigestRecap, p.Summary
		}
		log := session.DigestLog{}
		if category != "" && strings.TrimSpace(text) != "" {
			log.Push(turns[e.ID], category, text, entryTime(e))
		}
		var blocks []contentBlock
		_ = json.Unmarshal(p.Message.Content, &blocks)
		var todos []session.Todo
		for _, block := range blocks {
			if block.Type == "toolCall" && block.Arguments.Todos != nil {
				todos = block.Arguments.Todos
			}
		}
		if todos != nil {
			log.Push(turns[e.ID], session.DigestTodos, todoDescription(todos), entryTime(e))
		}

		for _, row := range log.Entries() {
			selectedContext := contextIDs[e.ID]
			if replacement, superseded := contextEdits[e.ID]; selectedContext && superseded && e.Type == "message" {
				if replacement == nil {
					selectedContext = false
				} else {
					replacementText := contentText(replacement.Content)
					if row.Category == session.DigestTodos {
						var replacementBlocks []contentBlock
						_ = json.Unmarshal(replacement.Content, &replacementBlocks)
						var todos []session.Todo
						for _, block := range replacementBlocks {
							if block.Type == "toolCall" && block.Arguments.Todos != nil {
								todos = block.Arguments.Todos
							}
						}
						if todos == nil {
							selectedContext = false
						} else {
							replacementText = todoDescription(todos)
						}
					}
					if selectedContext {
						replacementLog := session.DigestLog{}
						replacementLog.Push(row.Turn, row.Category, replacementText, row.Time)
						description := replacementLog.Entries()[0].Description
						row.ContextDescription = &description
					}
				}
			}
			row.ContextSelected = &selectedContext
			row.SourceEntryID, row.BranchID = e.ID, segments[e.ID]
			if e.ParentID != nil {
				row.ParentEntryID = *e.ParentID
			}
			copied := inherited[e.ID]
			if verified {
				row.Inherited = &copied
			}
			if live && pathOK {
				active := selected[e.ID]
				row.Active = &active
			} else if live && e.ID == leaf {
				active := true
				row.Active = &active
			}
			s.Digest = append(s.Digest, row)
		}

	}
	return result, nil
}

func addTokens(dst *session.ModelTokens, src session.ModelTokens) bool {
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.CacheReadInputTokens += src.CacheReadInputTokens
	dst.CacheCreationInputTokens += src.CacheCreationInputTokens
	dst.CacheCreation1hInputTokens += src.CacheCreation1hInputTokens
	return dst.InputTokens >= 0 && dst.OutputTokens >= 0 && dst.CacheReadInputTokens >= 0 && dst.CacheCreationInputTokens >= 0 && dst.CacheCreation1hInputTokens >= 0
}

func todoDescription(todos []session.Todo) string {
	var text strings.Builder
	for _, todo := range todos {
		state := "[ ] "
		if todo.Done {
			state = "[x] "
		}
		text.WriteString(state + todo.Text + "\n")
	}
	return text.String()
}
