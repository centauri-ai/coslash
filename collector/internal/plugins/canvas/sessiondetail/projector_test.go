package sessiondetail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/session"
)

const sharedID = "019f4dde-db5b-7100-bdc0-09b5aaaac56f"

type claudeGolden struct {
	Agent    string `json:"agent"`
	ID       string `json:"id"`
	TurnLog  []Turn `json:"turnLog"`
	FileEdit struct {
		Path  string `json:"path"`
		Adds  int    `json:"adds"`
		Dels  int    `json:"dels"`
		IsNew bool   `json:"isNew"`
	} `json:"fileEdit"`
	ContextFile struct {
		Path            string `json:"path"`
		Partial         bool   `json:"partial"`
		TotalLines      int    `json:"totalLines"`
		CapturedContent bool   `json:"capturedContent"`
		StartLine       int    `json:"startLine"`
		Content         string `json:"content"`
	} `json:"contextFile"`
}

func TestProjectClaudeMatchesTask00Golden(t *testing.T) {
	var golden claudeGolden
	readGolden(t, "claude-golden.json", &golden)
	path := writeTranscript(t, sharedID+".jsonl", claudeTranscript(sharedID))
	detail, err := New(Options{}).ProjectFile(context.Background(), contracts.SessionIdentity{Agent: "claude", ID: sharedID}, path)
	if err != nil {
		t.Fatalf("ProjectFile: %v", err)
	}
	if detail.Agent != "claude" || detail.ID != sharedID {
		t.Fatalf("identity = {%q,%q}, want composite claude identity", detail.Agent, detail.ID)
	}
	if len(detail.TurnLog) != 2 {
		t.Fatalf("turns = %#v", detail.TurnLog)
	}
	for index := range golden.TurnLog {
		want, got := golden.TurnLog[index], detail.TurnLog[index]
		if got.Prompt != want.Prompt || pointerValue(got.PlanText) != pointerValue(want.PlanText) || got.ToolUses != want.ToolUses {
			t.Errorf("turn %d = %#v, want golden %#v", index+1, got, want)
		}
		if fmt.Sprint(got.Todos) != fmt.Sprint(want.Todos) || !equalDecisions(got.Decisions, want.Decisions) || fmt.Sprint(got.FileEdits) != fmt.Sprint(want.FileEdits) {
			t.Errorf("turn %d detail = %#v, want golden %#v", index+1, got, want)
		}
	}
	if len(detail.FileEdits) != 1 {
		t.Fatalf("file edits = %#v", detail.FileEdits)
	}
	edit := detail.FileEdits[0]
	if edit.Path != golden.FileEdit.Path || edit.Additions != golden.FileEdit.Adds || edit.Deletions != golden.FileEdit.Dels || edit.IsNew != golden.FileEdit.IsNew {
		t.Errorf("file edit = %#v, want golden %#v", edit, golden.FileEdit)
	}
	if len(detail.ContextFiles) != 1 {
		t.Fatalf("context files = %#v", detail.ContextFiles)
	}
	contextFile := detail.ContextFiles[0]
	if contextFile.Path != golden.ContextFile.Path || contextFile.Partial != golden.ContextFile.Partial || contextFile.TotalLines == nil || *contextFile.TotalLines != golden.ContextFile.TotalLines || !contextFile.CapturedContent {
		t.Errorf("context file = %#v, want golden %#v", contextFile, golden.ContextFile)
	}
	if len(contextFile.Segments) != 1 || contextFile.Segments[0].StartLine != golden.ContextFile.StartLine || contextFile.Segments[0].Content != golden.ContextFile.Content {
		t.Errorf("context segments = %#v", contextFile.Segments)
	}
}

type codexGolden struct {
	Turn struct {
		Index    int      `json:"index"`
		Prompt   string   `json:"prompt"`
		PlanText string   `json:"planText"`
		Todos    []Todo   `json:"todos"`
		Decision Decision `json:"decision"`
		FileEdit string   `json:"fileEdit"`
	} `json:"turn"`
	FileEdit struct {
		Path  string `json:"path"`
		Adds  int    `json:"adds"`
		Dels  int    `json:"dels"`
		IsNew bool   `json:"isNew"`
	} `json:"fileEdit"`
	ContextFile struct {
		Path            string `json:"path"`
		Partial         bool   `json:"partial"`
		CapturedContent bool   `json:"capturedContent"`
		CombinedReadID  string `json:"combinedReadId"`
		StartLine       int    `json:"startLine"`
		Content         string `json:"content"`
	} `json:"contextFile"`
	ReadGroup ContextReadGroup `json:"readGroup"`
	Deferred  DeferredContext  `json:"deferred"`
}

