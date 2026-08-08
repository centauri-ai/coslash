package sessiondetail

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func (p *Projector) projectClaude(ctx context.Context, path string) (*heavyDetail, error) {
	rows, err := p.readRows(ctx, path)
	if err != nil {
		return nil, err
	}
	h := newHeavyDetail()
	prompts := 0
	pendingReads := map[string]pendingRead{}
	todoWrite := []Todo{}
	tasks := map[string]Todo{}
	taskOrder := []string{}

	allTodos := func() []Todo {
		if len(tasks) == 0 {
			return append([]Todo{}, todoWrite...)
		}
		result := make([]Todo, 0, len(taskOrder))
		for _, id := range taskOrder {
			if todo, ok := tasks[id]; ok {
				result = append(result, todo)
			}
		}
		return result
	}

	for _, raw := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, decodeErr := decodeObject(raw)
		if decodeErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformedTranscript, decodeErr)
		}
		typeName := stringField(row, "type")
		message := mapField(row, "message")
		if typeName == "user" {
			if !boolField(row, "isMeta") && !boolField(row, "isCompactSummary") {
				if text := claudeMessageText(message); text != "" && !strings.HasPrefix(text, "<") {
					prompts++
					h.turn(prompts).Prompt = text
				}
			}
			for _, part := range arrayField(message, "content") {
				if stringField(part, "type") != "tool_result" {
					continue
				}
				partObject, ok := part.(map[string]any)
				if !ok {
					continue
				}
				turn := h.turn(prompts)
				if boolField(part, "is_error") {
					turn.Errors++
				}
				callID := stringField(part, "tool_use_id")
				if pending, ok := pendingReads[callID]; ok {
					delete(pendingReads, callID)
					noteReadOutput(h, callID, pending, claudeToolResultText(row, partObject))
				}
			}
			result := mapField(row, "toolUseResult")
			if result != nil {
				noteClaudeFileEdit(h, result)
				noteClaudeContextFile(h, result)
				if task := mapField(result, "task"); task != nil {
					id, subject := stringField(task, "id"), stringField(task, "subject")
					if id != "" && subject != "" {
						if _, ok := tasks[id]; !ok {
							taskOrder = append(taskOrder, id)
						}
						tasks[id] = Todo{Text: subject}
						h.turn(prompts).Todos = allTodos()
					}
				}
				questions := arrayField(result, "questions")
				answers := mapField(result, "answers")
				for _, questionValue := range questions {
					question := stringField(questionValue, "question")
					if question == "" {
						continue
					}
					var answer *string
					if answers != nil {
						if value, ok := answers[question]; ok {
							text := answerText(value)
							if text != "" {
								answer = &text
							}
						}
					}
					h.turn(prompts).Decisions = append(h.turn(prompts).Decisions, Decision{Question: question, Answer: answer})
				}
			}
		}

		if typeName != "assistant" || message == nil {
			continue
		}
		for _, part := range arrayField(message, "content") {
			if stringField(part, "type") != "tool_use" {
				continue
			}
			turn := h.turn(prompts)
			turn.ToolUses++
			name := stringField(part, "name")
			input := mapField(part, "input")
			kind, displayName := triggeredName(name, input)
			h.addTriggered(kind, displayName)
			switch name {
			case "ExitPlanMode":
				if plan := stringField(input, "plan"); plan != "" {
					turn.PlanText = &plan
				}
			case "Bash":
				command := stringField(input, "command")
				if reads := parseShellReads(command); len(reads) > 0 {
					pendingReads[stringField(part, "id")] = pendingRead{Reads: reads, Command: command}
				}
			case "TodoWrite":
				todoWrite = todoWrite[:0]
				for _, item := range arrayField(input, "todos") {
					text := stringField(item, "content")
					if text == "" {
						continue
					}
					todoWrite = append(todoWrite, Todo{Text: text, Done: stringField(item, "status") == "completed"})
				}
				turn.Todos = allTodos()
			case "TaskUpdate":
				id := stringField(input, "taskId")
				if id == "" {
					if numeric, ok := intField(input, "taskId"); ok {
						id = fmt.Sprint(numeric)
					}
				}
				if todo, ok := tasks[id]; ok {
					if subject := stringField(input, "subject"); subject != "" {
						todo.Text = subject
					}
					status := stringField(input, "status")
					if status == "deleted" {
						delete(tasks, id)
					} else {
						todo.Done = status == "completed" || todo.Done
						tasks[id] = todo
					}
					turn.Todos = allTodos()
				}
			case "Edit", "Write":
				file := stringField(input, "file_path")
				turn.FileEdits = appendUnique(turn.FileEdits, file)
			}
		}
	}
	return h, nil
}

func claudeMessageText(message map[string]any) string {
	if message == nil {
		return ""
	}
	switch content := message["content"].(type) {
	case string:
		return strings.TrimSpace(content)
	case []any:
		for _, part := range content {
			if stringField(part, "type") == "text" {
				return strings.TrimSpace(stringField(part, "text"))
			}
		}
	}
	return ""
}

func triggeredName(tool string, input map[string]any) (string, string) {
	if strings.HasPrefix(tool, "mcp__") {
		return "mcp", strings.ReplaceAll(strings.TrimPrefix(tool, "mcp__"), "__", " / ")
	}
	if tool == "Skill" {
		name := stringField(input, "skill")
		if name == "" {
			name = stringField(input, "name")
		}
		if name == "" {
			name = tool
		}
		return "skill", name
	}
	return "tool", tool
}

func claudeToolResultText(row, part map[string]any) string {
	if result := mapField(row, "toolUseResult"); result != nil {
		stdout := stringField(result, "stdout")
		stderr := stringField(result, "stderr")
		if stdout != "" || stderr != "" {
			return strings.Trim(strings.Join([]string{stdout, stderr}, "\n"), "\n")
		}
	}
	switch content := part["content"].(type) {
	case string:
		return content
	case []any:
		var builder strings.Builder
		for _, item := range content {
			builder.WriteString(stringField(item, "text"))
		}
		return builder.String()
	}
	return ""
}

func noteClaudeFileEdit(h *heavyDetail, result map[string]any) {
	path := stringField(result, "filePath")
	if path == "" {
		return
	}
	lines := []string{}
	for _, patch := range arrayField(result, "structuredPatch") {
		for _, value := range arrayField(patch, "lines") {
			if line, ok := value.(string); ok {
				lines = append(lines, line)
			}
		}
	}
	isNew := stringField(result, "type") == "create"
	adds, dels := diffLines(lines)
	if len(lines) == 0 && isNew {
		adds = countLines(stringField(result, "content"))
	}
	h.addFileEdit(path, adds, dels, isNew, lines)
}

func noteClaudeContextFile(h *heavyDetail, result map[string]any) {
	file := mapField(result, "file")
	if file == nil {
		return
	}
	path, content := stringField(file, "filePath"), stringField(file, "content")
	if path == "" {
		return
	}
	start, ok := intField(file, "startLine")
	if !ok || start < 1 {
		start = 1
	}
	total, totalOK := intField(file, "totalLines")
	lines, linesOK := intField(file, "numLines")
	if !linesOK {
		lines = countLines(content)
	}
	var totalPointer *int
	if totalOK {
		totalPointer = &total
	}
	partial := start > 1 || (totalOK && lines < total)
	h.addContext(path, ContextSegment{StartLine: start, Content: content}, partial, totalPointer, true, nil)
}

func answerText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, ", ")
	default:
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
}
