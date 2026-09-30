package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/directedhandoff"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
)

func projectionFixture(t *testing.T, name, parent string, rows ...string) *transcript {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".jsonl")
	h, _ := json.Marshal(header{Type: "session", Version: 3, ID: name, CWD: "/tmp", Timestamp: "2026-01-01T00:00:00Z", ParentSession: parent})
	if err := os.WriteFile(path, []byte(string(h)+"\n"+strings.Join(rows, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
func userRow(id, parent, text, stamp string) string {
	p := "null"
	if parent != "" {
		p = fmt.Sprintf("%q", parent)
	}
	return fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":%q,"message":{"role":"user","content":%q}}`, id, p, stamp, text)
}
func assistantRow(id, parent, text string, cost int) string {
	return fmt.Sprintf(`{"type":"message","id":%q,"parentId":%q,"timestamp":"2026-01-01T00:00:02Z","message":{"role":"assistant","model":"request","responseModel":"actual","content":%q,"usage":{"input":10,"output":5,"cacheRead":2,"cacheWrite":3,"cost":{"total":%d}}}}`, id, parent, text, cost)
}
func assertCost(t *testing.T, tr, parent *transcript, want float64) {
	t.Helper()
	p, err := projectLeaf(tr, parent, tr.Entries[len(tr.Entries)-1].ID, false)
	if err != nil || p.Session.CostUnavailable || p.RecordedCost == nil || *p.RecordedCost != want {
		t.Fatalf("want %v: %#v, %v", want, p, err)
	}
}

func TestProjectionForkAttribution(t *testing.T) {
	root := userRow("r", "", "shared", "2026-01-01T00:00:01Z")
	a, b := assistantRow("a", "r", "abandoned", 2), assistantRow("b", "r", "selected", 3)
	parent := projectionFixture(t, "parent", "", root, a, b)
	clone := projectionFixture(t, "clone", parent.Path, root, b, assistantRow("new", "b", "new", 7))
	nested := projectionFixture(t, "nested", clone.Path, root, b, assistantRow("new", "b", "new", 7), assistantRow("nestednew", "new", "nested", 11))
	all := projectionFixture(t, "all", parent.Path, root, a, b)
	assertCost(t, parent, nil, 5)
	assertCost(t, clone, parent, 7)
	assertCost(t, nested, clone, 11)
	assertCost(t, all, parent, 0)
	repeated := projectionFixture(t, "repeated", parent.Path, root, b, assistantRow("novel", "b", "selected", 3))
	assertCost(t, repeated, parent, 3)
	parents := parentCache([]*transcript{parent, clone, nested})
	assertCost(t, nested, parents(nested), 11)
	for _, bad := range []*transcript{projectionFixture(t, "missing", "/missing", root, b), projectionFixture(t, "changed", parent.Path, root, assistantRow("b", "r", "selected", 4)), projectionFixture(t, "changedID", parent.Path, strings.Replace(root, `"r"`, `"changed"`, 1), b)} {
		p, _ := projectLeaf(bad, parent, bad.Entries[len(bad.Entries)-1].ID, false)
		if !p.Session.TokensUnavailable || !p.Session.CostUnavailable || len(p.Session.Tokens) != 0 || p.RecordedCost != nil {
			t.Fatalf("partial accounting %#v", p)
		}
	}
	if err := os.Rename(parent.Path, parent.Path+".moved"); err != nil {
		t.Fatal(err)
	}
	if parentCache([]*transcript{clone})(clone) != nil {
		t.Fatal("relocated parent recovered without evidence")
	}
}

func TestProjectionUsageSourcesAndAbsence(t *testing.T) {
	fixture, err := parseTranscript("testdata/schema3.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	fixture.Header.ParentSession = ""
	p, _ := projectLeaf(fixture, nil, "info", false)
	if p.Session.TokensUnavailable || p.Session.CostUnavailable || !p.Session.DetailsIncomplete || p.RecordedCost == nil || *p.RecordedCost != 12.5 {
		t.Fatalf("all five usage sources: %#v", p)
	}
	if p.Session.Tokens["actual"].InputTokens != 1 || p.Session.Tokens["requested"].InputTokens != 0 || p.Session.UnattributedTokens.InputTokens != 35 {
		t.Fatalf("attribution %#v", p.Session)
	}
	normal := projectionFixture(t, "normal", "", userRow("r", "", "hello", "2026-01-01T00:00:01Z"), assistantRow("a", "r", "done", 2), `{"type":"message","id":"tool","parentId":"a","message":{"role":"toolResult","content":"ok"}}`, `{"type":"compaction","id":"c","parentId":"tool","summary":"checkpoint"}`, `{"type":"branch_summary","id":"s","parentId":"c","summary":"branch"}`)
	assertCost(t, normal, nil, 2)
	for _, row := range []string{`{"type":"message","id":"a","message":{"role":"assistant","content":"unknown"}}`, `{"type":"usage","id":"a"}`, assistantRow("a", "", "zero pricing", 0), strings.ReplaceAll(assistantRow("a", "", "zero reporting", 2), `"input":10,"output":5,"cacheRead":2,"cacheWrite":3`, `"input":0,"output":0,"cacheRead":0,"cacheWrite":0`)} {
		tr := projectionFixture(t, "unknown", "", row)
		p, _ := projectLeaf(tr, nil, "a", false)
		if !p.Session.TokensUnavailable && !p.Session.CostUnavailable {
			t.Fatal("ambiguous reporting accepted")
		}
	}
	missingTokens := projectionFixture(t, "knownCost", "", `{"type":"usage","id":"a","model":"m","usage":{"cost":{"total":5}}}`)
	p, _ = projectLeaf(missingTokens, nil, "a", false)
	if !p.Session.TokensUnavailable || p.Session.CostUnavailable || p.RecordedCost == nil || *p.RecordedCost != 5 {
		t.Fatal("known cost lost")
	}
}

func TestProjectionBranchesAndCurrentContext(t *testing.T) {
	tr := projectionFixture(t, "branches", "", userRow("r", "", "shared", "2026-01-01T00:00:03Z"), assistantRow("a", "r", "abandoned recap", 2), userRow("u", "r", "chosen prompt", ""), assistantRow("b", "u", "chosen recap", 3), userRow("other", "", "second root", "2026-01-01T00:00:02Z"))
	p, _ := projectLeaf(tr, nil, "b", true)
	s := p.Session
	if *s.Summary != "chosen recap" || *s.Model != "actual" || s.Turns != 3 || len(s.Digest) != 5 || s.ContextTokens == nil {
		t.Fatalf("state %#v", s)
	}
	ids := []string{}
	for _, row := range s.Digest {
		ids = append(ids, row.SourceEntryID)
	}
	if strings.Join(ids, ",") != "a,u,b,other,r" {
		t.Fatalf("chronological order and append ties: %v", ids)
	}
	seen := map[string]bool{}
	for _, row := range s.Digest {
		if seen[row.SourceEntryID] {
			t.Fatal("duplicate shared row")
		}
		seen[row.SourceEntryID] = true
		if row.SourceEntryID == "u" && row.Time != 0 {
			t.Fatal("invented missing timestamp")
		}
		if row.SourceEntryID == "a" && (*row.Active || *row.ContextSelected) {
			t.Fatal("abandoned active")
		}
	}
	input := synthesis.BuildInput(s)
	if strings.Contains(input, "abandoned recap") || strings.Contains(input, "second root") {
		t.Fatalf("synthesis blended branches: %s", input)
	}
	other, _ := projectLeaf(tr, nil, "a", false)
	if *other.Session.Summary != "abandoned recap" || *other.RecordedCost != *p.RecordedCost {
		t.Fatal("runtime leaf changed accounting")
	}
	for _, row := range other.Session.Digest {
		if row.Active != nil {
			t.Fatal("fallback presented as live active")
		}
	}
	fallback, _ := projectLeaf(tr, nil, "other", false)
	if *fallback.Session.FirstPrompt != "second root" {
		t.Fatal("multiple roots first prompt")
	}
}

func TestProjectionCompactionAndContextEdits(t *testing.T) {
	tr := projectionFixture(t, "context", "", userRow("r", "", "old prompt", "2026-01-01T00:00:01Z"), assistantRow("a", "r", "old recap", 2), `{"type":"compaction","id":"c","parentId":"a","summary":"checkpoint","firstKeptEntryId":"c"}`, userRow("u", "c", "new prompt", "2026-01-01T00:00:04Z"), assistantRow("b", "u", "new recap", 3), `{"type":"context_edit","id":"edit","parentId":"b","targetId":"b","replacement":{"content":"edited recap"}}`)
	p, _ := projectLeaf(tr, nil, "edit", false)
	if *p.Session.FirstPrompt != "new prompt" || *p.Session.Summary != "edited recap" || p.Session.CompactionSeed != "checkpoint" {
		t.Fatalf("context %#v", p.Session)
	}
	input := synthesis.BuildInput(p.Session)
	if strings.Contains(input, "old recap") {
		t.Fatal("summarized history entered synthesis")
	}
}

func TestProjectionToolsAndInvalidAncestry(t *testing.T) {
	tr := projectionFixture(t, "tools", "", `{"type":"message","id":"a","message":{"role":"assistant","content":[{"type":"toolCall","id":"t","name":"bash","arguments":{"command":"git commit -m 'done'"}},{"type":"toolCall","id":"w","name":"write","arguments":{"path":"x.go","content":"one\ntwo\n"}}],"usage":{"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"cost":{"total":1}}}}`, `{"type":"message","id":"b","parentId":"a","message":{"role":"toolResult","toolCallId":"t","content":"[main abcdef1] done","isError":false}}`, `{"type":"message","id":"wresult","parentId":"b","message":{"role":"toolResult","toolCallId":"w","content":"written","isError":false}}`)
	p, _ := projectLeaf(tr, nil, "b", false)
	if p.Session.ToolUses != 2 || len(p.Session.Commands) != 1 || p.Session.EditedFileCount != 1 || p.Session.FileEdits[0].Additions != 2 || len(p.Session.CommitLog) != 1 {
		t.Fatalf("tools %#v", p.Session)
	}
	orphan := projectionFixture(t, "orphan", "", userRow("a", "missing", "visible", ""))
	p, _ = projectLeaf(orphan, nil, "a", false)
	if !p.Session.CostUnavailable || len(p.Session.Digest) != 1 {
		t.Fatal("orphan hidden or accounted")
	}
	p, _ = projectLeaf(orphan, nil, "a", true)
	if p.Session.Digest[0].Active == nil || !*p.Session.Digest[0].Active {
		t.Fatal("known runtime leaf lost active evidence")
	}
	cycle := projectionFixture(t, "cycle", "", userRow("a", "b", "a", ""), userRow("b", "a", "b", ""))
	p, _ = projectLeaf(cycle, nil, "b", false)
	if !p.Session.CostUnavailable {
		t.Fatal("cycle accounted")
	}
}

func TestProjectionLabelRewritesAndStableSegments(t *testing.T) {
	root := userRow("r", "", "shared", "2026-01-01T00:00:01Z")
	label := `{"type":"label","id":"oldlabel","parentId":"r","targetId":"r","label":"bookmark"}`
	billing := assistantRow("a", "oldlabel", "copied", 3)
	parent := projectionFixture(t, "labelparent", "", root, label, billing)
	clone := projectionFixture(t, "labelclone", parent.Path, root, assistantRow("a", "r", "copied", 3), `{"type":"label","id":"newlabel","parentId":"a","targetId":"r","label":"bookmark"}`, assistantRow("new", "newlabel", "new", 7))
	assertCost(t, clone, parent, 7)
	before, _ := projectLeaf(parent, nil, "a", false)
	branch := userRow("b", "r", "later branch", "2026-01-01T00:00:04Z")
	parent2 := projectionFixture(t, "labelparent2", "", root, label, billing, branch)
	after, _ := projectLeaf(parent2, nil, "a", false)
	for _, old := range before.Session.Digest {
		for _, new := range after.Session.Digest {
			if old.SourceEntryID == new.SourceEntryID && old.BranchID != new.BranchID {
				t.Fatal("adding branch mutated existing segment")
			}
		}
	}
}

func TestContextEditRemovesObsoleteGoalCandidate(t *testing.T) {
	tr := projectionFixture(t, "editedGoal", "", userRow("r", "", "obsolete goal", "2026-01-01T00:00:01Z"), assistantRow("a", "r", "obsolete recap", 2), `{"type":"context_edit","id":"removed","parentId":"a","targetId":"r","replacement":null}`, `{"type":"context_edit","id":"replaced","parentId":"removed","targetId":"a","replacement":{"content":"current recap"}}`, userRow("u", "replaced", "current goal", "2026-01-01T00:00:04Z"))
	p, _ := projectLeaf(tr, nil, "u", false)
	input := synthesis.BuildInput(p.Session)
	if *p.Session.FirstPrompt != "current goal" || *p.Session.Summary != "current recap" || strings.Contains(input, "obsolete") || !strings.Contains(input, "current recap") {
		t.Fatalf("obsolete context persisted: %s", input)
	}
	if len(p.Session.Digest) != 3 {
		t.Fatal("historical inspector rows removed")
	}
}

func TestProjectionTodosFollowSelectedPathWithDistinctDigestRows(t *testing.T) {
	root := userRow("r", "", "goal", "2026-01-01T00:00:01Z")
	a := strings.Replace(assistantRow("a", "r", "A recap", 2), `"content":"A recap"`, `"content":[{"type":"text","text":"A recap"},{"type":"toolCall","id":"ta","name":"todos","arguments":{"todos":[{"text":"A task","done":true}]}}]`, 1)
	b := strings.Replace(assistantRow("b", "r", "B recap", 3), `"content":"B recap"`, `"content":[{"type":"text","text":"B recap"},{"type":"toolCall","id":"tb","name":"todos","arguments":{"todos":[{"text":"B task","done":false}]}}]`, 1)
	tr := projectionFixture(t, "todos", "", root, a, b)
	p, _ := projectLeaf(tr, nil, "b", true)
	if len(p.Session.Todos) != 1 || p.Session.Todos[0].Text != "B task" || p.Session.Todos[0].Done {
		t.Fatal("abandoned todos selected")
	}
	categories := map[string]bool{}
	for _, row := range p.Session.Digest {
		if row.SourceEntryID == "b" {
			categories[row.Category] = true
		}
	}
	if !categories["recap"] || !categories["todos"] {
		t.Fatal("multi-category source rows collapsed")
	}
	if strings.Contains(synthesis.BuildInput(p.Session), "A task") {
		t.Fatal("abandoned todo entered synthesis")
	}
}

func TestProjectionFailedWritesAreNotEdits(t *testing.T) {
	tr := projectionFixture(t, "failedWrite", "", `{"type":"message","id":"a","message":{"role":"assistant","content":[{"type":"toolCall","id":"w","name":"write","arguments":{"path":"x.go","content":"new"}}],"usage":{"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"cost":{"total":1}}}}`, `{"type":"message","id":"r","parentId":"a","message":{"role":"toolResult","toolCallId":"w","content":"permission denied","isError":true}}`)
	p, _ := projectLeaf(tr, nil, "r", false)
	if p.Session.EditedFileCount != 0 || p.Session.ToolUses != 1 || p.Session.Errors != 1 {
		t.Fatal("failed write counted as changed file")
	}
}

func TestContextReplacementRetainsSecondPromptAndEarlierRecap(t *testing.T) {
	tr := projectionFixture(t, "replacementContext", "", userRow("r", "", "Build service", "2026-01-01T00:00:01Z"), assistantRow("a", "r", "obsolete decision", 2), userRow("u", "a", "obsolete requirement", "2026-01-01T00:00:03Z"), assistantRow("b", "u", "Understood", 3), `{"type":"context_edit","id":"eu","parentId":"b","targetId":"u","replacement":{"content":"Never send customer data to the external service"}}`, `{"type":"context_edit","id":"ea","parentId":"eu","targetId":"a","replacement":{"content":"Keep all customer processing local"}}`)
	p, _ := projectLeaf(tr, nil, "ea", true)
	input := synthesis.BuildInput(p.Session)
	for _, want := range []string{"Never send customer data to the external service", "Keep all customer processing local"} {
		if !strings.Contains(input, want) {
			t.Fatalf("replacement disappeared: %s", input)
		}
	}
	if strings.Contains(input, "obsolete") {
		t.Fatalf("obsolete context supplied: %s", input)
	}
	if *p.Session.FirstPrompt != "Build service" || *p.Session.Summary != "Understood" || len(p.Session.Digest) != 4 {
		t.Fatal("state or historical rows changed")
	}
	for _, row := range p.Session.Digest {
		if row.SourceEntryID == "u" && row.Description != "obsolete requirement" {
			t.Fatal("historical text replaced")
		}
		if row.SourceEntryID == "a" && row.Description != "obsolete decision" {
			t.Fatal("historical text replaced")
		}
	}
}

func TestDivergentSegmentBypassesMutableLabels(t *testing.T) {
	root := userRow("r", "", "shared", "2026-01-01T00:00:01Z")
	first := assistantRow("a", "r", "first", 2)
	for _, labelID := range []string{"oldLabel", "regeneratedLabel", ""} {
		rows := []string{root, first}
		parent := "r"
		if labelID != "" {
			rows = append(rows, fmt.Sprintf(`{"type":"label","id":%q,"parentId":"r","targetId":"r","label":"bookmark"}`, labelID))
			parent = labelID
		}
		rows = append(rows, assistantRow("b", parent, "second", 3))
		tr := projectionFixture(t, "segments"+labelID, "", rows...)
		p, _ := projectLeaf(tr, nil, "b", false)
		for _, row := range p.Session.Digest {
			if row.SourceEntryID == "b" && row.BranchID != "b" {
				t.Fatalf("mutable segment for label %q: %q", labelID, row.BranchID)
			}
		}
	}
}

func TestEmptyContextReplacementDoesNotRestoreHistoricalText(t *testing.T) {
	tr := projectionFixture(t, "emptyReplacement", "", userRow("r", "", "Build service", "2026-01-01T00:00:01Z"), assistantRow("a", "r", "Earlier answer", 2), userRow("u", "a", "obsolete requirement", "2026-01-01T00:00:03Z"), assistantRow("b", "u", "Understood", 3), `{"type":"context_edit","id":"e","parentId":"b","targetId":"u","replacement":{"content":""}}`)
	p, _ := projectLeaf(tr, nil, "e", true)
	if strings.Contains(synthesis.BuildInput(p.Session), "obsolete requirement") {
		t.Fatal("empty replacement restored source text")
	}
	found := false
	for _, row := range p.Session.Digest {
		if row.SourceEntryID == "u" {
			found = true
			if row.Description != "obsolete requirement" || row.ContextDescription == nil || *row.ContextDescription != "" || row.ContextSelected == nil || !*row.ContextSelected || row.Active == nil || !*row.Active {
				t.Fatalf("empty replacement metadata/history lost: %#v", row)
			}
		}
	}
	if !found || len(p.Session.Digest) != 4 {
		t.Fatal("replacement changed historical row identity/count")
	}
}

func TestRetainedOlderCompactionContext(t *testing.T) {
	tr := projectionFixture(t, "doubleCompaction", "", userRow("r", "", "goal", "2026-01-01T00:00:01Z"), assistantRow("a", "r", "retained answer", 2), `{"type":"compaction","id":"c1","parentId":"a","timestamp":"2026-01-01T00:00:03Z","summary":"obsolete checkpoint","firstKeptEntryId":"c1"}`, `{"type":"compaction","id":"c2","parentId":"c1","timestamp":"2026-01-01T00:00:04Z","summary":"current checkpoint","firstKeptEntryId":"a"}`)
	p, _ := projectLeaf(tr, nil, "c2", false)
	input := synthesis.BuildInput(p.Session)
	if p.Session.Cost == nil || *p.Session.Cost != 2 || p.Session.Compactions != 2 {
		t.Fatal("compaction projection changed raw accounting/history")
	}
	if p.Session.CompactionSeed != "current checkpoint" || p.Session.Summary == nil || *p.Session.Summary != "retained answer" {
		t.Fatalf("native selected context mismatch: %#v", p.Session)
	}
	found := false
	for _, row := range p.Session.Digest {
		if row.Description == "obsolete checkpoint" {
			found = true
			if row.ContextSelected == nil || *row.ContextSelected {
				t.Fatal("old checkpoint must remain historical only")
			}
		}
	}
	if !found {
		t.Fatal("raw old checkpoint history removed")
	}
	if strings.Contains(input, "obsolete checkpoint") {
		t.Fatalf("discarded older checkpoint appears in selected input: %s", input)
	}
}

func TestVerifiedEmptyPiAccountingIsKnownZero(t *testing.T) {
	value := &transcript{Header: header{ID: "empty"}, ByID: map[string]int{}}
	facts, err := projectLeaf(value, nil, "", false)
	if err != nil || facts.Session.TokensUnavailable || !facts.Session.TokensKnown || facts.RecordedCost == nil || *facts.RecordedCost != 0 {
		t.Fatalf("verified empty accounting lost: %#v, %v", facts, err)
	}
}

func TestProjectionInvalidatesChangedContextUsage(t *testing.T) {
	for _, row := range []string{
		`{"type":"context_edit","id":"later","parentId":"a","targetId":"a","replacement":{"content":"changed"}}`,
		`{"type":"message","id":"later","parentId":"a","message":{"role":"user","content":"follow up"}}`,
		`{"type":"custom_message","id":"later","parentId":"a","customType":"extension","content":"extra context","display":false}`,
	} {
		tr := projectionFixture(t, "changedContext", "", userRow("u", "", "goal", "2026-01-01T00:00:01Z"), assistantRow("a", "u", "answer", 2), row)
		p, _ := projectLeaf(tr, nil, "later", false)
		if p.Session.ContextTokens != nil {
			t.Fatal("stale usage retained after context changes")
		}
		if p.Session.TokensUnavailable || p.RecordedCost == nil || *p.RecordedCost != 2 {
			t.Fatal("context change altered recorded accounting")
		}
		var fresh entry
		fresh.Raw = []byte(assistantRow("fresh", "later", "fresh answer", 3))
		if err := json.Unmarshal(fresh.Raw, &fresh); err != nil {
			t.Fatal(err)
		}
		fresh.AppendIndex = len(tr.Entries)
		if err := extractUsage(&fresh); err != nil {
			t.Fatal(err)
		}
		tr.ByID[fresh.ID] = fresh.AppendIndex
		tr.Entries = append(tr.Entries, fresh)
		p, _ = projectLeaf(tr, nil, "fresh", false)
		if p.Session.ContextTokens == nil || *p.Session.ContextTokens != 20 {
			t.Fatal("new assistant evidence did not restore context usage")
		}
		tr.Entries = tr.Entries[:2]
		p, _ = projectLeaf(tr, nil, "a", false)
		if p.Session.ContextTokens == nil || *p.Session.ContextTokens != 20 {
			t.Fatal("verified assistant usage lost")
		}
	}
}

func TestProjectionCustomMessageContext(t *testing.T) {
	tr := projectionFixture(t, "customContext", "", userRow("u", "", "goal", "2026-01-01T00:00:01Z"), assistantRow("a", "u", "answer", 2),
		`{"type":"custom_message","id":"old","parentId":"a","timestamp":"2026-01-01T00:00:03Z","customType":"extension","content":"old extension context","display":true}`,
		`{"type":"compaction","id":"c","parentId":"old","timestamp":"2026-01-01T00:00:04Z","summary":"checkpoint","firstKeptEntryId":"c"}`,
		`{"type":"custom_message","id":"custom","parentId":"c","timestamp":"2026-01-01T00:00:05Z","customType":"extension","content":[{"type":"text","text":"original extension context"}],"display":false}`,
		`{"type":"context_edit","id":"edit","parentId":"custom","timestamp":"2026-01-01T00:00:06Z","targetId":"custom","replacement":{"content":"replacement extension context"}}`)
	p, _ := projectLeaf(tr, nil, "edit", false)
	input := synthesis.BuildInput(p.Session)
	if !strings.Contains(input, "replacement extension context") || strings.Contains(input, "original extension context") || strings.Contains(input, "old extension context") {
		t.Fatalf("custom selected context mismatch: %s", input)
	}
	if p.Session.Turns != 1 {
		t.Fatal("extension context counted as user turn")
	}
	rows := map[string]string{}
	for _, row := range p.Session.Digest {
		rows[row.SourceEntryID] = row.Description
	}
	if !strings.Contains(rows["custom"], "original extension context") || !strings.Contains(rows["old"], "old extension context") {
		t.Fatal("custom raw history missing")
	}
	tr.Entries[len(tr.Entries)-1].Raw = []byte(`{"type":"context_edit","id":"edit","parentId":"custom","targetId":"custom","replacement":null}`)
	p, _ = projectLeaf(tr, nil, "edit", false)
	if strings.Contains(synthesis.BuildInput(p.Session), "original extension context") {
		t.Fatal("deleted custom context retained")
	}
}

func TestProjectionEmptyContentEditedIntoText(t *testing.T) {
	for _, entryFormat := range []string{
		`{"type":"custom_message","id":"custom","parentId":"u","content":%s,"customType":"extension","display":true}`,
		`{"type":"message","id":"custom","parentId":"u","message":{"role":"user","content":%s}}`,
	} {
		for _, content := range []string{`""`, `[{"type":"image","mimeType":"image/png","data":"AA=="}]`} {
			tr := projectionFixture(t, "editedEmpty", "", userRow("u", "", "goal", "2026-01-01T00:00:01Z"),
				fmt.Sprintf(entryFormat, content),
				`{"type":"context_edit","id":"edit","parentId":"custom","targetId":"custom","replacement":{"content":"new extension context"}}`)
			p, _ := projectLeaf(tr, nil, "edit", false)
			if !strings.Contains(synthesis.BuildInput(p.Session), "new extension context") {
				t.Fatal("replacement for textless native context dropped")
			}
			found := false
			for _, row := range p.Session.Digest {
				if row.SourceEntryID == "custom" {
					found = true
					if row.Description != "" || row.ContextDescription == nil || *row.ContextDescription != "new extension context" {
						t.Fatal("invented historical text or lost replacement")
					}
				}
			}
			if !found {
				t.Fatal("context-only digest row missing")
			}
			p, _ = projectLeaf(tr, nil, "custom", false)
			for _, row := range p.Session.Digest {
				if row.SourceEntryID == "custom" {
					t.Fatal("unselected edit invented historical row")
				}
			}
		}
	}
}

func TestProjectionPreservesHandoffMarker(t *testing.T) {
	store, err := directedhandoff.Open(filepath.Join(t.TempDir(), "handoffs.json"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Start("local", "codex", "origin", "pi", "custom")
	if err != nil {
		t.Fatal(err)
	}
	prompt := directedhandoff.Marker(record.ID) + "\nDo the requested work\n" + strings.Repeat("é", 300)
	tr := projectionFixture(t, "handoff", "", userRow("u", "", prompt, "2026-01-01T00:00:01Z"))
	projected, err := projectLeaf(tr, nil, "u", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Observe("local", []*session.Session{projected.Session}); err != nil {
		t.Fatal(err)
	}
	if store.List()[0].TargetSessionID != "handoff" {
		t.Fatal("Pi prompt lost standalone handoff marker")
	}
	if len([]rune(*projected.Session.FirstPrompt)) > session.TruncateTextLimit {
		t.Fatal("unbounded prompt")
	}
}
func TestProjectionSessionTitleSurvivesBranchSelection(t *testing.T) {
	tr := projectionFixture(t, "named", "", userRow("u", "", "original", "2026-01-01T00:00:01Z"),
		`{"type":"session_info","id":"name1","parentId":"u","name":"Old name"}`,
		`{"type":"session_info","id":"name2","parentId":"name1","name":"New name"}`,
		userRow("branch", "u", "alternate", "2026-01-01T00:00:02Z"))
	for _, leaf := range []string{"u", "branch", "name1"} {
		p, err := projectLeaf(tr, nil, leaf, true)
		if err != nil || p.Name != "New name" {
			t.Fatalf("leaf %s lost latest title: %#v, %v", leaf, p, err)
		}
	}
}

func TestProjectionContextIncludesNativeOutput(t *testing.T) {
	row := `{"type":"message","id":"a","message":{"role":"assistant","model":"m","usage":{"input":100,"output":40,"cacheRead":10,"cacheWrite":5,"cost":{"total":1}}}}`
	parsed, err := projectLeaf(projectionFixture(t, "context-output", "", row), nil, "a", false)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Session.ContextTokens == nil || *parsed.Session.ContextTokens != 155 {
		t.Fatalf("context must match Pi's post-response occupancy: %+v", parsed.Session.ContextTokens)
	}
}
func TestProjectionForkLargeCounterMismatch(t *testing.T) {
	row := `{"type":"message","id":"a","message":{"role":"assistant","model":"m","usage":{"input":9007199254740992,"output":1,"cacheRead":0,"cacheWrite":0,"cost":{"total":1}}}}`
	parent := projectionFixture(t, "precision-parent", "", row)
	matching := projectionFixture(t, "precision-matching", parent.Path, row)
	assertCost(t, matching, parent, 0)
	fork := projectionFixture(t, "precision-fork", parent.Path, strings.Replace(row, "9007199254740992", "9007199254740993", 1))
	parsed, err := projectLeaf(fork, parent, "a", false)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Session.TokensUnavailable || !parsed.Session.CostUnavailable {
		t.Fatal("changed inherited counter was verified")
	}
}

func TestProjectionRepeatedLabelChains(t *testing.T) {
	rows := []string{userRow("root", "", "shared", "2026-01-01T00:00:01Z")}
	parent := "root"
	for i := 0; i < 3000; i++ {
		id := fmt.Sprintf("label-%d", i)
		rows = append(rows, fmt.Sprintf(`{"type":"label","id":%q,"parentId":%q}`, id, parent))
		parent = id
	}
	for i := 0; i < 3000; i++ {
		rows = append(rows, assistantRow(fmt.Sprintf("message-%d", i), parent, "recap", 1))
	}
	parsed, err := projectLeaf(projectionFixture(t, "labels", "", rows...), nil, "message-2999", false)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Session.CostUnavailable || parsed.RecordedCost == nil || *parsed.RecordedCost != 3000 || len(parsed.Session.Digest) != 3001 {
		t.Fatalf("label branches changed accounting/history: %+v", parsed.Session)
	}
	if parsed.Session.Digest[1].BranchID != "root" || parsed.Session.Digest[2].BranchID != "message-1" {
		t.Fatal("label-transparent branch identities changed")
	}
}

type cancelLabelContext struct {
	context.Context
	checks int
}

func (c *cancelLabelContext) Err() error {
	c.checks++
	if c.checks >= 10 {
		return context.Canceled
	}
	return nil
}
func TestLabelParentsCancellationAndInvalidChains(t *testing.T) {
	tr := &transcript{ByID: map[string]int{}}
	for i := 0; i < 100; i++ {
		id, parent := fmt.Sprint(i), fmt.Sprint(i+1)
		tr.ByID[id] = i
		tr.Entries = append(tr.Entries, entry{ID: id, Type: "label", ParentID: &parent})
	}
	ctx := &cancelLabelContext{Context: context.Background()}
	id := "0"
	if _, ok := labelParents(ctx, tr)(&id); ok || ctx.checks != 10 {
		t.Fatalf("chain did not honor mid-walk cancellation: %d", ctx.checks)
	}
	resolver := labelParents(context.Background(), tr)
	if _, ok := resolver(&id); ok {
		t.Fatal("orphan label chain accepted")
	}
	tr.Entries[99].ParentID = &id
	if _, ok := labelParents(context.Background(), tr)(&id); ok {
		t.Fatal("cyclic label chain accepted")
	}
	tr.Entries[99].ParentID = nil
	resolver = labelParents(context.Background(), tr)
	if got, ok := resolver(&id); !ok || got != "" {
		t.Fatal("null-root label chain rejected")
	}
	if got, ok := resolver(nil); !ok || got != "" {
		t.Fatal("null parent rejected")
	}
}
