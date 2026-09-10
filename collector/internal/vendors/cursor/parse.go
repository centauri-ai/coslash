package cursor

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

var cursorTimestampPattern = regexp.MustCompile(`^(.*) \(UTC([+-])(\d{1,2})(:([0-9]{2}))?\)$`)

func parseTranscript(path string) (*vendors.ParsedSession, error) {
	return parseTranscriptSource(vendors.LocalReadSource, path)
}

func parseTranscriptSource(source vendors.ReadSource, path string) (*vendors.ParsedSession, error) {
	if !IsTranscript(path) {
		return nil, fmt.Errorf("%w: Cursor transcript path %q", vendors.ErrInvalidData, path)
	}
	records, err := vendors.ParseJSONLSource[transcriptRecord](source, path)
	if err != nil {
		return nil, err
	}

	modified := vendors.SourceModificationTime(source, path)
	digest := session.DigestLog{}
	commands := session.CommandLog{}
	commitLog := []session.CommitObservation{}
	edits := session.NewFileEditSet()
	todos := []session.Todo{}
	todoStatus := map[string]string{}
	pullRequests := map[string]struct{}{}
	turns, toolUses, errorsCount := 0, 0, 0
	firstPrompt, cwd := "", ""
	startedAt := int64(0)
	var statusHint *string
	spawns := map[string]vendors.SpawnState{}
	assistantResult := ""
	inTurn := true
	stopped := false
	taskCount := 0
	pendingQuestion, pendingQuestionTime := "", int64(0)
	pendingQuestionTurn := 0

	for _, record := range records {
		if (record.Role == "user" || record.Role == "assistant") && record.Message != nil && len(record.Message.Content) > 0 {
			inTurn, stopped, statusHint = true, false, nil
		}
		if record.Role == "user" && record.Message != nil {
			text := firstText(record.Message.Content)
			if text == "" {
				continue
			}
			if pendingQuestion != "" {
				digest.PushQuestion(pendingQuestionTurn, pendingQuestion, "", pendingQuestionTime)
				pendingQuestion = ""
			}
			turns++
			prompt, timestamp := unwrapUserText(text)
			if startedAt == 0 && timestamp > 0 {
				startedAt = timestamp
			}
			category := session.DigestUser
			if firstPrompt == "" {
				firstPrompt = prompt
				category = session.DigestFirstPrompt
			}
			if category == session.DigestUser && strings.HasSuffix(strings.TrimSpace(prompt), "?") {
				pendingQuestion, pendingQuestionTurn, pendingQuestionTime = prompt, turns, timestamp
			} else {
				digest.Push(turns, category, prompt, timestamp)
			}
		}
		if record.Role == "assistant" && record.Message != nil {
			for _, block := range record.Message.Content {
				if block.Type == "text" {
					if text := strings.TrimSpace(block.Text); text != "" {
						assistantResult = text
						if pendingQuestion != "" {
							digest.PushQuestion(pendingQuestionTurn, pendingQuestion, text, pendingQuestionTime)
							pendingQuestion = ""
						}
					}
					for _, url := range session.PullRequestURLs(block.Text) {
						pullRequests[url] = struct{}{}
					}
				}
				if block.Type != "tool_use" {
					continue
				}
				toolUses++
				input, rawString := decodeToolInput(block.Input)
				switch block.Name {
				case "Shell", "run_terminal_cmd":
					if input.Command != "" {
						commands.Note(input.Command, input.Description)
						commitLog = append(commitLog, session.ParseCommitAttempts(input.Command)...)
					}
					if input.WorkingDirectory != "" {
						cwd = input.WorkingDirectory
					}
				case "Write":
					if input.Path != "" {
						edits.Add(input.Path, session.CountLines(input.Contents), 0, false)
						edits.Write(input.Path, input.Contents)
					}
				case "StrReplace":
					if input.Path != "" {
						edits.Add(input.Path, session.CountLines(input.NewString), session.CountLines(input.OldString), false)
						edits.Change(input.Path, input.OldString, input.NewString)
					}
				case "Delete":
					if input.Path != "" {
						edits.Add(input.Path, 0, 0, false)
					}
				case "ApplyPatch":
					patch := input.Patch
					if patch == "" {
						patch = rawString
					}
					files := patchFilesFromPatch(patch)
					if len(files) == 0 && input.Path != "" {
						files = []patchFile{{path: input.Path, patch: patch}}
					}
					for _, file := range files {
						adds, dels := patchLineCounts(file.patch)
						edits.Add(file.path, adds, dels, file.isNew)
						edits.Patch(file.path, file.patch)
					}
				case "TodoWrite":
					for _, item := range input.Todos {
						text := strings.TrimSpace(item.Content)
						if text == "" {
							continue
						}
						if item.Status == "completed" && todoStatus[text] != "completed" {
							digest.Push(max(turns, 1), session.DigestTodos, "completed — "+text, 0)
						}
						todoStatus[text] = item.Status
					}
					todos = todosFromTool(input.Todos)
				case "Task":
					taskCount++
					spawnKey := fmt.Sprintf("cursor-task:%d", taskCount)
					turn := max(turns, 1)
					task := input.Description
					if task == "" {
						task = input.Prompt
					}
					spawns[spawnKey] = vendors.SpawnState{Turn: &turn, Task: task}
					digest.PushSubagentTask(turn, spawnKey, task, 0)
				}
			}
		}
		if record.Type == "turn_ended" {
			inTurn = false
			stopped = record.Status == "error"
			switch record.Status {
			case "error":
				errorsCount++
				status := "error"
				statusHint = &status
			case "success":
				statusHint = nil
			}
		}
	}
	if pendingQuestion != "" {
		digest.PushQuestion(pendingQuestionTurn, pendingQuestion, "", pendingQuestionTime)
	}

	if cwd == "" {
		cwd = commonEditDirectory(edits.Edits)
	}
	if cwd == "" {
		cwd = workspaceFromPath(path)
	}
	details := session.SessionDetails{
		Turns: turns, ToolUses: toolUses, Errors: errorsCount,
		Commands: commands.Raw(), PullRequests: len(pullRequests), Todos: todos, Digest: digest.Entries(), FileEdits: edits.Edits,
	}
	if firstPrompt != "" {
		details.FirstPrompt = &firstPrompt
	}
	result := &session.Session{
		Agent: vendors.AgentCursor, ID: IDFromPath(path), WorkingDirectory: cwd,
		EditedFileCount: len(edits.Edits), StartedAt: startedAt, LastActivityTime: modified,
		Tokens: map[string]session.ModelTokens{}, UnpricedModels: []string{}, Subagents: []session.Subagent{},
		CommitLog: commitLog, SessionDetails: details,
	}
	if startedAt > 0 && modified >= startedAt {
		duration := int(modified - startedAt)
		result.DurationMs = &duration
	}
	return &vendors.ParsedSession{
		Session: result, LogPath: path, LogModifiedAtMs: modified,
		ParentID: ParentIDFromPath(path),
		InTurn:   inTurn, Stopped: stopped, Result: assistantResult,
		Spawns: spawns, Commands: commands.Labelled(), StatusHint: statusHint,
	}, nil
}

