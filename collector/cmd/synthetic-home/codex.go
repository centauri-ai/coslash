package main

import (
	"fmt"
	"path"
	"time"
)

const codexModel = "gpt-5-codex"

type codexRollout struct {
	id, parentID, cwd string
	start             time.Time
	path              string
}

func codexPath(id string, start time.Time) string {
	return path.Join(".codex/sessions", start.UTC().Format("2006/01/02"), "rollout-"+start.UTC().Format("2006-01-02T15-04-05")+"-"+id+".jsonl")
}

func codexRow(at time.Time, kind string, payload object) object {
	return object{"timestamp": stamp(at), "type": kind, "payload": payload}
}

func codexMeta(at time.Time, rollout codexRollout, source any, nickname string) object {
	payload := object{
		"id": rollout.id, "timestamp": stamp(at), "cwd": rollout.cwd, "originator": "codex_cli_rs",
		"cli_version": "0.63.0", "source": source, "model_provider": "openai",
		"git": object{"branch": "main"},
	}
	if rollout.parentID != "" {
		payload["parent_thread_id"] = rollout.parentID
	}
	if nickname != "" {
		payload["session_id"] = rollout.parentID
		payload["agent_nickname"] = nickname
	}
	return codexRow(at, "session_meta", payload)
}

func codexTurnStart(l *jsonl, c *clock, cwd, prompt string) {
	l.add(codexRow(c.next(), "turn_context", object{"cwd": cwd, "approval_policy": "on-request", "model": codexModel, "effort": "medium"}))
	l.add(codexRow(c.next(), "event_msg", object{"type": "task_started", "model_context_window": 272000}))
	l.add(codexRow(c.next(), "response_item", object{"type": "message", "role": "user", "content": []object{{"type": "input_text", "text": prompt}}}))
	l.add(codexRow(c.next(), "event_msg", object{"type": "user_message", "message": prompt, "images": []any{}}))
}

func codexCommand(l *jsonl, r source, c *clock, cwd, command, output string) {
	call := "call_" + r.token(24, alphabet)
	l.add(codexRow(c.next(), "response_item", object{"type": "function_call", "name": "exec_command", "call_id": call,
		"arguments": compact(object{"cmd": command, "workdir": cwd})}))
	l.add(codexRow(c.next(), "response_item", object{"type": "function_call_output", "call_id": call,
		"output": compact(object{"output": output, "metadata": object{"exit_code": 0, "duration_seconds": 0.2}})}))
}

func codexPatch(l *jsonl, r source, c *clock, changes object) {
	l.add(codexRow(c.next(), "event_msg", object{"type": "patch_apply_end", "call_id": "call_" + r.token(24, alphabet),
		"stdout": "Success.", "stderr": "", "success": true, "changes": changes}))
}

func codexTurnEnd(tail *jsonl, c *clock, reply string, tokens int) {
	usage := object{"input_tokens": tokens, "cached_input_tokens": tokens / 4, "output_tokens": tokens / 8, "reasoning_output_tokens": 0, "total_tokens": tokens + tokens/8}
	tail.add(codexRow(c.next(), "event_msg", object{"type": "token_count", "info": object{"total_token_usage": usage, "last_token_usage": usage, "model_context_window": 272000}}))
	tail.add(codexRow(c.next(), "event_msg", object{"type": "agent_message", "message": reply}))
	tail.add(codexRow(c.last(), "event_msg", object{"type": "task_complete", "last_agent_message": reply}))
}

func codexFiller(at time.Time) func(string) any {
	return func(text string) any {
		return codexRow(at, "response_item", object{"type": "message", "role": "assistant", "content": []object{{"type": "output_text", "text": text}}})
	}
}

