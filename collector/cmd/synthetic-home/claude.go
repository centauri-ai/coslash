package main

import (
	"maps"
	"path"
	"path/filepath"
	"time"
)

const claudeModel = "claude-sonnet-4-5-20250929"

type claudeTranscript struct {
	r                  source
	sessionID, cwd     string
	agentID, parentRow string
	lines              *jsonl
}

func (t *claudeTranscript) row(at time.Time, kind string, fields object) {
	row := object{"type": kind, "timestamp": stamp(at), "sessionId": t.sessionID, "cwd": t.cwd, "uuid": t.r.uuid(),
		"parentUuid": nil, "isSidechain": t.agentID != "", "userType": "external", "version": "2.1.3", "gitBranch": "main"}
	if t.parentRow != "" {
		row["parentUuid"] = t.parentRow
	}
	if t.agentID != "" {
		row["agentId"] = t.agentID
	}
	maps.Copy(row, fields)
	t.parentRow = row["uuid"].(string)
	t.lines.add(row)
}

func (t *claudeTranscript) user(at time.Time, content any, extra object) {
	fields := object{"message": object{"role": "user", "content": content}, "entrypoint": "cli"}
	maps.Copy(fields, extra)
	t.row(at, "user", fields)
}

func (t *claudeTranscript) assistant(at time.Time, content any, stop string, tokens int) {
	message := object{"model": claudeModel, "id": "msg_" + t.r.token(24, alphabet), "type": "message", "role": "assistant",
		"content": content, "stop_reason": nil}
	if stop != "" {
		message["stop_reason"] = stop
	}
	if tokens > 0 {
		message["usage"] = object{"input_tokens": tokens, "output_tokens": tokens / 6, "cache_read_input_tokens": tokens * 2, "cache_creation_input_tokens": 0}
	}
	t.row(at, "assistant", object{"message": message, "requestId": "req_" + t.r.token(24, alphabet)})
}

func (t *claudeTranscript) edit(c *clock, file, before, after string) {
	id := "toolu_" + t.r.token(24, alphabet)
	t.assistant(c.next(), []object{{"type": "tool_use", "id": id, "name": "Edit", "input": object{"file_path": file, "old_string": before, "new_string": after}}}, "tool_use", 900)
	t.user(c.next(), []object{{"type": "tool_result", "tool_use_id": id, "content": "The file " + file + " has been updated."}}, object{
		"toolUseResult": object{"filePath": file, "oldString": before, "newString": after, "userModified": false, "replaceAll": false,
			"structuredPatch": []object{{"oldStart": 1, "oldLines": 1, "newStart": 1, "newLines": 1, "lines": []string{"-" + before, "+" + after}}}},
	})
}

// claudeFiller pads with assistant thinking blocks, which carry no prompt,
// reply, usage or edit for the parser.
func claudeFiller(r source, id, cwd string, at time.Time) func(string) any {
	return func(text string) any {
		return object{"type": "assistant", "timestamp": stamp(at), "sessionId": id, "cwd": cwd, "uuid": r.uuid(), "isSidechain": false,
			"message": object{"model": claudeModel, "id": "msg_" + r.token(24, alphabet), "type": "message", "role": "assistant",
				"content": []object{{"type": "thinking", "thinking": text, "signature": ""}}, "stop_reason": nil}}
	}
}

// writeClaude writes healthy roots (the first with a Task subagent and its
// meta sidecar) and one transcript whose assistant message content is an
// object, which the complete-backup validator rejects.
func (g *generator) writeClaude(r source) error {
	for i := 0; i <= g.opts.sessionsPerAgent; i++ {
		unreadable := i == g.opts.sessionsPerAgent
		t := g.timing(r, i)
		if unreadable {
			t = g.timing(r, 0)
		}
		subject := g.topic(r, i)
		cwd := g.projects[i%len(g.projects)]
		id := r.uuid()
		project := path.Join(".claude/projects", slug(cwd))
		transcript := path.Join(project, id+".jsonl")
		entry := t.entry("claude", id)
		entry.Paths = []string{transcript}
		c := t.clock(r)
		var head, tail jsonl
		root := &claudeTranscript{r: r, sessionID: id, cwd: cwd, lines: &head}
		root.user(c.next(), subject.prompt(), nil)
		if !unreadable {
			entry.FileChanges = 1 + r.IntN(3)
			for change := range entry.FileChanges {
				root.edit(c, filepath.Join(cwd, subject.file(change)), "old "+subject.content(change), "new "+subject.content(change))
			}
		}
		if i == 0 {
			agentID := r.hex(17)
			toolID := "toolu_" + r.token(24, alphabet)
			task := "Survey the " + subject.subject + " for the parent task."
			root.assistant(c.next(), []object{{"type": "tool_use", "id": toolID, "name": "Task",
				"input": object{"description": "Synthetic survey", "prompt": task, "subagent_type": "general-purpose"}}}, "tool_use", 1100)
			var sidechain jsonl
			child := &claudeTranscript{r: r, sessionID: id, cwd: cwd, agentID: agentID, lines: &sidechain}
			childEnd := c.now.Add(90 * time.Second)
			if childEnd.After(t.end) {
				childEnd = t.end
			}
			childClock := timing{start: c.now, end: childEnd}.clock(r)
			child.user(childClock.next(), task, nil)
			child.assistant(childClock.last(), []object{{"type": "text", "text": "Synthetic survey complete."}}, "end_turn", 700)
			c.now = childEnd
			root.user(c.next(), []object{{"type": "tool_result", "tool_use_id": toolID, "content": []object{{"type": "text", "text": "Synthetic survey complete."}}}}, object{
				"toolUseResult": object{"status": "completed", "prompt": task, "agentId": agentID, "totalDurationMs": 90000, "totalTokens": 1400, "totalToolUseCount": 0,
					"content": []object{{"type": "text", "text": "Synthetic survey complete."}}},
			})
			subagent := path.Join(project, id, "subagents", "agent-"+agentID)
			if err := g.write(subagent+".jsonl", sidechain.Bytes(), childEnd); err != nil {
				return err
			}
			meta := compact(object{"agentType": "general-purpose", "description": "Synthetic survey", "toolUseId": toolID}) + "\n"
			if err := g.write(subagent+".meta.json", []byte(meta), childEnd); err != nil {
				return err
			}
			entry.Members = append(entry.Members, "agent-"+agentID)
			entry.Paths = append(entry.Paths, subagent+".jsonl", subagent+".meta.json")
			entry.Labels = append(entry.Labels, "subagent-family")
		}
		padAt := c.now
		ending := &claudeTranscript{r: r, sessionID: id, cwd: cwd, parentRow: root.parentRow, lines: &tail}
		if unreadable {
			ending.assistant(c.last(), object{"type": "text", "text": subject.reply()}, "end_turn", 1500)
			entry.ExpectedProblem = "artifact_invalid"
			entry.Labels = []string{"unreadable"}
			g.edge("unreadable", "claude", id, "assistant message content is an object, not an array or string", transcript)
		} else {
			tail.add(object{"type": "ai-title", "aiTitle": subject.title(), "sessionId": id})
			ending.assistant(c.next(), []object{{"type": "text", "text": subject.reply()}}, "end_turn", 2500+r.IntN(9000))
			ending.row(c.last(), "system", object{"subtype": "turn_duration", "durationMs": int(t.end.Sub(t.start).Milliseconds()), "isMeta": false})
		}
		if err := g.write(transcript, g.padded(&head, &tail, claudeFiller(r, id, cwd, padAt)), t.end); err != nil {
			return err
		}
		g.add(entry)
	}
	return nil
}
