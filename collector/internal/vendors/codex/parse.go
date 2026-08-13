package codex

import (
	"cmp"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

type codexSessionAnalysis struct {
	sessionID        string
	forkedFromID     string
	parentThreadID   string
	agentNickname    string
	taskName         string
	workingDirectory string
	branch           *string
	entrypoint       *string
	model            string
	timestamps       session.TimestampRange
	turnStartTime    *int64
	activeDurationMs int64
	firstUserPrompt  string
	prompts          int
	turnFinalReply   string
	turns            int
	toolUseCount     int
	compactions      int
	tokenInfo        *codexTokenInfo
	spawnTurns       map[string]int
	tokenSamples     []codexTokenSample
	commands         session.CommandLog
	commits          []string
	pullRequests     int
	fileEdits        *session.FileEditSet
	digest           session.DigestLog
	plan             []planStep
	turnDepth        int
	lastTurnAborted  bool
	inReview         bool
}

// Review mode's own generated instruction is not a user turn
func (analysis *codexSessionAnalysis) notePrompt(prompt string) {
	analysis.prompts++
	if analysis.firstUserPrompt == "" {
		analysis.firstUserPrompt = prompt
	}
	category := session.DigestUser
	if analysis.prompts == 1 {
		category = session.DigestFirstPrompt
	}
	analysis.digest.Push(analysis.prompts, category, prompt)
}

func (analysis *codexSessionAnalysis) noteFileChanges(changes codexPatchChanges) {
	for _, entry := range changes {
		change := entry.Change
		isNew := change.Type == "add"
		additions, deletions := unifiedDiffStat(change.UnifiedDiff)
		if change.UnifiedDiff == "" && isNew {
			additions = session.CountLines(change.Content)
		}
		analysis.fileEdits.Add(entry.Path, additions, deletions, isNew)
	}
}

func completedItemText(item codexItem) string {
	text := make([]string, 0, len(item.Content))
	for _, content := range item.Content {
		if content.Text != "" {
			text = append(text, content.Text)
		}
	}
	return strings.Join(text, "\n")
}

// codexFork carries what the fork stage needs: the rollout's cumulative
// token_count sequence and its fork parent, if any.
type codexFork struct {
	forkedFromID string
	samples      []codexTokenSample
}

// Parse turns one rollout into a Parsed. Tokens are bucketed with no fork
// baseline — the fork stage redoes the bucketing against the parent's shared
// prefix using the Fork payload.
func Parse(path string) (*vendors.ParsedTranscript, error) {
	analysis, err := analyzeCodexSession(path)
	if err != nil {
		return nil, err
	}
	parsed := &vendors.ParsedTranscript{
		Session:    analysis.unifiedSession(path),
		ParentID:   analysis.parentThreadID,
		InTurn:     analysis.turnDepth > 0,
		Stopped:    analysis.lastTurnAborted,
		SpawnTurns: analysis.spawnTurns,
		Commands:   analysis.commands.Labelled(),
		ForkUsage:  codexFork{forkedFromID: analysis.forkedFromID, samples: analysis.tokenSamples},
	}
	if parsed.ParentID != "" {
		// Codex does not ship agent description
		parsed.Name = cmp.Or(analysis.taskName, analysis.firstUserPrompt, analysis.agentNickname)
		parsed.SpawnKey = parsed.Session.ID
	} else {
		parsed.Name = analysis.firstUserPrompt
	}
	return parsed, nil
}

func analyzeCodexSession(file string) (*codexSessionAnalysis, error) {
	rows, err := session.ParseJSONL[codexRow](file)
	if err != nil {
		return nil, err
	}
	questionAnswers := questionAnswersByCall(rows)
	ownID := SessionIDFromRollout(file)
	var metas []codexMeta
	analysis := &codexSessionAnalysis{
		spawnTurns: map[string]int{},
		fileEdits:  session.NewFileEditSet(),
		commits:    []string{},
	}
	for _, row := range rows {
		var timestamp *int64
		if row.Timestamp != "" {
			milliseconds, err := session.RFC3339ToUnixEpoch(row.Timestamp)
			if err != nil {
				log.Printf("%s: skipping unparseable timestamp %q: %v", file, row.Timestamp, err)
			} else {
				timestamp = &milliseconds
				analysis.timestamps.Note(milliseconds)
			}
		}
		switch row.Type {
		case "session_meta":
			analysis.sessionID = row.Payload.SessionID
			analysis.workingDirectory = row.Payload.Cwd
			if row.Payload.Originator != "" {
				analysis.entrypoint = &row.Payload.Originator
			}
			if row.Payload.Git != nil && row.Payload.Git.Branch != "" {
				analysis.branch = &row.Payload.Git.Branch
			}
			meta := codexMeta{
				payloadID:        row.Payload.ID,
				sessionID:        row.Payload.SessionID,
				forkedFromID:     row.Payload.ForkedFromID,
				parentThreadID:   row.Payload.ParentThreadID,
				agentNickname:    row.Payload.AgentNickname,
				workingDirectory: row.Payload.Cwd,
				entrypoint:       row.Payload.Originator,
			}
			if row.Payload.Git != nil {
				meta.branch = row.Payload.Git.Branch
			}
			metas = append(metas, meta)
		case "turn_context":
			if row.Payload.Model != "" {
				analysis.model = row.Payload.Model
			}
		case "event_msg":
			switch row.Payload.Type {
			case "item_completed":
				item := row.Payload.Item
				switch item.Type {
				case "UserMessage":
					message := completedItemText(item)
					if !analysis.inReview && message != "" && !session.IsHarnessWrapped(message) {
						analysis.notePrompt(message)
					}
				case "AgentMessage":
					if item.Phase == "final_answer" || item.Phase == "" {
						analysis.turnFinalReply = strings.TrimSpace(completedItemText(item))
					}
				case "FileChange":
					analysis.noteFileChanges(item.Changes)
				}
			case "user_message":
				if analysis.inReview {
					continue
				}
				if row.Payload.Message != "" && !session.IsHarnessWrapped(row.Payload.Message) {
					prompt := promptText(row.Payload.Message)
					if prompt == "" {
						prompt = row.Payload.Message
					}
					analysis.notePrompt(prompt)
				}
			case "entered_review_mode":
				analysis.inReview = true
				review := "/review"
				if row.Payload.UserFacingHint != "" {
					review += " · " + row.Payload.UserFacingHint
				}
				analysis.notePrompt(review)
			case "exited_review_mode":
				analysis.inReview = false
			case "sub_agent_activity":
				if row.Payload.Kind == "started" && row.Payload.AgentThreadID != "" {
					analysis.spawnTurns[row.Payload.AgentThreadID] = max(analysis.prompts, 1)
					analysis.digest.PushSubagent(analysis.prompts, row.Payload.AgentThreadID)
				}
			case "agent_message":
				if row.Payload.Phase == "final_answer" || row.Payload.Phase == "" {
					analysis.turnFinalReply = strings.TrimSpace(row.Payload.Message)
				}
			case "token_count":
				if row.Payload.Info != nil {
					analysis.tokenInfo = row.Payload.Info
					analysis.tokenSamples = append(analysis.tokenSamples, codexTokenSample{
						model: analysis.model,
						usage: row.Payload.Info.TotalTokenUsage,
					})
				}
			case "context_compacted":
				analysis.compactions++
				analysis.digest.Push(analysis.prompts,
					session.DigestCompaction,
					fmt.Sprintf("context compacted (%d)", analysis.compactions),
				)
			case "task_started":
				if analysis.turnDepth == 0 {
					analysis.turnFinalReply = ""
					analysis.turnStartTime = timestamp
				}
				analysis.turns++
				analysis.turnDepth++
			case "task_complete", "turn_aborted":
				if row.Payload.Type == "task_complete" && analysis.turnDepth <= 1 &&
					analysis.prompts > 0 && analysis.turnFinalReply != "" {
					analysis.digest.Push(
						analysis.prompts,
						session.DigestRecap,
						analysis.turnFinalReply,
					)
				}
				analysis.turnFinalReply = ""
				analysis.lastTurnAborted = row.Payload.Type == "turn_aborted"
				analysis.turnDepth = max(0, analysis.turnDepth-1)
				if analysis.turnDepth == 0 && analysis.turnStartTime != nil && timestamp != nil {
					if span := *timestamp - *analysis.turnStartTime; span > 0 {
						analysis.activeDurationMs += span
					}
					analysis.turnStartTime = nil
				}
			case "patch_apply_end":
				analysis.noteFileChanges(row.Payload.Changes)
			}
		case "response_item":
			switch row.Payload.Type {
			case "agent_message":
				if analysis.taskName == "" {
					analysis.taskName = assignedTaskName(row.Payload)
				}
			case "custom_tool_call", "function_call":
				analysis.toolUseCount++
				if command, ok := commandFrom(row.Payload); ok {
					analysis.commands.Note(command, "")
					if message, ok := session.CommitMessage(command); ok {
						analysis.commits = append(analysis.commits, message)
					}
					if session.IsPullRequestCreate(command) {
						analysis.pullRequests++
					}
				}
				if questions := questionsFrom(row.Payload); len(questions) > 0 {
					for _, question := range questions {
						answer := strings.Join(questionAnswers[row.Payload.CallID][question.id].values(), ", ")
						analysis.digest.PushQuestion(analysis.prompts, question.text, answer)
					}
				}
				analysis.notePlan(row.Payload)
			case "function_call_output":
				if id := spawnedAgentID(row.Payload.Output); id != "" {
					analysis.spawnTurns[id] = max(analysis.prompts, 1)
					analysis.digest.PushSubagent(analysis.prompts, id)
				}
				analysis.notePlan(row.Payload)
			}
		}
	}
	// Lineage, parentage, and context all come from the rollout's OWN meta —
	// a fork's inlined ancestor metas must not win last-write on cwd/branch.
	own := ownMeta(metas, ownID)
	analysis.sessionID = own.sessionID
	analysis.forkedFromID = own.forkedFromID
	analysis.parentThreadID = own.parentThreadID
	analysis.agentNickname = own.agentNickname
	analysis.workingDirectory = own.workingDirectory
	analysis.branch = nil
	if own.branch != "" {
		analysis.branch = &own.branch
	}
	analysis.entrypoint = nil
	if own.entrypoint != "" {
		analysis.entrypoint = &own.entrypoint
	}
	return analysis, nil
}

func ownMeta(metas []codexMeta, ownID string) codexMeta {
	if len(metas) == 0 {
		return codexMeta{}
	}
	if ownID != "" {
		for i := len(metas) - 1; i >= 0; i-- {
			meta := metas[i]
			if cmp.Or(meta.payloadID, meta.sessionID) == ownID {
				return meta
			}
		}
	}
	isAncestor := make(map[string]struct{}, len(metas))
	for _, meta := range metas {
		if meta.forkedFromID != "" {
			isAncestor[meta.forkedFromID] = struct{}{}
		}
	}
	head := codexMeta{}
	found := 0
	for _, meta := range metas {
		if _, ok := isAncestor[meta.sessionID]; !ok {
			head = meta
			found++
		}
	}
	if found == 1 {
		return head
	}
	return metas[0]
}

func (analysis *codexSessionAnalysis) unifiedSession(filePath string) *session.Session {
	sessionID := SessionIDFromRollout(filePath)
	if sessionID == "" {
		sessionID = analysis.sessionID
	}
	unifiedSessionDetails := session.SessionDetails{
		LogPath:      filePath,
		Turns:        analysis.turns,
		ToolUses:     analysis.toolUseCount,
		Compactions:  analysis.compactions,
		Errors:       0, // Codex does not report exit code (may change in the future)
		Commands:     analysis.commands.Raw(),
		CommandCount: analysis.commands.Count(),
		Commits:      analysis.commits,
		PullRequests: analysis.pullRequests,
		FileEdits:    analysis.fileEdits.Edits,
		Digest:       analysis.digest.Entries(),
	}
	if analysis.model != "" {
		unifiedSessionDetails.Model = &analysis.model
	}
	if analysis.firstUserPrompt != "" {
		unifiedSessionDetails.FirstPrompt = &analysis.firstUserPrompt
	}
	unifiedSession := session.Session{
		Agent:            vendors.AgentCodex,
		ID:               sessionID,
		WorkingDirectory: analysis.workingDirectory,
		Branch:           analysis.branch,
		Entrypoint:       analysis.entrypoint,
		LastActivityTime: analysis.timestamps.Latest,
		EditedFileCount:  len(analysis.fileEdits.Edits),
		SessionDetails:   unifiedSessionDetails,
		Tokens:           map[string]session.ModelTokens{},
		Subagents:        []session.Subagent{},
	}
	if duration := analysis.durationMs(); duration > 0 || analysis.timestamps.Latest > 0 {
		unifiedSession.DurationMs = &duration
	}
	if unifiedSession.LastActivityTime == 0 {
		unifiedSession.LastActivityTime = session.FileModificationTime(filePath)
	}
	todos := []session.Todo{}
	for _, step := range analysis.plan {
		todos = append(todos, session.Todo{Text: step.step, Done: step.status == "completed"})
	}
	unifiedSession.Todos = todos
	if summary := analysis.digest.LastRecap(); summary != "" {
		unifiedSession.Summary = &summary
	}
	if analysis.tokenInfo != nil {
		unifiedSession.Tokens = tokenBuckets(analysis.tokenSamples, nil, filePath)
		contextTokens := analysis.tokenInfo.LastTokenUsage.InputTokens
		unifiedSession.ContextTokens = &contextTokens
		if analysis.tokenInfo.ModelContextWindow > 0 {
			window := analysis.tokenInfo.ModelContextWindow
			unifiedSession.ContextWindow = &window
		} else {
			unifiedSession.ContextWindow = session.ContextWindowFor(analysis.model)
		}
	}
	return &unifiedSession
}

func tokenBuckets(
	samples []codexTokenSample,
	parentUsages []codexTokenUsage,
	filePath string,
) map[string]session.ModelTokens {
	buckets := map[string]session.ModelTokens{}
	shared := sharedUsagePrefix(samples, parentUsages)
	previous := codexTokenUsage{}
	if shared > 0 {
		previous = samples[shared-1].usage
	}
	for _, sample := range samples[shared:] {
		delta := subtractUsage(sample.usage, previous)
		previous = sample.usage
		if sample.model == "" {
			log.Printf(
				"%s: token usage without a turn_context model, omitting token delta",
				filePath,
			)
			continue
		}
		current := buckets[sample.model]
		// Fresh input cannot go negative in real Codex data when cache-read and
		// cache-write totals move independently, so clamp it per delta.
		current.InputTokens += max(
			0,
			delta.InputTokens-delta.CachedInputTokens-delta.CacheWriteInputTokens,
		)
		current.CacheCreationInputTokens += delta.CacheWriteInputTokens
		current.CacheReadInputTokens += delta.CachedInputTokens
		current.OutputTokens += delta.OutputTokens
		buckets[sample.model] = current
	}
	return buckets
}

func sharedUsagePrefix(samples []codexTokenSample, parent []codexTokenUsage) int {
	shared := 0
	for shared < len(samples) && shared < len(parent) && samples[shared].usage == parent[shared] {
		shared++
	}
	return shared
}

func subtractUsage(total, base codexTokenUsage) codexTokenUsage {
	return codexTokenUsage{
		InputTokens:           max(0, total.InputTokens-base.InputTokens),
		CachedInputTokens:     max(0, total.CachedInputTokens-base.CachedInputTokens),
		CacheWriteInputTokens: max(0, total.CacheWriteInputTokens-base.CacheWriteInputTokens),
		OutputTokens:          max(0, total.OutputTokens-base.OutputTokens),
	}
}

func (analysis *codexSessionAnalysis) durationMs() int {
	if analysis.activeDurationMs > 0 {
		return int(analysis.activeDurationMs)
	}
	return analysis.timestamps.SpanMs()
}

func assignedTaskName(payload codexPayload) string {
	if payload.Recipient == "" || !strings.Contains(string(payload.Content), "NEW_TASK") {
		return ""
	}
	return payload.Recipient[strings.LastIndex(payload.Recipient, "/")+1:]
}

func spawnedAgentID(output json.RawMessage) string {
	var body string
	if json.Unmarshal(output, &body) != nil {
		return ""
	}
	var spawned struct {
		AgentID string `json:"agent_id"`
	}
	if json.Unmarshal([]byte(body), &spawned) != nil {
		return ""
	}
	return spawned.AgentID
}

const requestMarker = "## My request for Codex:"

func promptText(message string) string {
	if marker := strings.Index(message, requestMarker); marker >= 0 {
		message = message[marker+len(requestMarker):]
	}
	return strings.TrimSpace(message)
}

func unifiedDiffStat(diff string) (additions, deletions int) {
	lines := make([]string, 0)
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") ||
			strings.HasPrefix(line, "@@") {
			continue
		}
		lines = append(lines, line)
	}
	return session.DiffStat(lines)
}

