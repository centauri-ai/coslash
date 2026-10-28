package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"testing"
)

type capturedSkill struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

func skillMessage(role, text string) string {
	return string(mustJSON(map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": role, "content": []map[string]string{{"type": "input_text", "text": text}}}}))
}

func TestExplicitSkillsPreserveHistory(t *testing.T) {
	body := "# Instructions\n\n  preserve spaces\n"
	load := "<skill>\n<name>example</name>\n<path>/historical/example/SKILL.md</path>\n" + body + "\n</skill>"
	changed := "<skill>\n<name>example</name>\n<path>/historical/example/SKILL.md</path>\nchanged\n</skill>"
	parsed := parseLifecycleRows(t, []string{skillMessage("user", load), skillMessage("user", load), skillMessage("user", changed)})
	assertCapturedSkills(t, parsed.Session, []capturedSkill{{"example", body}, {"example", "changed"}})
	if parsed.ParentID == "" {
		t.Fatal("fixture must exercise child parsing")
	}
}

func assertCapturedSkills(t *testing.T, value any, want []capturedSkill) {
	t.Helper()
	var got struct {
		Skills []capturedSkill `json:"skills"`
	}
	if err := json.Unmarshal(mustJSON(value), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Skills, want) {
		t.Fatalf("skills = %#v; want %#v", got.Skills, want)
	}
}

func TestSkillReadsRequireSuccessfulEvidence(t *testing.T) {
	for _, tt := range []struct {
		name, command string
		output        any
		want          []capturedSkill
	}{
		{"cat", "cat '/old/example/SKILL.md'", `{"output":"  historical\n\n","exit_code":0}`, []capturedSkill{{"example", "  historical\n\n"}}},
		{"sed", "sed -n '1,200p' /old/example/SKILL.md", `{"output":"partial\n","metadata":{"exit_code":0}}`, []capturedSkill{{"example", "partial\n"}}},
		{"empty", "cat /old/example/SKILL.md", `{"output":"","exit_code":0}`, []capturedSkill{{"example", ""}}},
		{"failed", "cat /old/example/SKILL.md", `{"output":"error","exit_code":1}`, nil},
		{"native text", "cat /old/example/SKILL.md", "Chunk ID: test\nWall time: 0.1 seconds\nProcess exited with code 0\nFinal output:\n  body\n", []capturedSkill{{"example", "  body\n"}}},
		{"unknown status", "cat /old/example/SKILL.md", "looks like instructions", nil},
		{"listing", "ls /old/example/SKILL.md", `{"output":"SKILL.md","exit_code":0}`, nil},
		{"mention", "echo /old/example/SKILL.md", `{"output":"SKILL.md","exit_code":0}`, nil},
		{"write", "cat > /old/example/SKILL.md", `{"output":"","exit_code":0}`, nil},
		{"reference", "cat /old/example/references/readme.md", `{"output":"reference","exit_code":0}`, nil},
		{"compound", "cat /old/example/SKILL.md; echo extra", `{"output":"mixed","exit_code":0}`, nil},
		{"substitution", "cat \"$(echo /old/example/SKILL.md)\"", `{"output":"mixed","exit_code":0}`, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parseLifecycleRows(t, []string{execCall("function_call", "read", tt.command), execResult("function_call_output", "read", tt.output)})
			assertCapturedSkills(t, parsed.Session, tt.want)
		})
	}
}

func TestSkillCatalogMentionsAndUnfinishedReadsDoNotCount(t *testing.T) {
	load := "<skill>\n<name>example</name>\n<path>/old/example/SKILL.md</path>\nbody\n</skill>"
	parsed := parseLifecycleRows(t, []string{
		skillMessage("user", "<skill_instructions>"+load+"</skill_instructions>"),
		skillMessage("assistant", load), skillMessage("user", "Please read /old/example/SKILL.md"),
		execCall("function_call", "unfinished", "cat /old/example/SKILL.md"),
		execResult("function_call_output", "unmatched", `{"output":"body","exit_code":0}`),
	})
	assertCapturedSkills(t, parsed.Session, nil)
}

func TestCodeModeSkillsPairAllCommands(t *testing.T) {
	script := "// @exec: {}\n" + `text(await tools.exec_command({cmd: "cat /old/first/SKILL.md", max_output_tokens: 4000})); text(await tools.exec_command({"cmd": "cat /old/second/SKILL.md", "workdir": "/old"})); text(await tools.exec_command({cmd: "cat /old/failed/SKILL.md"}));`
	call := string(mustJSON(map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "code", "input": script}}))
	blocks := []map[string]string{
		{"text": "Script completed\nOutput:\n"},
		{"text": `{"output":"first body\n","exit_code":0}`},
		{"text": `{"output":"second body","exit_code":0}`},
		{"text": `{"output":"failed body","exit_code":1}`},
	}
	parsed := parseLifecycleRows(t, []string{call, execResult("custom_tool_call_output", "code", blocks)})
	assertCapturedSkills(t, parsed.Session, []capturedSkill{{"first", "first body\n"}, {"second", "second body"}})
	parsed = parseLifecycleRows(t, []string{call, execResult("custom_tool_call_output", "code", blocks[:2])})
	assertCapturedSkills(t, parsed.Session, nil)
}