func TestProjectCodexMatchesTask00Golden(t *testing.T) {
	var golden codexGolden
	readGolden(t, "codex-golden.json", &golden)
	path := writeTranscript(t, "rollout-2026-08-08T00-00-00-"+sharedID+".jsonl", codexTranscript(sharedID))
	detail, err := New(Options{}).ProjectFile(context.Background(), contracts.SessionIdentity{Agent: "codex", ID: sharedID}, path)
	if err != nil {
		t.Fatalf("ProjectFile: %v", err)
	}
	if detail.Agent != "codex" || detail.ID != sharedID {
		t.Fatalf("identity = {%q,%q}, want composite codex identity", detail.Agent, detail.ID)
	}
	if len(detail.TurnLog) != 1 {
		t.Fatalf("turns = %#v", detail.TurnLog)
	}
	turn := detail.TurnLog[0]
	if turn.Prompt != golden.Turn.Prompt || pointerValue(turn.PlanText) != golden.Turn.PlanText || fmt.Sprint(turn.Todos) != fmt.Sprint(golden.Turn.Todos) {
		t.Errorf("turn = %#v, want golden %#v", turn, golden.Turn)
	}
	if !containsDecision(turn.Decisions, golden.Turn.Decision.Question) || !containsString(turn.FileEdits, golden.Turn.FileEdit) {
		t.Errorf("turn evidence = %#v", turn)
	}
	if len(detail.FileEdits) != 1 || detail.FileEdits[0].Path != golden.FileEdit.Path || detail.FileEdits[0].Additions != golden.FileEdit.Adds || !detail.FileEdits[0].IsNew {
		t.Errorf("file edits = %#v, want golden %#v", detail.FileEdits, golden.FileEdit)
	}
	if len(detail.ContextFiles) != 1 || detail.ContextFiles[0].CombinedReadID == nil || *detail.ContextFiles[0].CombinedReadID != golden.ContextFile.CombinedReadID || detail.ContextFiles[0].CapturedContent != golden.ContextFile.CapturedContent {
		t.Errorf("context files = %#v, want golden %#v", detail.ContextFiles, golden.ContextFile)
	}
	if len(detail.ContextReadGroups) != 1 || detail.ContextReadGroups[0] != golden.ReadGroup {
		t.Errorf("read groups = %#v, want %#v", detail.ContextReadGroups, golden.ReadGroup)
	}
	if len(detail.DeferredContext) != 1 || detail.DeferredContext[0] != golden.Deferred {
		t.Errorf("deferred = %#v, want %#v", detail.DeferredContext, golden.Deferred)
	}
}

func TestProjectUsesCompositeIdentityForDuplicateIDs(t *testing.T) {
	claudePath := writeTranscript(t, sharedID+".jsonl", claudeTranscript(sharedID))
	codexPath := writeTranscript(t, "rollout-2026-08-08T00-00-00-"+sharedID+".jsonl", codexTranscript(sharedID))
	claudeCalls, codexCalls := 0, 0
	projector := New(Options{
		ClaudeResolver: func(_ context.Context, id string) (string, error) { claudeCalls++; return claudePath, nil },
		CodexResolver:  func(_ context.Context, id string) (string, error) { codexCalls++; return codexPath, nil },
	})
	claudeDetail, err := projector.Project(context.Background(), contracts.SessionIdentity{Agent: "claude", ID: sharedID})
	if err != nil {
		t.Fatal(err)
	}
	codexDetail, err := projector.Project(context.Background(), contracts.SessionIdentity{Agent: "codex", ID: sharedID})
	if err != nil {
		t.Fatal(err)
	}
	if claudeCalls != 1 || codexCalls != 1 || claudeDetail.TurnLog[0].Prompt == codexDetail.TurnLog[0].Prompt {
		t.Fatalf("resolver calls claude=%d codex=%d, prompts %#v / %#v", claudeCalls, codexCalls, claudeDetail.TurnLog, codexDetail.TurnLog)
	}
}

