package main

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const openCodeDB = ".local/share/opencode/opencode.db"

const openCodeSchema = `
CREATE TABLE project (id TEXT PRIMARY KEY, worktree TEXT NOT NULL, vcs TEXT, name TEXT, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, time_initialized INTEGER, sandboxes TEXT NOT NULL DEFAULT '[]');
CREATE TABLE session (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT, slug TEXT NOT NULL, directory TEXT NOT NULL, title TEXT NOT NULL, version TEXT NOT NULL, share_url TEXT,
	summary_additions INTEGER, summary_deletions INTEGER, summary_files INTEGER, summary_diffs TEXT, revert TEXT, permission TEXT, agent TEXT, model TEXT, cost REAL NOT NULL DEFAULT 0,
	time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, time_compacting INTEGER, time_archived INTEGER);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE todo (session_id TEXT NOT NULL, content TEXT NOT NULL, status TEXT NOT NULL, priority TEXT NOT NULL, position INTEGER NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, PRIMARY KEY (session_id, position));
CREATE TABLE session_share (session_id TEXT PRIMARY KEY, id TEXT NOT NULL, secret TEXT NOT NULL, url TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL);
CREATE INDEX message_session_idx ON message (session_id);
CREATE INDEX part_message_idx ON part (message_id);
CREATE INDEX session_project_idx ON session (project_id);
`

type openCodeWriter struct {
	tx      *sql.Tx
	r       source
	counter uint64
	err     error
}

type openCodeMessage struct {
	created time.Time
	data    object
	parts   []object
}

// id follows OpenCode's shape: 12 hex digits of creation time and a counter,
// then random base62.
func (w *openCodeWriter) id(prefix string, at time.Time) string {
	w.counter++
	return fmt.Sprintf("%s_%012x%s", prefix, (uint64(at.UnixMilli())<<12+w.counter%4096)&(1<<48-1), w.r.token(14, alphabet))
}

func (w *openCodeWriter) exec(query string, args ...any) {
	if w.err == nil {
		_, w.err = w.tx.Exec(query, args...)
	}
}

func millis(t time.Time) int64 { return t.UnixMilli() }

func openCodeUser(at time.Time, text string) openCodeMessage {
	return openCodeMessage{created: at, data: object{"role": "user", "time": object{"created": millis(at)}, "agent": "build",
		"model": object{"providerID": "anthropic", "modelID": "claude-sonnet-4-5"}}, parts: []object{{"type": "text", "text": text}}}
}

func openCodeAssistant(start, end time.Time, cwd string, input int, parts []object, reply string) openCodeMessage {
	tokens := object{"input": input, "output": 180, "reasoning": 0, "cache": object{"read": 0, "write": 0}}
	parts = append(append([]object{{"type": "step-start"}}, parts...), object{"type": "text", "text": reply, "time": object{"start": millis(end), "end": millis(end)}},
		object{"type": "step-finish", "reason": "stop", "cost": 0.0125, "tokens": tokens})
	return openCodeMessage{created: start, data: object{"role": "assistant", "time": object{"created": millis(start), "completed": millis(end)},
		"modelID": "claude-sonnet-4-5", "providerID": "anthropic", "mode": "build", "agent": "build", "path": object{"cwd": cwd, "root": cwd},
		"cost": 0.0125, "tokens": tokens, "finish": "stop"}, parts: parts}
}

func (w *openCodeWriter) session(id, parent, project, cwd, title string, t timing, messages []openCodeMessage, diffs []object) {
	var parentID any
	if parent != "" {
		parentID = parent
	}
	var summary any
	if len(diffs) > 0 {
		summary = compact(diffs)
	}
	w.exec(`INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, summary_additions, summary_deletions, summary_files, summary_diffs, agent, model, cost, time_created, time_updated)
		VALUES (?, ?, ?, ?, ?, ?, '1.0.20', ?, 0, ?, ?, 'build', ?, ?, ?, ?)`,
		id, project, parentID, "synthetic-"+w.r.token(8, alphabet[36:]), cwd, title, len(diffs), len(diffs), summary,
		compact(object{"id": "claude-sonnet-4-5", "providerID": "anthropic"}), 0.0125*float64(len(messages)/2), millis(t.start), millis(t.end))
	for _, message := range messages {
		messageID := w.id("msg", message.created)
		message.data["id"], message.data["sessionID"] = messageID, id
		updated := millis(message.created)
		w.exec(`INSERT INTO message VALUES (?, ?, ?, ?, ?)`, messageID, id, updated, updated, compact(message.data))
		for _, part := range message.parts {
			partID := w.id("prt", message.created)
			part["id"], part["sessionID"], part["messageID"] = partID, id, messageID
			w.exec(`INSERT INTO part VALUES (?, ?, ?, ?, ?, ?)`, partID, messageID, id, updated, updated, compact(part))
		}
	}
}

