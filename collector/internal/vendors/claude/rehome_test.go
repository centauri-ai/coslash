package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// Claude Code re-homes a terminal session onto its background surface by
// writing a new transcript that copies every row of the original and adds
// sessionKind:"bg". Both files land in the same project directory, so without
// collapseBackgroundRehomes each serves as its own session card.

func TestBackgroundRehomeCollapsesIntoSuccessor(t *testing.T) {
	dir := t.TempDir()
	predecessor := writeTranscript(t, dir, "aaaaaaaa-0000-0000-0000-000000000001", transcript{
		prompt: "Fix the login bug",
		turns:  []turn{{id: "msg_1", in: 100, out: 50}},
	})
	successor := writeTranscript(t, dir, "bbbbbbbb-0000-0000-0000-000000000002", transcript{
		prompt:     "Fix the login bug",
		background: true,
		turns: []turn{
			{id: "msg_1", in: 100, out: 50}, // copied verbatim from the predecessor
			{id: "msg_2", in: 80, out: 30},  // the turn that came after the re-home
		},
	})

	survivors := collapse(t, predecessor, successor)

	if len(survivors) != 1 {
		t.Fatalf("survivors = %d; want 1 card for one conversation", len(survivors))
	}
	if got := survivors[0].Session.ID; got != idOf(successor) {
		t.Fatalf("survivor = %s; want the successor %s, whose id is live", got, idOf(successor))
	}
	// applyForkedUsage left msg_1 on the predecessor and msg_2 on the
	// successor, so the fold must total both without double counting.
	tokens := survivors[0].Session.Tokens[testModel]
	if tokens.InputTokens != 180 || tokens.OutputTokens != 80 {
		t.Fatalf("folded tokens = in %d, out %d; want in 180, out 80",
			tokens.InputTokens, tokens.OutputTokens)
	}
}

func TestBackgroundRehomeChainFoldsIntoFinalSurvivor(t *testing.T) {
	dir := t.TempDir()
	a := writeTranscript(t, dir, "aaaaaaaa-0000-0000-0000-00000000000a", transcript{
		prompt: "Start here",
		turns:  []turn{{id: "msg_1", in: 10, out: 1}},
	})
	b := writeTranscript(t, dir, "bbbbbbbb-0000-0000-0000-00000000000b", transcript{
		prompt: "Start here", background: true,
		turns: []turn{{id: "msg_1", in: 10, out: 1}, {id: "msg_2", in: 20, out: 2}},
	})
	c := writeTranscript(t, dir, "cccccccc-0000-0000-0000-00000000000c", transcript{
		prompt: "Start here", background: true,
		turns: []turn{
			{id: "msg_1", in: 10, out: 1},
			{id: "msg_2", in: 20, out: 2},
			{id: "msg_3", in: 30, out: 3},
		},
	})

	survivors := collapse(t, a, b, c)

	if len(survivors) != 1 {
		t.Fatalf("survivors = %d; want 1 for an A ⊂ B ⊂ C chain", len(survivors))
	}
	if got := survivors[0].Session.ID; got != idOf(c) {
		t.Fatalf("survivor = %s; want the final successor %s", got, idOf(c))
	}
	if tokens := survivors[0].Session.Tokens[testModel]; tokens.InputTokens != 60 {
		t.Fatalf("chain input tokens = %d; want 60", tokens.InputTokens)
	}
}

func TestBackgroundSessionWithoutPredecessorSurvives(t *testing.T) {
	dir := t.TempDir()
	// Backgrounded from birth: bg, but shares no messages with anything.
	born := writeTranscript(t, dir, "dddddddd-0000-0000-0000-00000000000d", transcript{
		prompt: "Backgrounded from the start", background: true,
		turns: []turn{{id: "msg_solo", in: 5, out: 5}},
	})
	other := writeTranscript(t, dir, "eeeeeeee-0000-0000-0000-00000000000e", transcript{
		prompt: "Unrelated work",
		turns:  []turn{{id: "msg_other", in: 7, out: 7}},
	})

	if survivors := collapse(t, born, other); len(survivors) != 2 {
		t.Fatalf("survivors = %d; want 2, bg alone is not the duplicate signal", len(survivors))
	}
}

func TestResumeForkKeepsItsOwnCard(t *testing.T) {
	dir := t.TempDir()
	// A deliberate `claude --resume` branch: contained, but never re-homed.
	parent := writeTranscript(t, dir, "ffffffff-0000-0000-0000-00000000000f", transcript{
		prompt: "Original work",
		turns:  []turn{{id: "msg_1", in: 10, out: 1}},
	})
	branch := writeTranscript(t, dir, "99999999-0000-0000-0000-000000000009", transcript{
		prompt: "Original work",
		turns:  []turn{{id: "msg_1", in: 10, out: 1}, {id: "msg_2", in: 20, out: 2}},
	})

	if survivors := collapse(t, parent, branch); len(survivors) != 2 {
		t.Fatalf("survivors = %d; want 2, a resume branch stays visible", len(survivors))
	}
}