func firstText(blocks []contentBlock) string {
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			return strings.TrimSpace(block.Text)
		}
	}
	return ""
}

func unwrapUserText(text string) (string, int64) {
	timestampText := between(text, "<timestamp>", "</timestamp>")
	timestamp := int64(0)
	if parsed, ok := parseTimestamp(timestampText); ok {
		timestamp = parsed.UnixMilli()
	}
	if query := between(text, "<user_query>", "</user_query>"); query != "" {
		return strings.TrimSpace(query), timestamp
	}
	return strings.TrimSpace(text), timestamp
}

func parseTimestamp(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, true
	}
	match := cursorTimestampPattern.FindStringSubmatch(value)
	if match == nil {
		return time.Time{}, false
	}
	hours, hourErr := strconv.Atoi(match[3])
	minutes := 0
	var minuteErr error
	if match[5] != "" {
		minutes, minuteErr = strconv.Atoi(match[5])
	}
	if hourErr != nil || minuteErr != nil || hours > 23 || minutes > 59 {
		return time.Time{}, false
	}
	offset := (hours*60 + minutes) * 60
	if match[2] == "-" {
		offset = -offset
	}
	zone := time.FixedZone("UTC"+match[2]+match[3], offset)
	parsed, err := time.ParseInLocation("Monday, Jan 2, 2006, 3:04 PM", match[1], zone)
	return parsed, err == nil
}

func between(text, start, end string) string {
	_, after, ok := strings.Cut(text, start)
	if !ok {
		return ""
	}
	value, _, ok := strings.Cut(after, end)
	if !ok {
		return ""
	}
	return value
}

func decodeToolInput(raw json.RawMessage) (toolInput, string) {
	var input toolInput
	if len(raw) == 0 {
		return input, ""
	}
	if err := json.Unmarshal(raw, &input); err == nil {
		return input, ""
	}
	var text string
	_ = json.Unmarshal(raw, &text)
	return input, text
}

func todosFromTool(items []todoToolItem) []session.Todo {
	result := make([]session.Todo, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Content) == "" {
			continue
		}
		result = append(result, session.Todo{Text: item.Content, Done: item.Status == "completed"})
	}
	return result
}

type patchFile struct {
	path  string
	patch string
	isNew bool
}

func patchFilesFromPatch(patch string) []patchFile {
	files := []patchFile{}
	var current *patchFile
	var body strings.Builder
	flush := func() {
		if current != nil {
			current.patch = body.String()
			if current.path != "" {
				files = append(files, *current)
			}
			current = nil
			body.Reset()
		}
	}
	for rawLine := range strings.Lines(patch) {
		line := strings.TrimSuffix(rawLine, "\n")
		path, update := strings.CutPrefix(line, "*** Update File: ")
		if !update {
			path, update = strings.CutPrefix(line, "*** Delete File: ")
		}
		addPath, add := strings.CutPrefix(line, "*** Add File: ")
		if update || add {
			flush()
			if add {
				path = addPath
			}
			current = &patchFile{path: strings.TrimSpace(path), isNew: add}
			continue
		}
		if strings.HasPrefix(line, "*** ") {
			flush()
			continue
		}
		if current != nil {
			body.WriteString(rawLine)
		}
	}
	flush()
	return files
}

func patchLineCounts(patch string) (int, int) {
	adds, dels := 0, 0
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			adds++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			dels++
		}
	}
	return adds, dels
}

func commonEditDirectory(edits []session.FileEdit) string {
	common := ""
	for _, edit := range edits {
		if !filepath.IsAbs(edit.Path) {
			continue
		}
		directory := filepath.Dir(filepath.Clean(edit.Path))
		if common == "" {
			common = directory
			continue
		}
		for !containsPath(common, directory) {
			parent := filepath.Dir(common)
			if parent == common {
				return ""
			}
			common = parent
		}
	}
	return common
}

func containsPath(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func workspaceFromPath(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for index, part := range parts {
		if part != "agent-transcripts" || index == 0 {
			continue
		}
		slug := parts[index-1]
		if strings.HasPrefix(slug, "Users-") {
			return "/" + strings.ReplaceAll(slug, "-", "/")
		}
	}
	return ""
}
