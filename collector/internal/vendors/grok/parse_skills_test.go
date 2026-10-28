package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func skillReadCall(id, path string) string {
	quoted, _ := json.Marshal(path)
	return `{"params":{"update":{"sessionUpdate":"tool_call","toolCallId":"` + id + `","title":"read_file","rawInput":{"target_file":` + string(quoted) + `}}}}`
}

func skillReadResult(id, status string, fields map[string]any) string {
	body, _ := json.Marshal(map[string]any{"params": map[string]any{"update": map[string]any{
		"sessionUpdate": "tool_call_update", "toolCallId": id, "status": status,
		"rawOutput": map[string]any{"type": "ReadFile", "FileContent": fields},
	}}})
	return string(body)
}

func nativeSkillCall(id, name string) string {
	quoted, _ := json.Marshal(name)
	return `{"params":{"update":{"sessionUpdate":"tool_call","toolCallId":"` + id + `","title":"skill","rawInput":{"skill":` + string(quoted) + `}}}}`
}

func nativeSkillResult(id, status string, fields map[string]any) string {
	body, _ := json.Marshal(map[string]any{"params": map[string]any{"update": map[string]any{
		"sessionUpdate": "tool_call_update", "toolCallId": id, "status": status, "rawOutput": fields,
	}}})
	return string(body)
}

func TestNativeSkillsRetainCapturedNamesAndMessageVariants(t *testing.T) {
	body := "<skill name=\"demo\">\n  Captured **instructions**.\n</skill>\n"
	var rows []string
	for _, content := range []string{body, body, "changed\n"} {
		rows = append(rows, nativeSkillCall("a", "user:demo"), nativeSkillResult("a", "completed", map[string]any{
			"type": "Skill", "success": true, "skill_name": "demo", "skill_message": content,
			"tool_result": "Loaded demo", "error": nil,
		}))
	}
	rows = append(rows, skillReadCall("b", "/skills/demo/SKILL.md"), skillReadResult("b", "completed", map[string]any{"raw_output": body}))
	s, _ := parseOutputSession(t, rows...)
	want := []session.Skill{{Name: "demo", Content: body}, {Name: "demo", Content: "changed\n"}}
	if !reflect.DeepEqual(s.Skills, want) {
		t.Fatalf("skills = %#v, want %#v", s.Skills, want)
	}
}

func TestNativeSkillsRequireSuccessfulCompletedResults(t *testing.T) {
	for _, test := range []struct {
		name, status string
		fields       map[string]any
	}{
		{"failed resolution", "completed", map[string]any{"type": "Skill", "success": false, "skill_name": "demo", "skill_message": "not loaded", "tool_result": "error"}},
		{"unknown success", "completed", map[string]any{"type": "Skill", "skill_name": "demo", "tool_result": "error"}},
		{"failed call", "failed", map[string]any{"type": "Skill", "success": true, "skill_name": "demo", "skill_message": "body"}},
		{"cancelled call", "cancelled", map[string]any{"type": "Skill", "success": true, "skill_name": "demo"}},
		{"running call", "in_progress", map[string]any{"type": "Skill", "success": true, "skill_name": "demo"}},
		{"wrong variant", "completed", map[string]any{"type": "ListDir", "success": true, "skill_name": "demo", "skill_message": "body"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _ := parseOutputSession(t, nativeSkillCall("a", "demo"), nativeSkillResult("a", test.status, test.fields))
			if s.Skills != nil {
				t.Fatalf("unexpected skills: %#v", s.Skills)
			}
		})
	}
}

func TestNativeSkillsKeepUnknownContentAndFallbackNames(t *testing.T) {
	for _, test := range []struct {
		name, input, output string
		message             any
		want                []session.Skill
	}{
		{"null message", "user:demo", "demo", nil, []session.Skill{{Name: "demo", Content: ""}}},
		{"missing message", "user:demo", "demo", nil, []session.Skill{{Name: "demo", Content: ""}}},
		{"empty message", "user:demo", "demo", "", []session.Skill{{Name: "demo", Content: ""}}},
		{"input name", "user:demo", "", "body", []session.Skill{{Name: "user:demo", Content: "body"}}},
		{"unknown name", "", "", "body", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields := map[string]any{
				"type": "Skill", "success": true, "skill_name": test.output, "skill_message": test.message,
				"tool_result": "fallback result is not instructions",
			}
			if test.name == "missing message" {
				delete(fields, "skill_message")
			}
			s, _ := parseOutputSession(t, nativeSkillCall("a", test.input), nativeSkillResult("a", "completed", fields))
			if !reflect.DeepEqual(s.Skills, test.want) {
				t.Fatalf("skills = %#v, want %#v", s.Skills, test.want)
			}
		})
	}
}