// ENG-1377's own evidence: from_handoff_1's only new turn was an aborted "No
// response requested." carrying zero usage, so the pair's usage-bearing
// message.ids are equal and only the row uuids separate them.
func TestBackgroundRehomeWithoutNewUsageCollapses(t *testing.T) {
	dir := t.TempDir()
	predecessor := writeTranscript(t, dir, "aaaaaaaa-0000-0000-0000-0000000000c1", transcript{
		prompt: "Review key decisions made",
		turns:  []turn{{id: "msg_1", in: 100, out: 50}},
	})
	successor := writeTranscript(t, dir, "bbbbbbbb-0000-0000-0000-0000000000c2", transcript{
		prompt: "Review key decisions made", background: true,
		turns:    []turn{{id: "msg_1", in: 100, out: 50}},
		trailing: "No response requested.",
	})

	survivors := collapse(t, predecessor, successor)

	if len(survivors) != 1 {
		t.Fatalf("survivors = %d; want 1, the re-home added no usage-bearing turn",
			len(survivors))
	}
	if got := survivors[0].Session.ID; got != idOf(successor) {
		t.Fatalf("survivor = %s; want the successor %s", got, idOf(successor))
	}
}

// A re-home that lands before the next turn copies the conversation exactly,
// so only its own bookkeeping tail separates the two files.
func TestBackgroundRehomeWithOnlyMetadataCollapses(t *testing.T) {
	dir := t.TempDir()
	predecessor := writeTranscript(t, dir, "aaaaaaaa-0000-0000-0000-0000000000e1", transcript{
		prompt: "Start the work",
		turns:  []turn{{id: "msg_1", in: 100, out: 50}},
	})
	successor := writeTranscript(t, dir, "bbbbbbbb-0000-0000-0000-0000000000e2", transcript{
		prompt: "Start the work", background: true,
		turns:   []turn{{id: "msg_1", in: 100, out: 50}},
		logRows: 1,
	})

	survivors := collapse(t, predecessor, successor)

	if len(survivors) != 1 {
		t.Fatalf("survivors = %d; want 1, the re-home added no conversation row",
			len(survivors))
	}
	if got := survivors[0].Session.ID; got != idOf(successor) {
		t.Fatalf("survivor = %s; want the successor %s", got, idOf(successor))
	}
}

// Two re-homes in a row, neither carrying a new turn. All three files hold the
// same conversation and differ only in how much bookkeeping each accumulated,
// so conversation-row counts alone cannot order them.
func TestMetadataOnlyRehomeChainLeavesOneCard(t *testing.T) {
	dir := t.TempDir()
	a := writeTranscript(t, dir, "aaaaaaaa-0000-0000-0000-0000000000f1", transcript{
		prompt: "Start the work",
		turns:  []turn{{id: "msg_1", in: 100, out: 50}},
	})
	b := writeTranscript(t, dir, "bbbbbbbb-0000-0000-0000-0000000000f2", transcript{
		prompt: "Start the work", background: true,
		turns:   []turn{{id: "msg_1", in: 100, out: 50}},
		logRows: 1,
	})
	c := writeTranscript(t, dir, "cccccccc-0000-0000-0000-0000000000f3", transcript{
		prompt: "Start the work", background: true,
		turns:   []turn{{id: "msg_1", in: 100, out: 50}},
		logRows: 2,
	})

	for _, order := range [][]string{{a, b, c}, {c, b, a}, {b, c, a}} {
		survivors := collapse(t, order...)
		if len(survivors) != 1 {
			t.Fatalf("survivors = %d; want 1, all three are one conversation",
				len(survivors))
		}
		if got := survivors[0].Session.ID; got != idOf(c) {
			t.Fatalf("survivor = %s; want the last re-home %s", got, idOf(c))
		}
	}
}

// A ⊂ B ⊂ C where B is a deliberate resume of A and only C is a re-home.
// C supersedes B alone; A was never re-homed and keeps its card.
func TestRehomedResumeBranchKeepsItsAncestor(t *testing.T) {
	dir := t.TempDir()
	a := writeTranscript(t, dir, "aaaaaaaa-0000-0000-0000-0000000000d1", transcript{
		prompt: "Original work",
		turns:  []turn{{id: "msg_1", in: 10, out: 1}},
	})
	b := writeTranscript(t, dir, "bbbbbbbb-0000-0000-0000-0000000000d2", transcript{
		prompt: "Original work",
		turns:  []turn{{id: "msg_1", in: 10, out: 1}, {id: "msg_2", in: 20, out: 2}},
	})
	c := writeTranscript(t, dir, "cccccccc-0000-0000-0000-0000000000d3", transcript{
		prompt: "Original work", background: true,
		turns: []turn{
			{id: "msg_1", in: 10, out: 1},
			{id: "msg_2", in: 20, out: 2},
			{id: "msg_3", in: 30, out: 3},
		},
	})

	survivors := collapse(t, a, b, c)

	kept := map[string]bool{}
	for _, s := range survivors {
		kept[s.Session.ID] = true
	}
	if !kept[idOf(a)] {
		t.Error("dropped A, which was resumed from but never re-homed")
	}
	if kept[idOf(b)] {
		t.Error("kept B, which C re-homed")
	}
	if len(survivors) != 2 {
		t.Fatalf("survivors = %d; want 2 (A and C)", len(survivors))
	}
	if tokens := survivors[0].Session.Tokens[testModel]; kept[idOf(a)] &&
		tokens.InputTokens != 10 {
		t.Errorf("A input tokens = %d; want its own 10", tokens.InputTokens)
	}
}