// writeCodex writes healthy roots (a subagent family, a family with a hidden
// guardian review, and a session with 100 exact file changes first), one
// rollout whose first row is not session_meta, and the session index.
func (g *generator) writeCodex(r source) error {
	var index jsonl
	for i := 0; i <= g.opts.sessionsPerAgent; i++ {
		unreadable := i == g.opts.sessionsPerAgent
		t := g.timing(r, i)
		if unreadable {
			t = g.timing(r, 0)
		}
		subject := g.topic(r, i)
		root := codexRollout{id: r.uuid(), cwd: g.projects[i%len(g.projects)], start: t.start}
		root.path = codexPath(root.id, t.start)
		entry := t.entry("codex", root.id)
		entry.Paths = []string{root.path}
		c := t.clock(r)
		var head, tail jsonl
		if unreadable {
			head.add(codexRow(c.next(), "event_msg", object{"type": "task_started", "model_context_window": 272000}))
		}
		head.add(codexMeta(t.start, root, "cli", ""))
		codexTurnStart(&head, c, root.cwd, subject.prompt())
		codexCommand(&head, r, c, root.cwd, "ls src", "part-000.txt\n")
		changes := 1 + r.IntN(2)
		if i == 2 {
			changes = 100
			entry.Labels = append(entry.Labels, "file-changes-100")
		}
		for change := range changes {
			if change%2 == 0 {
				codexPatch(&head, r, c, object{subject.file(change): object{"type": "add", "content": subject.content(change)}})
			} else {
				codexPatch(&head, r, c, object{subject.file(change): object{"type": "update", "unified_diff": fmt.Sprintf("@@ -1 +1 @@\n-%s+revised %s", subject.content(change), subject.content(change))}})
			}
		}
		entry.FileChanges = changes
		var children []codexRollout
		switch {
		case i == 0:
			child := codexRollout{id: r.uuid(), parentID: root.id, cwd: root.cwd, start: t.start.Add(time.Minute)}
			codexCommand(&head, r, c, root.cwd, "true", "")
			call := "call_" + r.token(24, alphabet)
			head.add(codexRow(c.next(), "response_item", object{"type": "function_call", "name": "spawn_agent", "call_id": call,
				"arguments": compact(object{"message": "Survey the " + subject.subject + " for the parent task.", "agent_type": "worker"})}))
			head.add(codexRow(c.next(), "response_item", object{"type": "function_call_output", "call_id": call,
				"output": compact(object{"agent_id": child.id, "nickname": "Synthetic Worker"})}))
			children = append(children, child)
			entry.Members = append(entry.Members, child.id)
			entry.Labels = append(entry.Labels, "subagent-family")
		case i == 1:
			children = append(children, codexRollout{id: r.uuid(), parentID: root.id, cwd: root.cwd, start: t.start.Add(2 * time.Minute)})
			entry.Hidden = []string{children[0].id}
			entry.Labels = append(entry.Labels, "guardian-family")
		}
		padAt := c.now
		codexTurnEnd(&tail, c, subject.reply(), 4000+r.IntN(20000))
		if err := g.write(root.path, g.padded(&head, &tail, codexFiller(padAt)), t.end); err != nil {
			return err
		}
		for _, child := range children {
			child.path = codexPath(child.id, child.start)
			entry.Paths = append(entry.Paths, child.path)
			guardian := i == 1
			var rows jsonl
			if guardian {
				rows.add(codexMeta(child.start, child, object{"subagent": object{"other": "guardian"}}, ""))
				rows.add(codexRow(child.start, "event_msg", object{"type": "user_message", "message": "Review the pending synthetic command for approval."}))
				rows.add(codexRow(child.start, "event_msg", object{"type": "agent_message", "message": "Synthetic verdict: approve."}))
			} else {
				childClock := timing{start: child.start, end: child.start.Add(3 * time.Minute)}.clock(r)
				rows.add(codexMeta(child.start, child, object{"subagent": object{"thread_spawn": object{"parent_thread_id": root.id, "depth": 1}}}, "Synthetic Worker"))
				codexTurnStart(&rows, childClock, child.cwd, "Survey the "+subject.subject+" for the parent task.")
				codexTurnEnd(&rows, childClock, "Synthetic survey complete.", 1200)
			}
			modified := child.start.Add(3 * time.Minute)
			if err := g.write(child.path, rows.Bytes(), modified); err != nil {
				return err
			}
		}
		if unreadable {
			entry.ExpectedProblem = "artifact_invalid"
			entry.Labels = []string{"unreadable"}
			g.edge("unreadable", "codex", root.id, "first rollout row is event_msg, not session_meta", root.path)
		} else {
			index.add(object{"id": root.id, "thread_name": subject.title(), "updated_at": t.end.Format(time.RFC3339)})
		}
		switch i {
		case 1:
			g.edge("guardian-family", "codex", root.id, "hidden guardian (auto-review) subagent rollout under the root", entry.Paths...)
		case 2:
			g.edge("file-changes-100", "codex", root.id, "100 exact file-change bodies", root.path)
		}
		g.add(entry)
	}
	return g.write(".codex/session_index.jsonl", index.Bytes(), g.opts.now)
}