func TestProjectKnownPreservesCollectorFacts(t *testing.T) {
	path := writeTranscript(t, sharedID+".jsonl", claudeTranscript(sharedID))
	name, status := "resolved name", "idle"
	known := session.Session{
		Agent: "claude", ID: sharedID, Name: &name, Status: &status,
		SessionDetails: session.SessionDetails{
			Synthesis: &session.SessionSynthesis{Goals: []string{"preserve me"}, Outcome: "resolved"},
		},
	}
	detail, err := New(Options{}).ProjectKnown(context.Background(), known, path)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Name == nil || *detail.Name != name || detail.Status == nil || *detail.Status != status || detail.Synthesis == nil || detail.Synthesis.Outcome != "resolved" {
		t.Fatalf("collector facts were not preserved: %#v", detail.Session)
	}
	if len(detail.TurnLog) == 0 {
		t.Fatal("heavy detail was not projected")
	}
}

func TestProjectRejectsUnknownMissingMalformedAndMismatched(t *testing.T) {
	projector := New(Options{ClaudeResolver: func(context.Context, string) (string, error) { return "", ErrNotFound }})
	if _, err := projector.Project(context.Background(), contracts.SessionIdentity{Agent: "other", ID: "id"}); !errors.Is(err, ErrUnknownAgent) {
		t.Errorf("unknown vendor error = %v", err)
	}
	if _, err := projector.Project(context.Background(), contracts.SessionIdentity{Agent: "claude", ID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing error = %v", err)
	}
	malformed := writeTranscript(t, sharedID+".jsonl", "{\"type\":\"user\"\n")
	if _, err := New(Options{}).ProjectFile(context.Background(), contracts.SessionIdentity{Agent: "claude", ID: sharedID}, malformed); !errors.Is(err, ErrMalformedTranscript) {
		t.Errorf("malformed error = %v", err)
	}
	valid := writeTranscript(t, sharedID+".jsonl", claudeTranscript(sharedID))
	if _, err := New(Options{}).ProjectFile(context.Background(), contracts.SessionIdentity{Agent: "claude", ID: "different"}, valid); !errors.Is(err, ErrIdentityMismatch) {
		t.Errorf("identity mismatch error = %v", err)
	}
}

func TestProjectRejectsNullTranscriptRowsWithoutPanicking(t *testing.T) {
	for _, test := range []struct {
		agent      string
		transcript string
	}{
		{agent: "claude", transcript: claudeTranscript(sharedID) + "null\n"},
		{agent: "codex", transcript: codexTranscript(sharedID) + "null\n"},
	} {
		t.Run(test.agent, func(t *testing.T) {
			path := writeTranscript(t, test.agent+"-null.jsonl", test.transcript)
			_, err := New(Options{}).ProjectFile(context.Background(),
				contracts.SessionIdentity{Agent: test.agent, ID: sharedID}, path)
			if !errors.Is(err, ErrMalformedTranscript) {
				t.Fatalf("error = %v, want ErrMalformedTranscript", err)
			}
		})
	}
}

func TestProjectHonorsTranscriptProjectionRowAndCancellationBounds(t *testing.T) {
	path := writeTranscript(t, sharedID+".jsonl", claudeTranscript(sharedID))
	if _, err := New(Options{MaxTranscriptBytes: 10}).ProjectFile(context.Background(), contracts.SessionIdentity{Agent: "claude", ID: sharedID}, path); !errors.Is(err, ErrTranscriptTooLarge) {
		t.Errorf("transcript bound error = %v", err)
	}
	if _, err := New(Options{MaxRows: 1}).ProjectFile(context.Background(), contracts.SessionIdentity{Agent: "claude", ID: sharedID}, path); !errors.Is(err, ErrTranscriptTooLarge) {
		t.Errorf("row bound error = %v", err)
	}
	if _, err := New(Options{MaxProjectionBytes: 10}).ProjectFile(context.Background(), contracts.SessionIdentity{Agent: "claude", ID: sharedID}, path); !errors.Is(err, ErrProjectionTooLarge) {
		t.Errorf("projection bound error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(Options{}).ProjectFile(cancelled, contracts.SessionIdentity{Agent: "claude", ID: sharedID}, path); !errors.Is(err, context.Canceled) {
		t.Errorf("cancellation error = %v", err)
	}
}

func TestGroupedCommandReadsSplitExactSegments(t *testing.T) {
	reads := parseShellReads("sed -n '1,2p' first.md && sed -n '5,6p' second.md")
	if len(reads) != 2 {
		t.Fatalf("reads = %#v", reads)
	}
	h := newHeavyDetail()
	noteReadOutput(h, "group", pendingRead{Reads: reads, Command: "grouped"}, "one\ntwo\nfive\nsix\n")
	detail := h.finish(emptySession())
	if len(detail.ContextReadGroups) != 0 {
		t.Fatalf("exact grouped output unexpectedly retained fallback group: %#v", detail.ContextReadGroups)
	}
	if len(detail.ContextFiles) != 2 || detail.ContextFiles[0].Segments[0].Content != "one\ntwo" || detail.ContextFiles[1].Segments[0].Content != "five\nsix" {
		t.Fatalf("context files = %#v", detail.ContextFiles)
	}
}

func TestTriggeredContextClassifiesAndCountsToolMCPAndSkill(t *testing.T) {
	h := newHeavyDetail()
	kind, name := triggeredName("mcp__github__search", nil)
	h.addTriggered(kind, name)
	h.addTriggered(kind, name)
	kind, name = triggeredName("Skill", map[string]any{"skill": "review"})
	h.addTriggered(kind, name)
	kind, name = triggeredName("Read", nil)
	h.addTriggered(kind, name)
	detail := h.finish(emptySession())
	want := []TriggeredContext{
		{Kind: "mcp", Name: "github / search", Calls: 2},
		{Kind: "skill", Name: "review", Calls: 1},
		{Kind: "tool", Name: "Read", Calls: 1},
	}
	if fmt.Sprint(detail.TriggeredContext) != fmt.Sprint(want) {
		t.Fatalf("triggered context = %#v, want %#v", detail.TriggeredContext, want)
	}
}

func readGolden(t *testing.T, name string, target any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func writeTranscript(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func jsonLine(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data) + "\n"
}

func claudeTranscript(id string) string {
	base := map[string]any{"sessionId": id, "cwd": "/workspace/canvas-fixture", "gitBranch": "fixture/review-workspace", "entrypoint": "claude-code"}
	row := func(extra map[string]any) string {
		value := map[string]any{}
		for key, item := range base {
			value[key] = item
		}
		for key, item := range extra {
			value[key] = item
		}
		return jsonLine(value)
	}
	patchLines := []string{"@@ -0,0 +1,24 @@"}
	for index := 0; index < 24; index++ {
		patchLines = append(patchLines, fmt.Sprintf("+line %d", index+1))
	}
	return row(map[string]any{"type": "user", "timestamp": "2026-08-08T00:00:00Z", "message": map[string]any{"content": "Create sanitized migration fixtures."}}) +
		row(map[string]any{"type": "assistant", "timestamp": "2026-08-08T00:00:01Z", "message": map[string]any{"id": "m1", "model": "fixture-claude-model", "stop_reason": "tool_use", "content": []any{
			map[string]any{"type": "tool_use", "id": "plan", "name": "ExitPlanMode", "input": map[string]any{"plan": "Inspect schemas, write synthetic records, then validate them."}},
			map[string]any{"type": "tool_use", "id": "todos", "name": "TodoWrite", "input": map[string]any{"todos": []any{map[string]any{"content": "Validate JSON", "status": "completed"}}}},
			map[string]any{"type": "tool_use", "id": "edit", "name": "Edit", "input": map[string]any{"file_path": "docs/plans/canvas-suite/fixtures/README.md"}},
		}}}) +
		row(map[string]any{"type": "user", "timestamp": "2026-08-08T00:00:02Z", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "edit", "content": "done"}}}, "toolUseResult": map[string]any{
			"filePath": "docs/plans/canvas-suite/fixtures/README.md", "type": "create", "structuredPatch": []any{map[string]any{"lines": patchLines}},
			"file":      map[string]any{"filePath": "docs/reference-contract.md", "content": "# Synthetic reference contract\n", "numLines": 12, "startLine": 1, "totalLines": 12},
			"questions": []any{map[string]any{"question": "May fixtures include real paths?"}}, "answers": map[string]any{"May fixtures include real paths?": "No; use /workspace/canvas-fixture."},
		}}) +
		row(map[string]any{"type": "user", "timestamp": "2026-08-08T00:00:03Z", "message": map[string]any{"content": "Verify the package."}}) +
		row(map[string]any{"type": "assistant", "timestamp": "2026-08-08T00:00:04Z", "message": map[string]any{"id": "m2", "model": "fixture-claude-model", "stop_reason": "tool_use", "content": []any{
			map[string]any{"type": "tool_use", "id": "edit2", "name": "Edit", "input": map[string]any{"file_path": "docs/plans/canvas-suite/fixtures/characterization.md"}},
		}}})
}