var execCmdPattern = regexp.MustCompile(
	`(?:^|[{,]\s*)(?:"cmd"|cmd)\s*:\s*("(?:[^"\\]|\\.)*")`,
)
var requestUserInputCallPattern = regexp.MustCompile(`\brequest_user_input\s*\(`)
var questionIDPattern = regexp.MustCompile(`"id"\s*:\s*("(?:[^"\\]|\\.)*")`)
var questionPattern = regexp.MustCompile(`"question"\s*:\s*("(?:[^"\\]|\\.)*")`)
var planStepPattern = regexp.MustCompile(
	`"?step"?\s*:\s*("(?:[^"\\]|\\.)*")\s*,\s*"?status"?\s*:\s*"(\w+)"`,
)

func (analysis *codexSessionAnalysis) notePlan(payload codexPayload) {
	text := payloadText(payload)
	if payload.Name != "update_plan" && !strings.Contains(text, "update_plan") {
		return
	}
	matches := planStepPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return
	}
	before := make(map[string]string, len(analysis.plan))
	for _, step := range analysis.plan {
		before[step.step] = step.status
	}
	steps := make([]planStep, 0, len(matches))
	for _, m := range matches {
		var text string
		if err := json.Unmarshal([]byte(m[1]), &text); err != nil {
			continue
		}
		status := m[2]
		steps = append(steps, planStep{step: text, status: status})
		if status == "completed" && before[text] != "completed" {
			analysis.digest.Push(analysis.prompts, session.DigestTodos, "completed — "+text)
		}
	}
	analysis.plan = steps
}