func TestSkillsRetainObservedBodiesInCompletionOrder(t *testing.T) {
	body := "---\nname: demo\n---\n\n  Keep **formatting**.\n"
	path := "/historical/skills/demo/SKILL.md"
	rows := []string{skillReadCall("a", path), skillReadCall("b", path)}
	for _, read := range []struct{ id, body string }{{"b", body}, {"a", "changed\n"}, {"c", body}} {
		if read.id == "c" {
			rows = append(rows, skillReadCall(read.id, path))
		}
		rows = append(rows, skillReadResult(read.id, "completed", map[string]any{
			"absolute_path": path, "content": "1→formatted projection", "raw_output": read.body,
			"offset": nil, "total_lines": 6,
		}))
	}
	s, _ := parseOutputSession(t, rows...)
	want := []session.Skill{{Name: "demo", Content: body}, {Name: "demo", Content: "changed\n"}}
	if !reflect.DeepEqual(s.Skills, want) {
		t.Fatalf("skills = %#v, want %#v", s.Skills, want)
	}
}

func TestSkillsRequireSuccessfulMatchingReads(t *testing.T) {
	path := "/skills/demo/SKILL.md"
	for _, test := range []struct {
		name string
		rows []string
	}{
		{"unfinished", []string{skillReadCall("a", path)}},
		{"failed", []string{skillReadCall("a", path), skillReadResult("a", "failed", map[string]any{"raw_output": "error"})}},
		{"cancelled", []string{skillReadCall("a", path), skillReadResult("a", "cancelled", map[string]any{"raw_output": "partial"})}},
		{"running", []string{skillReadCall("a", path), skillReadResult("a", "in_progress", map[string]any{"raw_output": "partial"})}},
		{"unmatched", []string{skillReadCall("a", path), skillReadResult("b", "completed", map[string]any{"absolute_path": path, "raw_output": "text"})}},
		{"missing id", []string{skillReadCall("", path), skillReadResult("", "completed", map[string]any{"absolute_path": path})}},
		{"ordinary file", []string{skillReadCall("a", "/skills/demo/README.md"), skillReadResult("a", "completed", map[string]any{"absolute_path": "/skills/demo/README.md", "raw_output": path})}},
		{"different result file", []string{skillReadCall("a", path), skillReadResult("a", "completed", map[string]any{"absolute_path": "/skills/demo/README.md", "raw_output": "text"})}},
		{"listing", []string{strings.ReplaceAll(skillReadCall("a", path), "read_file", "list_directory"), skillReadResult("a", "completed", map[string]any{"absolute_path": path})}},
		{"write", []string{strings.ReplaceAll(skillReadCall("a", path), "read_file", "write_file"), skillReadResult("a", "completed", map[string]any{"absolute_path": path})}},
		{"mention", []string{`{"params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Read /skills/demo/SKILL.md"}}}}`}},
		{"read error variant", []string{skillReadCall("a", path), `{"params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"a","status":"completed","rawOutput":{"type":"ReadFile","FileNotFound":"missing"}}}}`}},
		{"wrong output type", []string{skillReadCall("a", path), strings.ReplaceAll(skillReadResult("a", "completed", map[string]any{"absolute_path": path}), "ReadFile", "ListDir")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _ := parseOutputSession(t, test.rows...)
			if s.Skills != nil {
				t.Fatalf("unexpected skills: %#v", s.Skills)
			}
		})
	}
}

func TestSkillsIgnoreUnsuccessfulShellResults(t *testing.T) {
	completed := shellResult("a", "cat /skills/demo/SKILL.md", "partial", 0)
	for _, row := range []string{
		strings.ReplaceAll(completed, `"timed_out":false`, `"timed_out":true`),
		strings.ReplaceAll(completed, `"exit_code":0`, `"exit_code":null`),
		strings.ReplaceAll(completed, `"status":"completed"`, `"status":"failed"`),
	} {
		s, _ := parseOutputSession(t, row)
		if s.Skills != nil {
			t.Fatalf("unsuccessful read produced skills: %#v", s.Skills)
		}
	}
}

