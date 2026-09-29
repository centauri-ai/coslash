package main

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"maps"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	cursorModel = "claude-4.5-sonnet"
	laneIDE     = "cursor-ide"
	laneCLI     = "cursor-cli"
)

type cursorDB struct {
	relative string
	db       *sql.DB
	err      error
}

func (g *generator) openCursorDB(relative, schema string) (*cursorDB, error) {
	path := g.path(relative)
	if err := makeParent(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	store := &cursorDB{relative: relative, db: db}
	store.exec(schema)
	return store, store.err
}

func (store *cursorDB) exec(query string, args ...any) {
	if store.err == nil {
		_, store.err = store.db.Exec(query, args...)
	}
}

func (store *cursorDB) close() error { return errors.Join(store.err, store.db.Close()) }

func cursorUser(at time.Time, text string) object {
	return object{"role": "user", "message": object{"content": []object{{"type": "text",
		"text": "<timestamp>" + at.UTC().Format(time.RFC3339) + "</timestamp>\n<user_query>\n" + text + "\n</user_query>"}}}}
}

func cursorAssistant(blocks ...object) object {
	return object{"role": "assistant", "message": object{"content": blocks}}
}

func cursorText(text string) object { return object{"type": "text", "text": text} }

func cursorTool(r source, name string, input object) object {
	return object{"type": "tool_use", "id": "toolu_" + r.token(24, alphabet), "name": name, "input": input}
}

// cursorProject is the per-workspace folder Cursor keeps agent transcripts in.
func cursorProject(cwd string) string {
	return path.Join(".cursor/projects", strings.TrimLeft(slug(cwd), "-"))
}

func cursorHeader(id, name, cwd string, t timing, extra object) string {
	header := object{"type": "head", "composerId": id, "name": name, "createdAt": millis(t.start), "lastUpdatedAt": millis(t.end), "unifiedMode": "agent",
		"workspaceIdentifier": object{"id": slug(cwd), "uri": object{"$mid": 1, "fsPath": cwd, "external": "file://" + filepath.ToSlash(cwd), "path": filepath.ToSlash(cwd), "scheme": "file"}}}
	maps.Copy(header, extra)
	return compact(header)
}

// writeCursor alternates IDE and CLI roots (the first IDE root has a subagent
// with its own composer header), adds one CLI chat missing its meta.json
// sidecar, and one stray .jsonl in a folder that names no session.
func (g *generator) writeCursor(r source) (err error) {
	global := cursorGlobalStorage()
	state, err := g.openCursorDB(path.Join(global, "state.vscdb"), `
		CREATE TABLE ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB);
		CREATE TABLE cursorDiskKV (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB);
		CREATE TABLE composerHeaders (composerId TEXT PRIMARY KEY, value TEXT, createdAt INTEGER, lastUpdatedAt INTEGER);
		INSERT INTO ItemTable VALUES ('workbench.panel.markers.hidden', 'true');`)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, state.close()) }()
	search, err := g.openCursorDB(path.Join(global, "conversation-search.db"), `CREATE TABLE conversations (id TEXT PRIMARY KEY, title TEXT, source TEXT, updatedAt INTEGER)`)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, search.close()) }()
	tracking, err := g.openCursorDB(".cursor/ai-tracking/ai-code-tracking.db", `CREATE TABLE conversation_summaries (conversationId TEXT PRIMARY KEY, title TEXT, tldr TEXT, overview TEXT, summaryBullets TEXT, model TEXT, mode TEXT, updatedAt INTEGER)`)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, tracking.close()) }()

	for i := 0; i <= g.opts.sessionsPerAgent; i++ {
		unreadable := i == g.opts.sessionsPerAgent
		lane := laneIDE
		if unreadable || i%4 == 1 || i%4 == 2 {
			lane = laneCLI
		}
		t := g.timing(r, i)
		if unreadable {
			t = g.timing(r, 0)
		}
		subject := g.topic(r, i)
		cwd := g.projects[i%len(g.projects)]
		id := r.uuid()
		project := cursorProject(cwd)
		transcript := path.Join(project, "agent-transcripts", id, id+".jsonl")
		entry := t.entry("cursor", id)
		entry.Lane = lane
		entry.Paths = []string{transcript}
		c := t.clock(r)
		var head, tail jsonl
		head.add(cursorUser(c.next(), subject.prompt()))
		head.add(cursorAssistant(cursorText("Looking at the "+subject.subject+" first."),
			cursorTool(r, "Shell", object{"command": "ls src", "working_directory": cwd, "description": "List synthetic sources"})))
		changes := 1 + r.IntN(3)
		for change := range changes {
			head.add(cursorAssistant(cursorTool(r, "Write", object{"path": filepath.Join(cwd, subject.file(change)), "contents": subject.content(change)})))
		}
		entry.FileChanges = changes
		childID, toolID := "", ""
		if i == 0 {
			childID = r.uuid()
			task := cursorTool(r, "Task", object{"description": "Synthetic survey", "prompt": "Survey the " + subject.subject + " for the parent task.", "subagent_type": "explore"})
			toolID = task["id"].(string)
			head.add(cursorAssistant(task))
			var child jsonl
			child.add(cursorUser(c.next(), "Survey the "+subject.subject+" for the parent task."))
			child.add(cursorAssistant(cursorText("Synthetic survey complete.")))
			child.add(object{"type": "turn_ended", "status": "success"})
			childPath := path.Join(project, "agent-transcripts", id, "subagents", childID+".jsonl")
			if err := g.write(childPath, child.Bytes(), c.next()); err != nil {
				return err
			}
			entry.Members = append(entry.Members, childID)
			entry.Paths = append(entry.Paths, childPath)
			entry.Labels = append(entry.Labels, "subagent-family")
		}
		tail.add(cursorAssistant(cursorText(subject.reply())))
		tail.add(object{"type": "turn_ended", "status": "success"})
		data := g.padded(&head, &tail, func(text string) any { return cursorAssistant(cursorText(text)) })
		if err := g.write(transcript, data, c.last()); err != nil {
			return err
		}

		if lane == laneIDE {
			state.exec(`INSERT INTO composerHeaders VALUES (?, ?, ?, ?)`, id, cursorHeader(id, subject.title(), cwd, t, nil), millis(t.start), millis(t.end))
			userBubble, replyBubble := r.uuid(), r.uuid()
			state.exec(`INSERT INTO cursorDiskKV VALUES (?, ?)`, "composerData:"+id, compact(object{"_v": 10, "composerId": id, "name": subject.title(),
				"createdAt": millis(t.start), "lastUpdatedAt": millis(t.end), "modelConfig": object{"modelName": cursorModel, "maxMode": false},
				"contextTokensUsed": 12000 + r.IntN(40000), "contextTokenLimit": 200000,
				"fullConversationHeadersOnly": []object{{"bubbleId": userBubble, "type": 1}, {"bubbleId": replyBubble, "type": 2}}}))
			state.exec(`INSERT INTO cursorDiskKV VALUES (?, ?)`, "bubbleId:"+id+":"+userBubble, compact(object{"_v": 3, "type": 1, "bubbleId": userBubble,
				"text": subject.prompt(), "createdAt": t.start.UTC().Format(time.RFC3339)}))
			state.exec(`INSERT INTO cursorDiskKV VALUES (?, ?)`, "bubbleId:"+id+":"+replyBubble, compact(object{"_v": 3, "type": 2, "bubbleId": replyBubble,
				"text": subject.reply(), "modelInfo": object{"modelName": cursorModel}, "createdAt": t.end.UTC().Format(time.RFC3339)}))
			search.exec(`INSERT INTO conversations VALUES (?, ?, 'local', ?)`, id, subject.title(), millis(t.end))
			tracking.exec(`INSERT INTO conversation_summaries VALUES (?, ?, ?, ?, '[]', ?, 'agent', ?)`, id, subject.title(), subject.reply(), subject.prompt(), cursorModel, millis(t.end))
			entry.Paths = append(entry.Paths, state.relative, search.relative, tracking.relative)
			if childID != "" {
				childTime := timing{start: t.start.Add(time.Minute), end: t.start.Add(3 * time.Minute)}
				state.exec(`INSERT INTO composerHeaders VALUES (?, ?, ?, ?)`, childID, cursorHeader(childID, "Synthetic survey", cwd, childTime,
					object{"subagentInfo": object{"parentComposerId": id, "toolCallId": toolID}}), millis(childTime.start), millis(childTime.end))
				taskBubble := r.uuid()
				state.exec(`INSERT INTO cursorDiskKV VALUES (?, ?)`, "bubbleId:"+id+":"+taskBubble, compact(object{"_v": 3, "type": 2, "bubbleId": taskBubble,
					"createdAt": childTime.start.UTC().Format(time.RFC3339), "toolFormerData": object{"name": "task_v2", "toolCallId": toolID, "status": "completed",
						"params": compact(object{"description": "Synthetic survey"}), "result": compact(object{"agentId": childID})}}))
			}
		} else {
			chat := path.Join(".cursor/chats", r.hex(32), id)
			store, err := g.openCursorDB(path.Join(chat, "store.db"), `CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT); CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB)`)
			if err != nil {
				return err
			}
			prompt, reply := r.hex(64), r.hex(64)
			store.exec(`INSERT INTO meta VALUES ('0', ?)`, hex.EncodeToString([]byte(compact(object{"agentId": id, "latestRootBlobId": reply, "name": subject.title(),
				"mode": "default", "createdAt": millis(t.start), "lastUsedModel": "gpt-5"}))))
			store.exec(`INSERT INTO blobs VALUES (?, ?), (?, ?)`, prompt, []byte(compact(object{"role": "user", "content": subject.prompt()})),
				reply, []byte(compact(object{"role": "assistant", "content": subject.reply()})))
			if err := store.close(); err != nil {
				return err
			}
			entry.Paths = append(entry.Paths, store.relative)
			if unreadable {
				entry.ExpectedProblem = "artifact_unattributable"
				entry.Labels = []string{"unreadable"}
				g.edge("unreadable", "cursor", id, "CLI chat store has no meta.json sidecar", entry.Paths...)
			} else {
				meta := path.Join(chat, "meta.json")
				if err := g.write(meta, []byte(compact(object{"cwd": cwd})+"\n"), t.start); err != nil {
					return err
				}
				entry.Paths = append(entry.Paths, meta)
			}
		}
		g.add(entry)
	}
	stray := path.Join(cursorProject(g.projects[0]), "agent-transcripts", "scratch-notes-draft", "scratch-notes-draft.jsonl")
	var draft jsonl
	draft.add(cursorUser(g.opts.now.Add(-time.Hour), "Synthetic scratch draft that names no session."))
	draft.add(cursorAssistant(cursorText("Synthetic draft noted.")))
	draft.add(object{"type": "turn_ended", "status": "success"})
	g.edge("stray-file", "cursor", "", "unrelated .jsonl in a non-UUID folder that must not block any family", stray)
	return g.write(stray, draft.Bytes(), g.opts.now.Add(-time.Hour))
}