type codexQuestion struct {
	id   string
	text string
}

func questionsFrom(payload codexPayload) []codexQuestion {
	text := payloadText(payload)
	if payload.Name != "request_user_input" && !requestUserInputCallPattern.MatchString(text) {
		return nil
	}
	var arguments struct {
		Questions []struct {
			ID       string `json:"id"`
			Question string `json:"question"`
		} `json:"questions"`
	}
	if err := json.Unmarshal([]byte(text), &arguments); err == nil && len(arguments.Questions) > 0 {
		questions := make([]codexQuestion, 0, len(arguments.Questions))
		for _, item := range arguments.Questions {
			question := item.Question
			if strings.TrimSpace(question) == "" {
				question = "requested user input"
			}
			questions = append(questions, codexQuestion{id: item.ID, text: question})
		}
		return questions
	}

	ids := questionIDPattern.FindAllStringSubmatch(text, -1)
	questionMatches := questionPattern.FindAllStringSubmatch(text, -1)
	questions := make([]codexQuestion, 0, len(questionMatches))
	for i, match := range questionMatches {
		var question string
		if json.Unmarshal([]byte(match[1]), &question) != nil {
			continue
		}
		if strings.TrimSpace(question) == "" {
			question = "requested user input"
		}
		id := ""
		if i < len(ids) {
			_ = json.Unmarshal([]byte(ids[i][1]), &id)
		}
		questions = append(questions, codexQuestion{id: id, text: question})
	}
	if len(questions) == 0 {
		return []codexQuestion{{text: "requested user input"}}
	}
	return questions
}