func TestSkillsKeepCapturedFallbacksAndPartialReads(t *testing.T) {
	for _, test := range []struct {
		name   string
		fields map[string]any
		want   string
	}{
		{"content", map[string]any{"content": "1→  body\n"}, "1→  body\n"},
		{"concise", map[string]any{"content_concise": "1→body\n"}, "1→body\n"},
		{"empty raw", map[string]any{"raw_output": "", "content": "(empty file)"}, ""},
		{"unknown", map[string]any{}, ""},
		{"partial", map[string]any{"raw_output": "  observed section\n", "offset": 12, "total_lines": 40}, "  observed section\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _ := parseOutputSession(t, skillReadCall("a", `C:\skills\demo\SKILL.md`), skillReadResult("a", "completed", test.fields))
			want := []session.Skill{{Name: "demo", Content: test.want}}
			if !reflect.DeepEqual(s.Skills, want) {
				t.Fatalf("skills = %#v, want %#v", s.Skills, want)
			}
		})
	}
}

func TestSkillsFromUnambiguousSuccessfulShellReads(t *testing.T) {
	for _, test := range []struct {
		command string
		exit    int
		want    bool
	}{
		{"cat /skills/demo/SKILL.md", 0, true},
		{"cat -- /skills/demo/SKILL.md", 0, true},
		{"cat /skills/demo/SKILL.md", 1, false},
		{"ls /skills/demo/SKILL.md", 0, false},
		{"echo /skills/demo/SKILL.md", 0, false},
		{"cat /skills/demo/README.md", 0, false},
		{"cat /skills/demo/SKILL.md /other", 0, false},
		{"cat /skills/demo/SKILL.md > /other", 0, false},
		{"cat /skills/$NAME/SKILL.md", 0, false},
		{"cat /skills/demo/SKILL.md;", 0, false},
	} {
		t.Run(test.command+"/exit="+strconv.Itoa(test.exit), func(t *testing.T) {
			s, _ := parseOutputSession(t, shellResult("a", test.command, "# body\n", test.exit))
			var want []session.Skill
			if test.want {
				want = []session.Skill{{Name: "demo", Content: "# body\n"}}
			}
			if !reflect.DeepEqual(s.Skills, want) {
				t.Fatalf("skills = %#v, want %#v", s.Skills, want)
			}
		})
	}
}

func TestFrozenFamilySkillsBelongToEachParsedSession(t *testing.T) {
	live := t.TempDir()
	parent, child := filepath.Join(live, "parent"), filepath.Join(live, "child")
	writeSummary(t, parent, `{"info":{"id":"parent"},"chat_format_version":1}`)
	writeSummary(t, child, `{"info":{"id":"child"},"chat_format_version":1,"session_kind":"subagent","parent_session_id":"parent"}`)
	for _, dir := range []string{parent, child} {
		name := filepath.Base(dir)
		path := filepath.Join(live, "skills", name, "SKILL.md")
		rows := []string{skillReadCall("a", path), skillReadResult("a", "completed", map[string]any{"absolute_path": path, "raw_output": name + " instructions\n"})}
		rows = append(rows, nativeSkillCall("b", "native-"+name), nativeSkillResult("b", "completed", map[string]any{
			"type": "Skill", "success": true, "skill_name": "native-" + name, "skill_message": "native instructions\n",
		}))
		if err := os.WriteFile(filepath.Join(dir, "updates.jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	refs, err := PlanFamiliesContext(t.Context(), []string{parent, child})
	if err != nil || len(refs) != 1 {
		t.Fatalf("plan = %#v, %v", refs, err)
	}
	frozen := t.TempDir()
	var dirs []string
	for _, dir := range refs[0].Dirs {
		dest := filepath.Join(frozen, filepath.Base(dir))
		if err := os.CopyFS(dest, os.DirFS(dir)); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dest)
	}
	if err := os.RemoveAll(live); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseFrozenFamilyContext(t.Context(), refs[0], dirs, nil)
	if err != nil || len(parsed) != 2 {
		t.Fatalf("frozen family = %#v, %v", parsed, err)
	}
	for _, item := range parsed {
		name := item.Session.ID
		want := []session.Skill{{Name: name, Content: name + " instructions\n"}, {Name: "native-" + name, Content: "native instructions\n"}}
		if !reflect.DeepEqual(item.Session.Skills, want) {
			t.Fatalf("%s skills = %#v, want %#v", name, item.Session.Skills, want)
		}
	}
}