func TestNativeSkillResourceRead(t *testing.T) {
	for _, tt := range []struct {
		resource string
		failed   bool
		want     []capturedSkill
	}{
		{"skill://package/example/SKILL.md", false, []capturedSkill{{"example", "# Main\n"}}},
		{"skill://package/example/references/guide.md", false, nil},
		{"skill://package/example/SKILL.md", true, nil},
	} {
		call := string(mustJSON(map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "skills.read", "call_id": "native", "arguments": map[string]string{"resource": tt.resource}}}))
		output := map[string]any{"isError": tt.failed, "content": []map[string]string{{"type": "text", "text": "# Main\n"}}}
		parsed := parseLifecycleRows(t, []string{call, execResult("function_call_output", "native", string(mustJSON(output)))})
		assertCapturedSkills(t, parsed.Session, tt.want)
	}
}

func TestSkillBackupFamilyAndCache(t *testing.T) {
	home := t.TempDir()
	id := "11111111-2222-3333-4444-555555555555"
	file := writeFamilyTestRollout(t, home, id, id)
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	rows := []string{execCall("function_call", "load", "cat /unavailable/example/SKILL.md"), execResult("function_call_output", "load", `{"output":"backup body","exit_code":0}`)}
	_, err = f.WriteString(strings.Join(rows, "\n") + "\n")
	closeErr := f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	archived := filepath.Join(home, ".codex", "archived_sessions", filepath.Base(file))
	if err := os.MkdirAll(filepath.Dir(archived), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(file, archived); err != nil {
		t.Fatal(err)
	}
	parsed, failures, err := ParseRemoteFiles(vendors.LocalReadSource, home, []string{archived}, nil)
	if err != nil || len(failures) > 0 || len(parsed) != 1 {
		t.Fatalf("backup parse: %v, %v, %d", err, failures, len(parsed))
	}
	want := []capturedSkill{{"example", "backup body"}}
	assertCapturedSkills(t, parsed[0].Session, want)
	source, err := parseSource(vendors.LocalReadSource, archived, func(string, string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeCached(source)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeCached(encoded)
	if err != nil {
		t.Fatal(err)
	}
	assertCapturedSkills(t, decoded.transcript.Session, want)
}

func TestCodeModeSkillsSkipAmbiguousScripts(t *testing.T) {
	for _, script := range []string{
		`const first = await tools.exec_command({cmd: "cat /old/first/SKILL.md"}); const second = await tools.exec_command({cmd: "cat /old/second/SKILL.md"}); text(second); text(first);`,
		`const example = {cmd: "cat /old/phantom/SKILL.md"}; text({exit_code: 0, output: "status only"});`,
		`if (false) text(await tools.exec_command({cmd: "cat /old/phantom/SKILL.md"})); text({exit_code: 0, output: "status only"});`,
		`text(await tools.exec_command({cmd: "cat /old/phantom/SKILL.md", cmd: "echo status"}));`,
		`text(await tools.exec_command({cmd: "cat /old/phantom/SKILL.md"})); text({exit_code: 0, output: "extra"});`,
	} {
		t.Run(script, func(t *testing.T) {
			call := string(mustJSON(map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "code", "input": script}}))
			blocks := []map[string]string{{"text": "Script completed\nOutput:\n"}, {"text": `{"output":"status only","exit_code":0}`}}
			if strings.HasPrefix(script, "const first") {
				blocks = append(blocks, map[string]string{"text": `{"output":"other body","exit_code":0}`})
			}
			parsed := parseLifecycleRows(t, []string{call, execResult("custom_tool_call_output", "code", blocks)})
			assertCapturedSkills(t, parsed.Session, nil)
		})
	}
}
