package claude

import (
	"cmp"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// interruptMarker prefixes the synthetic user message Claude Code injects when a turn is interrupted.
const interruptMarker = "[Request interrupted by user"

type claudeSessionAnalysis struct {
	sessionID                string
	workingDirectory         string
	branch                   *string
	entrypoint               *string
	durationMs               int
	timestamps               session.TimestampRange
	customTitle              string
	customName               string
	generatedName            string
	inTurn                   bool
	awaySummary              string
	compactionSeed           string
	declaredGoal             string
	compactBoundaries        int
	compactSummaries         int
	pullRequests             map[string]struct{}
	turns                    int
	firstUserPrompt          string
	toolUseCount             int
	errors                   int
	spawns                   map[string]vendors.SpawnState
	editedFiles              map[string]struct{}
	commands                 session.CommandLog
	commits                  []string
	dedupedMessageTokenUsage map[string]messageUsage
	tokens                   map[string]session.ModelTokens
	fileEdits                *session.FileEditSet
	tasks                    map[string]*taskEntry
	taskOrder                []string
	digest                   session.DigestLog
	userPromptCount          int
	model                    string
	lastUsage                *claudeUsage
}

type messageUsage struct {
	model string
	usage *claudeUsage
}

type parsedSession struct {
	transcript *vendors.ParsedSession
	forkUsage  map[string]messageUsage
}

type taskEntry struct {
	subject string
	status  string
}

func parseTranscript(path string) (*vendors.ParsedSession, error) {
	parsed, err := parse(path)
	if err != nil {
		return nil, err
	}
	return parsed.transcript, nil
}

func parse(path string) (*parsedSession, error) {
	analysis, err := analyzeClaudeSession(path)
	if err != nil {
		return nil, err
	}
	parsed := &vendors.ParsedSession{
		Session:  analysis.unifiedSession(path),
		LogPath:  path,
		ParentID: ParentIDFromPath(path),
		Name:     analysis.transcriptName(),
		InTurn:   analysis.inTurn,
		Spawns:   analysis.spawns,
		Commands: analysis.commands.Labelled(),
	}
	if parsed.ParentID != "" {
		metaPath := strings.TrimSuffix(path, ".jsonl") + ".meta.json"
		var meta subagentMeta
		if _, err := session.ReadJSONIfValid(metaPath, &meta); err != nil {
			log.Printf("%s: unreadable subagent metadata: %v", metaPath, err)
		}
		parsed.Name = cmp.Or(meta.Description, meta.AgentType)
		parsed.SpawnKey = cmp.Or(workflowRunID(path), meta.ToolUseID)
		parsed.Stopped = meta.StoppedByUser
	}
	return &parsedSession{transcript: parsed, forkUsage: analysis.dedupedMessageTokenUsage}, nil
}

func analyzeClaudeSession(file string) (*claudeSessionAnalysis, error) {
	rows, err := session.ParseJSONL[claudeSessionRecord](file)
	if err != nil {
		return nil, err
	}
	analysis := &claudeSessionAnalysis{
		// A subagent transcript opens mid-turn: the spawn already happened, and
		// its task prompt is a meta row that emitPrompt skips. A forked skill has
		// no other prompt row, so nothing else would ever raise this.
		inTurn:                   ParentIDFromPath(file) != "",
		dedupedMessageTokenUsage: map[string]messageUsage{},
		pullRequests:             map[string]struct{}{},
		spawns:                   map[string]vendors.SpawnState{},
		editedFiles:              map[string]struct{}{},
		tokens:                   map[string]session.ModelTokens{},
		tasks:                    map[string]*taskEntry{},
		fileEdits:                session.NewFileEditSet(),
		commits:                  []string{},
	}
	pendingAssistantText := ""
	pendingCommand := ""
	emitPrompt := func(text string, timestamp int64) {
		analysis.inTurn = true
		pendingAssistantText = ""
		analysis.userPromptCount++
		if analysis.firstUserPrompt == "" {
			analysis.firstUserPrompt = text
		}
		category := session.DigestUser
		if analysis.userPromptCount == 1 {
			category = session.DigestFirstPrompt
		}
		analysis.digest.Push(analysis.userPromptCount, category, text, timestamp)
	}
	for _, row := range rows {
		if row.SessionID != "" {
			analysis.sessionID = row.SessionID
		}
		if row.WorkingDirectory != "" {
			analysis.workingDirectory = row.WorkingDirectory
		}
		if row.Branch != nil && *row.Branch != "" {
			analysis.branch = row.Branch
		}
		if row.Entrypoint != nil && *row.Entrypoint != "" {
			analysis.entrypoint = row.Entrypoint
		}
		if row.TurnDurationMs != nil {
			analysis.durationMs += *row.TurnDurationMs
			analysis.turns++
			analysis.inTurn = false
			pendingAssistantText = ""
		}
		var rowTimestamp int64
		if row.Timestamp != "" {
			ts, err := session.RFC3339ToUnixEpoch(row.Timestamp)
			if err != nil {
				log.Printf("%s: skipping unparseable timestamp %q: %v", file, row.Timestamp, err)
			} else {
				rowTimestamp = ts
				analysis.timestamps.Note(ts)
			}
		}
		if row.Type == "custom-title" && row.CustomTitle != "" {
			analysis.customTitle = row.CustomTitle
		}
		if row.Type == "agent-name" && row.CustomSessionName != "" {
			analysis.customName = row.CustomSessionName
		}
		if row.Type == "ai-title" && row.GeneratedSessionName != "" {
			analysis.generatedName = row.GeneratedSessionName
		}
		if row.Type == "pr-link" && row.PRURL != "" {
			analysis.pullRequests[row.PRURL] = struct{}{}
		}
		if row.Subtype == "away_summary" {
			analysis.awaySummary = row.Content
		}
		if row.Subtype == "compact_boundary" {
			analysis.compactBoundaries++
			analysis.digest.Push(analysis.userPromptCount,
				session.DigestCompaction,
				fmt.Sprintf("context compacted (%d)", analysis.compactBoundaries),
				rowTimestamp,
			)
		}
		if row.IsCompactSummary && row.Message != nil {
			analysis.compactionSeed = stripCompactionSummary(row.Message.textContent())
		}
		if row.IsCompactSummary {
			analysis.compactSummaries++
		}
		if row.Type == "user" {
			if row.Message != nil && !row.IsCompactSummary {
				if goal, ok := parseDeclaredGoal(row.Message.textContent()); ok {
					analysis.declaredGoal = goal
				}
			}
			switch {
			case row.commandInvocation() != "":
				pendingCommand = row.commandInvocation()
			case row.IsMeta:
				if pendingCommand != "" {
					emitPrompt(pendingCommand, rowTimestamp)
					pendingCommand = ""
				}
			default:
				pendingCommand = ""
				text := row.promptText()
				switch {
				case strings.HasPrefix(text, interruptMarker):
					analysis.inTurn = false
					pendingAssistantText = ""
				case text != "":
					emitPrompt(text, rowTimestamp)
				}
			}
		}
		resultToolUseID := ""
		if row.Message != nil {
			if row.Type == "assistant" {
				if text := strings.TrimSpace(row.Message.textContent()); text != "" {
					pendingAssistantText = text
				}
			}
			blocks, err := row.Message.contentBlocks()
			if err != nil {
				log.Printf("%s: skipping malformed message content: %v", file, err)
				blocks = nil
			}
			for _, block := range blocks {
				if block.Type == "tool_use" {
					analysis.toolUseCount++
					if block.ID != "" {
						turn := max(analysis.userPromptCount, 1)
						analysis.spawns[block.ID] = vendors.SpawnState{
							Turn: &turn,
						}
						if isSubagentTool(block.Name) {
							analysis.digest.PushSubagent(analysis.userPromptCount, block.ID, rowTimestamp)
						}
					}
				}
				if block.Type == "tool_result" && block.ToolUseID != "" {
					spawn := analysis.spawns[block.ToolUseID]
					spawn.Completed = true
					analysis.spawns[block.ToolUseID] = spawn
					resultToolUseID = block.ToolUseID
				}
				if block.IsError {
					analysis.errors++
				}
				if block.Name == "ExitPlanMode" && analysis.userPromptCount > 0 &&
					pendingAssistantText != "" {
					analysis.digest.Push(
						analysis.userPromptCount,
						session.DigestRecap,
						pendingAssistantText,
						rowTimestamp,
					)
					pendingAssistantText = ""
				}
				if block.Name == "Edit" || block.Name == "Write" {
					analysis.editedFiles[block.Input.FilePath] = struct{}{}
				}
				if block.Name == "TaskUpdate" {
					if task, ok := analysis.tasks[block.Input.TaskID]; ok {
						if block.Input.Status == "deleted" {
							delete(analysis.tasks, block.Input.TaskID)
						} else if block.Input.Status != "" {
							task.status = block.Input.Status
							if block.Input.Status == "completed" {
								analysis.digest.Push(analysis.userPromptCount, session.DigestTodos, "completed — "+task.subject, rowTimestamp)
							}
						}
					}
				}
				if block.Input.Command != "" {
					analysis.commands.Note(block.Input.Command, block.Input.Description)
					if message, ok := session.CommitMessage(block.Input.Command); ok {
						analysis.commits = append(analysis.commits, message)
					}
				}
			}
			if row.Message.ID != "" && row.Message.Usage != nil &&
				row.Message.Model != "<synthetic>" {
				analysis.dedupedMessageTokenUsage[row.Message.ID] = messageUsage{
					model: row.Message.Model,
					usage: row.Message.Usage,
				}
				analysis.lastUsage = row.Message.Usage
			}
			if row.Message.Model != "" && row.Message.Model != "<synthetic>" {
				analysis.model = row.Message.Model
			}
			if row.Type == "assistant" && row.Message.StopReason != "" {
				if row.Message.StopReason != "tool_use" && row.Message.StopReason != "pause_turn" {
					analysis.inTurn = false
					reply := strings.TrimSpace(row.Message.textContent())
					if analysis.userPromptCount > 0 && reply != "" {
						analysis.digest.Push(analysis.userPromptCount, session.DigestRecap, reply, rowTimestamp)
					}
					pendingAssistantText = ""
				}
			}
		}
		if row.ToolUseResult != nil {
			result, err := row.toolResult()
			if err != nil {
				log.Printf("%s: skipping malformed toolUseResult: %v", file, err)
			}
			if result != nil && result.FilePath != "" {
				var diffLines []string
				createdContent := ""
				for _, patch := range result.StructuredPatch {
					diffLines = append(diffLines, fmt.Sprintf(
						"@@ -%d,%d +%d,%d @@",
						patch.OldStart, patch.OldLines, patch.NewStart, patch.NewLines,
					))
					diffLines = append(diffLines, patch.Lines...)
				}
				lineAdditions, lineDeletions := session.DiffStat(diffLines)
				if len(result.StructuredPatch) == 0 && result.Type == "create" {
					if err := json.Unmarshal(result.Content, &createdContent); err != nil {
						log.Printf(
							"%s: skipping unreadable new file for %s: %v",
							file,
							result.FilePath,
							err,
						)
					} else {
						lineAdditions = session.CountLines(createdContent)
					}
				}
				analysis.fileEdits.Add(
					result.FilePath, lineAdditions, lineDeletions, result.Type == "create",
				)
				if len(diffLines) > 0 {
					analysis.fileEdits.Patch(result.FilePath, strings.Join(diffLines, "\n"))
				} else if result.Type == "create" {
					analysis.fileEdits.Change(result.FilePath, "", createdContent)
				}
			}
			if result != nil && result.Task != nil && result.Task.ID != "" {
				if _, ok := analysis.tasks[result.Task.ID]; !ok {
					analysis.taskOrder = append(analysis.taskOrder, result.Task.ID)
				}
				analysis.tasks[result.Task.ID] = &taskEntry{
					subject: result.Task.Subject,
				}
			}
			if result != nil && result.RunID != "" {
				if spawn, ok := analysis.spawns[resultToolUseID]; ok {
					analysis.spawns[result.RunID] = vendors.SpawnState{Turn: spawn.Turn}
				}
				analysis.digest.PushSubagent(analysis.userPromptCount, result.RunID, rowTimestamp)
			}
			if result != nil && result.Answers != nil {
				for _, question := range result.Questions {
					analysis.digest.PushQuestion(
						analysis.userPromptCount,
						question.Question,
						result.Answers[question.Question].String(),
						rowTimestamp,
					)
				}
			}
		}
	}
	for _, message := range analysis.dedupedMessageTokenUsage {
		usage := message.usage
		currentTotalUsage := analysis.tokens[message.model]
		currentTotalUsage.InputTokens += usage.InputTokens
		currentTotalUsage.OutputTokens += usage.OutputTokens
		currentTotalUsage.CacheReadInputTokens += usage.CacheReadInputTokens
		currentTotalUsage.CacheCreation1hInputTokens += usage.CacheWriteInputTokens.Ephemeral1h
		currentTotalUsage.CacheCreationInputTokens += usage.CacheWriteInputTokens.Ephemeral5m +
			usage.untieredCacheCreation()
		analysis.tokens[message.model] = currentTotalUsage
	}
	return analysis, nil
}

func isSubagentTool(name string) bool {
	return name == "Agent" || name == "Task"
}

func (analysis *claudeSessionAnalysis) unifiedSession(filePath string) *session.Session {
	sessionID := IDFromPath(filePath)
	if sessionID == "" {
		sessionID = analysis.sessionID
	}
	turns := analysis.turns
	if turns == 0 {
		turns = analysis.userPromptCount
	}
	unifiedSessionDetails := session.SessionDetails{
		Compactions:    max(analysis.compactBoundaries, analysis.compactSummaries),
		PullRequests:   len(analysis.pullRequests),
		Turns:          turns,
		ToolUses:       analysis.toolUseCount,
		Errors:         analysis.errors,
		Commands:       analysis.commands.Raw(),
		Commits:        analysis.commits,
		FileEdits:      analysis.fileEdits.Edits,
		Digest:         analysis.digest.Entries(),
		ContextTokens:  contextTokens(analysis.lastUsage),
		CompactionSeed: analysis.compactionSeed,
	}
	if analysis.firstUserPrompt != "" {
		unifiedSessionDetails.FirstPrompt = &analysis.firstUserPrompt
	}
	if analysis.declaredGoal != "" {
		unifiedSessionDetails.DeclaredGoal = &analysis.declaredGoal
	}
	summary := analysis.awaySummary
	if recap := analysis.digest.LastRecap(); recap != "" {
		summary = recap
	}
	summary = session.Truncate(summary, session.TruncateTextLimit)
	unifiedSession := session.Session{
		Agent:            vendors.AgentClaude,
		ID:               sessionID,
		WorkingDirectory: analysis.workingDirectory,
		Branch:           analysis.branch,
		Entrypoint:       analysis.entrypoint,
		SessionDetails:   unifiedSessionDetails,
		LastActivityTime: analysis.timestamps.Latest,
		EditedFileCount:  len(analysis.editedFiles),
		Tokens:           analysis.tokens,
		Subagents:        []session.Subagent{},
	}
	if summary != "" {
		unifiedSession.Summary = &summary
	}
	unifiedSession.DurationMs = analysis.elapsedMs()
	if analysis.model != "" {
		unifiedSession.Model = &analysis.model
	}
	todos := []session.Todo{}
	for _, id := range analysis.taskOrder {
		if task, ok := analysis.tasks[id]; ok {
			todos = append(
				todos,
				session.Todo{Text: task.subject, Done: task.status == "completed"},
			)
		}
	}
	unifiedSession.Todos = todos
	return &unifiedSession
}

func contextTokens(usage *claudeUsage) *int {
	if usage == nil {
		return nil
	}
	total := session.ContextTokens(
		usage.InputTokens,
		usage.CacheReadInputTokens,
		usage.CacheWriteInputTokens.Ephemeral1h+usage.CacheWriteInputTokens.Ephemeral5m,
	)
	return &total
}

func (analysis *claudeSessionAnalysis) transcriptName() string {
	if analysis.customTitle != "" {
		return analysis.customTitle
	}
	if analysis.customName != "" {
		return analysis.customName
	}
	return analysis.generatedName
}

func (analysis *claudeSessionAnalysis) elapsedMs() *int {
	if analysis.durationMs > 0 {
		return &analysis.durationMs
	}
	if analysis.timestamps.Latest > 0 {
		span := analysis.timestamps.SpanMs()
		return &span
	}
	return nil
}