type codexQuestionAnswer struct {
	Answers  []string `json:"answers"`
	Selected []string `json:"selected"`
	Other    string   `json:"other"`
}

func (answer codexQuestionAnswer) values() []string {
	values := make([]string, 0, len(answer.Answers)+len(answer.Selected)+1)
	values = append(values, answer.Answers...)
	values = append(values, answer.Selected...)
	if answer.Other != "" {
		values = append(values, answer.Other)
	}

	cleaned := values[:0]
	for _, value := range values {
		value = strings.TrimSpace(strings.TrimPrefix(value, "user_note:"))
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	return cleaned
}

func questionAnswersByCall(rows []codexRow) map[string]map[string]codexQuestionAnswer {
	answersByCall := make(map[string]map[string]codexQuestionAnswer)
	for _, row := range rows {
		if row.Type != "response_item" ||
			(row.Payload.Type != "function_call_output" && row.Payload.Type != "custom_tool_call_output") ||
			row.Payload.CallID == "" {
			continue
		}
		if answers, ok := questionAnswersFrom(row.Payload.Output); ok {
			answersByCall[row.Payload.CallID] = answers
		}
	}
	return answersByCall
}

func questionAnswersFrom(output json.RawMessage) (map[string]codexQuestionAnswer, bool) {
	var encoded string
	if err := json.Unmarshal(output, &encoded); err != nil {
		return nil, false
	}
	var result struct {
		Answers map[string]codexQuestionAnswer `json:"answers"`
	}
	if err := json.Unmarshal([]byte(encoded), &result); err != nil || result.Answers == nil {
		return nil, false
	}
	return result.Answers, true
}

func payloadText(payload codexPayload) string {
	if payload.Input != "" {
		return payload.Input
	}
	return string(payload.Arguments)
}

func commandFrom(payload codexPayload) (string, bool) {
	text := payloadText(payload)
	m := execCmdPattern.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	var command string
	if err := json.Unmarshal([]byte(m[1]), &command); err != nil {
		return "", false
	}
	return command, true
}