func codexTranscript(id string) string {
	lines := make([]string, 64)
	for index := range lines {
		lines[index] = fmt.Sprintf("line %d", index+1)
	}
	planArgs := jsonLine(map[string]any{"explanation": "Build the payload, run validation, and report the blocked browser capture.", "plan": []any{map[string]any{"step": "Run build", "status": "completed"}}})
	questionArgs := jsonLine(map[string]any{"questions": []any{map[string]any{"question": "Can the screenshot requirement be inferred?"}}})
	return jsonLine(map[string]any{"timestamp": "2026-08-08T00:00:00Z", "type": "session_meta", "payload": map[string]any{"id": id, "cwd": "/workspace/canvas-fixture", "originator": "codex-tui", "git": map[string]any{"branch": "fixture/implementation-workspace"}}}) +
		jsonLine(map[string]any{"timestamp": "2026-08-08T00:00:01Z", "type": "event_msg", "payload": map[string]any{"type": "user_message", "message": "Implement the fixture using only synthetic inputs."}}) +
		jsonLine(map[string]any{"timestamp": "2026-08-08T00:00:02Z", "type": "turn_context", "payload": map[string]any{"model": "fixture-codex-model"}}) +
		jsonLine(map[string]any{"timestamp": "2026-08-08T00:00:03Z", "type": "response_item", "payload": map[string]any{"type": "function_call", "name": "update_plan", "call_id": "plan", "arguments": strings.TrimSpace(planArgs)}}) +
		jsonLine(map[string]any{"timestamp": "2026-08-08T00:00:04Z", "type": "response_item", "payload": map[string]any{"type": "function_call", "name": "exec_command", "call_id": "read-fixture-1", "arguments": `{"cmd":"sed -n '5,8p' docs/reference-contract.md"}`}}) +
		jsonLine(map[string]any{"timestamp": "2026-08-08T00:00:05Z", "type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "read-fixture-1", "output": "Use composite session identity.\n"}}) +
		jsonLine(map[string]any{"timestamp": "2026-08-08T00:00:06Z", "type": "response_item", "payload": map[string]any{"type": "function_call", "name": "skills.read", "call_id": "skill-fixture", "arguments": `{"uri":"skill://fixture-validation"}`}}) +
		jsonLine(map[string]any{"timestamp": "2026-08-08T00:00:07Z", "type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "skill-fixture", "output": "Validate only synthetic payloads."}}) +
		jsonLine(map[string]any{"timestamp": "2026-08-08T00:00:08Z", "type": "response_item", "payload": map[string]any{"type": "function_call", "name": "request_user_input", "call_id": "question", "arguments": strings.TrimSpace(questionArgs)}}) +
		jsonLine(map[string]any{"timestamp": "2026-08-08T00:00:09Z", "type": "event_msg", "payload": map[string]any{"type": "patch_apply_end", "changes": map[string]any{"docs/plans/canvas-suite/fixtures/session/codex-detail.json": map[string]any{"type": "add", "content": strings.Join(lines, "\n")}}}})
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func containsDecision(values []Decision, question string) bool {
	for _, value := range values {
		if value.Question == question {
			return true
		}
	}
	return false
}
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func emptySession() session.Session {
	return session.Session{Agent: "claude", ID: "test"}
}

func equalDecisions(left, right []Decision) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Question != right[index].Question || pointerValue(left[index].Answer) != pointerValue(right[index].Answer) {
			return false
		}
	}
	return true
}
