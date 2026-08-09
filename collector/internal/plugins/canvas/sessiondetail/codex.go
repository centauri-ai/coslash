package sessiondetail

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var (
	jsonStringFieldPattern = regexp.MustCompile(`(?s)["']?([A-Za-z_][A-Za-z0-9_]*)["']?\s*:\s*("(?:[^"\\]|\\.)*")`)
	exitCodePattern        = regexp.MustCompile(`(?:"exit_code"|exit_code)\s*:\s*(-?\d+)`)
)

func (p *Projector) projectCodex(ctx context.Context, path string) (*heavyDetail, error) {
	rows, err := p.readRows(ctx, path)
	if err != nil {
		return nil, err
	}
	h := newHeavyDetail()
	prompts := 0
	pendingReads := map[string]pendingRead{}
	pendingDeferred := map[string]struct{ kind, name string }{}

	for _, raw := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, decodeErr := decodeObject(raw)
		if decodeErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformedTranscript, decodeErr)
		}
		payload := mapField(row, "payload")
		switch stringField(row, "type") {
		case "response_item":
			itemType := stringField(payload, "type")
			switch itemType {
			case "custom_tool_call", "function_call":
				turn := h.turn(prompts)
				turn.ToolUses++
				name := stringField(payload, "name")
				text := codexPayloadText(payload)
				kind, displayName := codexTriggeredName(name, text)
				h.addTriggered(kind, displayName)
				callID := stringField(payload, "call_id")
				if command := codexCommand(name, text); command != "" {
					if reads := parseShellReads(command); len(reads) > 0 && callID != "" {
						pendingReads[callID] = pendingRead{Reads: reads, Command: command}
					}
				}
				if deferredKind, deferredName := codexDeferred(name, text); deferredKind != "" && callID != "" {
					pendingDeferred[callID] = struct{ kind, name string }{deferredKind, deferredName}
				}
				noteCodexPlan(turn, name, text)
				noteCodexQuestion(turn, name, text)
			case "custom_tool_call_output", "function_call_output":
				callID := stringField(payload, "call_id")
				output := codexOutputText(payload["output"])
				if matches := exitCodePattern.FindStringSubmatch(output); len(matches) == 2 && matches[1] != "0" {
					h.turn(prompts).Errors++
				}
				if pending, ok := pendingReads[callID]; ok {
					delete(pendingReads, callID)
					noteReadOutput(h, callID, pending, stripExecPreamble(output))
				}
				if deferred, ok := pendingDeferred[callID]; ok {
					delete(pendingDeferred, callID)
					if output != "" {
						h.deferred = append(h.deferred, DeferredContext{ID: callID, Kind: deferred.kind, Name: deferred.name, Content: output})
					}
				}
			}
		case "event_msg":
			switch stringField(payload, "type") {
			case "user_message":
				message := stringField(payload, "message")
				if message != "" && !strings.HasPrefix(message, "<") {
					prompts++
					h.turn(prompts).Prompt = codexPromptText(message)
				}
			case "patch_apply_end":
				noteCodexPatch(h, h.turn(prompts), mapField(payload, "changes"))
			}
		}
	}
	return h, nil
}

func codexPayloadText(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if text := stringField(payload, "input"); text != "" {
		return text
	}
	return stringField(payload, "arguments")
}

func codexTriggeredName(name, text string) (string, string) {
	if strings.HasPrefix(name, "mcp__") {
		return "mcp", strings.ReplaceAll(strings.TrimPrefix(name, "mcp__"), "__", " / ")
	}
	if kind, skill := codexDeferred(name, text); kind != "" {
		return kind, skill
	}
	return "tool", name
}

func codexDeferred(name, text string) (string, string) {
	if strings.HasPrefix(name, "mcp__") {
		return "mcp", strings.ReplaceAll(strings.TrimPrefix(name, "mcp__"), "__", " / ")
	}
	if name != "skills.read" && name != "Skill" {
		return "", ""
	}
	if marker := strings.Index(text, "skill://"); marker >= 0 {
		value := text[marker+len("skill://"):]
		if end := strings.IndexAny(value, `"'\s`); end >= 0 {
			value = value[:end]
		}
		if value != "" {
			return "skill", value
		}
	}
	return "skill", name
}

func codexCommand(name, text string) string {
	if name != "exec_command" && !strings.Contains(text, "exec_command") {
		return ""
	}
	if object := decodeTextObject(text); object != nil {
		if command := stringField(object, "cmd"); command != "" {
			return command
		}
	}
	return extractQuotedField(text, "cmd")
}

func noteCodexPlan(turn *Turn, name, text string) {
	if name != "update_plan" && !strings.Contains(text, "update_plan") {
		return
	}
	object := decodeTextObject(text)
	if object == nil {
		return
	}
	if explanation := stringField(object, "explanation"); explanation != "" {
		turn.PlanText = &explanation
	}
	items := arrayField(object, "plan")
	if len(items) == 0 {
		return
	}
	turn.Todos = turn.Todos[:0]
	for _, item := range items {
		step := stringField(item, "step")
		if step != "" {
			turn.Todos = append(turn.Todos, Todo{Text: step, Done: stringField(item, "status") == "completed"})
		}
	}
}

func noteCodexQuestion(turn *Turn, name, text string) {
	if name != "request_user_input" && !strings.Contains(text, "request_user_input") {
		return
	}
	object := decodeTextObject(text)
	if object == nil {
		return
	}
	if questions := arrayField(object, "questions"); len(questions) > 0 {
		for _, item := range questions {
			if question := stringField(item, "question"); question != "" {
				turn.Decisions = append(turn.Decisions, Decision{Question: question})
			}
		}
		return
	}
	if question := stringField(object, "question"); question != "" {
		turn.Decisions = append(turn.Decisions, Decision{Question: question})
	}
}

func decodeTextObject(text string) map[string]any {
	var object map[string]any
	if json.Unmarshal([]byte(text), &object) == nil {
		return object
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start && json.Unmarshal([]byte(text[start:end+1]), &object) == nil {
		return object
	}
	return nil
}

func extractQuotedField(text, field string) string {
	for _, match := range jsonStringFieldPattern.FindAllStringSubmatch(text, -1) {
		if match[1] != field {
			continue
		}
		var value string
		if json.Unmarshal([]byte(match[2]), &value) == nil {
			return value
		}
	}
	return ""
}

func codexOutputText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		var builder strings.Builder
		for _, item := range typed {
			builder.WriteString(stringField(item, "text"))
		}
		return builder.String()
	default:
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
}

func stripExecPreamble(text string) string {
	if marker := strings.Index(text, "Output:\n"); marker >= 0 {
		return text[marker+len("Output:\n"):]
	}
	return text
}

const codexRequestMarker = "## My request for Codex:"

func codexPromptText(message string) string {
	if marker := strings.Index(message, codexRequestMarker); marker >= 0 {
		message = message[marker+len(codexRequestMarker):]
	}
	return strings.TrimSpace(message)
}

func noteCodexPatch(h *heavyDetail, turn *Turn, changes map[string]any) {
	for file, value := range changes {
		change, ok := value.(map[string]any)
		if !ok {
			continue
		}
		isNew := stringField(change, "type") == "add"
		diff := stringField(change, "unified_diff")
		lines := []string{}
		if diff != "" {
			lines = strings.Split(diff, "\n")
		}
		adds, dels := diffLines(lines)
		if diff == "" && isNew {
			adds = countLines(stringField(change, "content"))
		}
		h.addFileEdit(file, adds, dels, isNew, lines)
		turn.FileEdits = appendUnique(turn.FileEdits, file)
	}
}