func TestSupersededRootRepointsItsSubagents(t *testing.T) {
	dir := t.TempDir()
	predecessorID := "aaaaaaaa-0000-0000-0000-0000000000a1"
	successorID := "bbbbbbbb-0000-0000-0000-0000000000b1"
	predecessor := writeTranscript(t, dir, predecessorID, transcript{
		prompt: "Delegate some work",
		turns:  []turn{{id: "msg_1", in: 10, out: 1}},
	})
	successor := writeTranscript(t, dir, successorID, transcript{
		prompt: "Delegate some work", background: true,
		turns: []turn{{id: "msg_1", in: 10, out: 1}, {id: "msg_2", in: 20, out: 2}},
	})
	// Subagent transcripts stay keyed to the predecessor's uuid on disk; the
	// re-home does not copy the directory.
	subagentDir := filepath.Join(dir, predecessorID, "subagents")
	if err := os.MkdirAll(subagentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	child := writeTranscript(t, subagentDir, "agent-1", transcript{
		prompt: "child work",
		turns:  []turn{{id: "msg_child", in: 3, out: 3}},
	})

	survivors := collapse(t, predecessor, successor, child)

	var repointed *vendors.ParsedSession
	for _, s := range survivors {
		if s.ParentID != "" {
			repointed = s
		}
	}
	if repointed == nil {
		t.Fatal("subagent was dropped; it should follow the surviving root")
	}
	if repointed.ParentID != successorID {
		t.Fatalf("subagent parent = %s; want the successor %s, or composeSessions orphans it",
			repointed.ParentID, successorID)
	}
}

// --- fixture helpers ---

const testModel = "claude-sonnet-4-20250514"

type turn struct {
	id      string
	in, out int
}

type transcript struct {
	prompt     string
	background bool
	turns      []turn
	trailing   string // a prompt with no assistant reply, so it carries no usage
	logRows    int    // uuid-bearing bookkeeping rows that carry no message
}

// collapse runs the transcripts through the same stages as Collect.
func collapse(t *testing.T, paths ...string) []*vendors.ParsedSession {
	t.Helper()
	parsed := make([]*parsedSession, 0, len(paths))
	for _, path := range paths {
		p, err := parse(path)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		parsed = append(parsed, p)
	}
	applyForkedUsage(parsed)
	survivors := collapseBackgroundRehomes(parsed)
	out := make([]*vendors.ParsedSession, 0, len(survivors))
	for _, p := range survivors {
		out = append(out, p.transcript)
	}
	return out
}

func idOf(path string) string { return IDFromPath(path) }

func writeTranscript(t *testing.T, dir, id string, spec transcript) string {
	t.Helper()
	path := filepath.Join(dir, id+".jsonl")
	kind := ""
	if spec.background {
		kind = "bg"
	}
	// Row uuids derive from content, never from the session id: a re-home
	// copies its predecessor's rows verbatim, uuids included.
	rows := []map[string]any{{
		"type": "user", "uuid": "row:" + spec.prompt, "sessionId": id, "sessionKind": kind,
		"timestamp": "2026-08-18T10:00:00.000Z", "cwd": "/test/project",
		"message": map[string]any{"content": spec.prompt},
	}}
	for _, turn := range spec.turns {
		rows = append(rows, map[string]any{
			"type": "assistant", "uuid": "row:" + turn.id, "sessionId": id,
			"sessionKind": kind, "timestamp": "2026-08-18T10:00:01.000Z",
			"durationMs": 500,
			"message": map[string]any{
				"id": turn.id, "model": testModel, "stop_reason": "end_turn",
				"content": []map[string]any{{"type": "text", "text": "ok"}},
				"usage": map[string]any{
					"input_tokens": turn.in, "output_tokens": turn.out,
					"cache_read_input_tokens": 0, "cache_creation_input_tokens": 0,
				},
			},
		})
	}
	if spec.trailing != "" {
		rows = append(rows, map[string]any{
			"type": "user", "uuid": "row:" + spec.trailing, "sessionId": id,
			"sessionKind": kind, "timestamp": "2026-08-18T10:03:00.000Z",
			"message": map[string]any{"content": spec.trailing},
		})
	}
	for i := range spec.logRows {
		rows = append(rows, map[string]any{
			"type": "system", "subtype": "informational",
			"uuid":      fmt.Sprintf("row:log:%s:%d", id, i),
			"sessionId": id, "sessionKind": kind,
			"timestamp": "2026-08-18T10:04:00.000Z",
		})
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	return path
}