func openCodeSize(messages []openCodeMessage) int64 {
	var size int64
	for _, message := range messages {
		size += int64(len(compact(message.data)))
		for _, part := range message.parts {
			size += int64(len(compact(part)))
		}
	}
	return size
}

// writeOpenCode writes one opencode.db holding healthy roots (the first with
// a task child session) and one root whose assistant token counters are
// negative, which the portable record rejects.
func (g *generator) writeOpenCode(r source) (err error) {
	path := g.path(openCodeDB)
	if err := makeParent(path); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer func() { err = joinClose(err, db) }()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	w := &openCodeWriter{tx: tx, r: r}
	w.exec(openCodeSchema)
	projects := map[string]string{}
	for _, workspace := range g.projects {
		projects[workspace] = r.hex(40)
		w.exec(`INSERT INTO project (id, worktree, vcs, name, time_created, time_updated) VALUES (?, ?, 'git', ?, ?, ?)`,
			projects[workspace], workspace, filepath.Base(workspace), millis(g.opts.now.Add(-90*24*time.Hour)), millis(g.opts.now))
	}
	for i := 0; i <= g.opts.sessionsPerAgent; i++ {
		unreadable := i == g.opts.sessionsPerAgent
		t := g.timing(r, i)
		if unreadable {
			t = g.timing(r, 0)
		}
		subject := g.topic(r, i)
		cwd := g.projects[i%len(g.projects)]
		id := w.id("ses", t.start)
		entry := t.entry("opencode", id)
		entry.Paths = []string{openCodeDB}
		c := t.clock(r)
		messages := []openCodeMessage{openCodeUser(c.next(), subject.prompt())}
		if unreadable {
			reply := openCodeAssistant(c.next(), c.last(), cwd, -4096, nil, subject.reply())
			w.session(id, "", projects[cwd], cwd, subject.title(), t, append(messages, reply), nil)
			entry.ExpectedProblem = "artifact_invalid"
			entry.Labels = []string{"unreadable"}
			g.edge("unreadable", "opencode", id, "assistant token counters are negative", openCodeDB)
			g.add(entry)
			continue
		}
		var tools []object
		var diffs []object
		changes := 1 + r.IntN(3)
		for change := range changes {
			at := c.next()
			file := filepath.Join(cwd, subject.file(change))
			tools = append(tools, object{"type": "tool", "callID": "toolu_" + r.token(24, alphabet), "tool": "write", "state": object{
				"status": "completed", "input": object{"filePath": file, "content": subject.content(change)}, "output": "", "title": subject.file(change),
				"metadata": object{"filepath": file, "exists": false}, "time": object{"start": millis(at), "end": millis(at)}}})
			diffs = append(diffs, object{"file": subject.file(change), "additions": 1, "deletions": 0, "status": "added"})
		}
		entry.FileChanges = changes
		var child []openCodeMessage
		childID := ""
		if i == 0 {
			childID = w.id("ses", t.start)
			task := "Survey the " + subject.subject + " for the parent task."
			start, end := c.next(), c.next()
			tools = append(tools, object{"type": "tool", "callID": "toolu_" + r.token(24, alphabet), "tool": "task", "state": object{
				"status": "completed", "input": object{"description": "Synthetic survey", "prompt": task, "subagent_type": "general"},
				"output": "Synthetic survey complete.", "title": "Synthetic survey", "metadata": object{"sessionId": childID},
				"time": object{"start": millis(start), "end": millis(end)}}})
			child = []openCodeMessage{openCodeUser(start, task), openCodeAssistant(start, end, cwd, 900, nil, "Synthetic survey complete.")}
			entry.Members = append(entry.Members, childID)
			entry.Labels = append(entry.Labels, "subagent-family")
		}
		start := c.next()
		size := openCodeSize(append(messages, openCodeAssistant(start, t.end, cwd, 0, tools, subject.reply())))
		for salt := 0; size < g.opts.bytesPerSession; salt++ {
			part := object{"type": "reasoning", "text": filler(min(padChunk, max(256, int(g.opts.bytesPerSession-size))), salt),
				"time": object{"start": millis(start), "end": millis(start)}}
			size += int64(len(compact(part)))
			tools = append(tools, part)
		}
		reply := openCodeAssistant(start, c.last(), cwd, 3000+r.IntN(9000), tools, subject.reply())
		w.session(id, "", projects[cwd], cwd, subject.title(), t, append(messages, reply), diffs)
		if childID != "" {
			w.session(childID, id, projects[cwd], cwd, "Synthetic survey (@general subagent)", t, child, nil)
		}
		for position, status := range []string{"completed", "pending"} {
			w.exec(`INSERT INTO todo VALUES (?, ?, ?, 'medium', ?, ?, ?)`, id, fmt.Sprintf("Synthetic step %d for the %s", position+1, subject.subject),
				status, position, millis(t.start), millis(t.end))
		}
		g.add(entry)
	}
	if w.err != nil {
		return w.err
	}
	return tx.Commit()
}
