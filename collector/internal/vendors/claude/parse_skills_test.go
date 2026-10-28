package claude

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func skillJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func skillUse(t *testing.T, id, name string, input map[string]any) string {
	t.Helper()
	return skillJSON(t, map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}}}})
}

func skillResult(t *testing.T, id, content string, failed bool, result any) string {
	t.Helper()
	return skillJSON(t, map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": content, "is_error": failed}}}, "toolUseResult": result})
}

func skillMeta(t *testing.T, id, body string) string {
	t.Helper()
	return skillJSON(t, map[string]any{"type": "user", "isMeta": true, "sourceToolUseID": id, "message": map[string]any{"content": "Base directory for this skill: /unavailable/skills/review\n\n" + body}})
}

func assertSkills(t *testing.T, value *session.Session, want []session.Skill) {
	t.Helper()
	if !reflect.DeepEqual(value.Skills, want) {
		t.Fatalf("skills = %#v, want %#v", value.Skills, want)
	}
}

func TestSkillsNativeCapturedBodies(t *testing.T) {
	body := "# Review\n\n  Keep formatting.  \n"
	rows := []string{`{"type":"user","message":{"content":"review this"}}`}
	for _, call := range []struct{ id, body string }{{"a", body}, {"b", body}, {"c", "# Changed\n"}} {
		rows = append(rows, skillUse(t, call.id, "Skill", map[string]any{"skill": "plugin:review", "args": "diff"}), skillResult(t, call.id, "Launching skill: plugin:review", false, map[string]any{"success": true, "commandName": "plugin:review"}), skillMeta(t, call.id, call.body))
	}
	rows = append(rows, skillUse(t, "unknown", "Skill", map[string]any{"skill": "unknown"}), skillResult(t, "unknown", "Launching skill: unknown", false, map[string]any{"success": true, "commandName": "unknown"}))
	path := filepath.Join(t.TempDir(), "11111111-1111-1111-1111-111111111111.jsonl")
	writeOutputTranscript(t, path, bashRows(rows...))
	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	assertSkills(t, parsed.Session, []session.Skill{{Name: "plugin:review", Content: body}, {Name: "plugin:review", Content: "# Changed\n"}, {Name: "unknown"}})
	if parsed.Session.ToolUses != 4 || parsed.Session.FirstPrompt == nil || *parsed.Session.FirstPrompt != "review this" {
		t.Fatal("lost prompt/tool behavior")
	}
}

func TestSkillsIgnoreUnsuccessfulAndNonLoads(t *testing.T) {
	rows := []string{`{"type":"user","message":{"content":"Use plugin:review and read SKILL.md"}}`,
		skillUse(t, "failed", "Skill", map[string]any{"skill": "failed"}), skillResult(t, "failed", "failed", false, map[string]any{"success": false, "commandName": "failed"}), skillMeta(t, "failed", "bad"),
		skillUse(t, "error", "Skill", map[string]any{"skill": "error"}), skillResult(t, "error", "denied", true, nil),
		skillUse(t, "pending", "Skill", map[string]any{"skill": "pending"}),
		skillUse(t, "list", "Bash", map[string]any{"command": "ls /skills/review/SKILL.md"}), skillResult(t, "list", "/skills/review/SKILL.md", false, nil),
		skillUse(t, "echo", "Bash", map[string]any{"command": "echo cat /skills/review/SKILL.md"}), skillResult(t, "echo", "cat /skills/review/SKILL.md", false, nil),
		skillUse(t, "write", "Write", map[string]any{"file_path": "/skills/review/SKILL.md"}), skillResult(t, "write", "written", false, nil),
		skillUse(t, "read-error", "Read", map[string]any{"file_path": "/skills/review/SKILL.md"}), skillResult(t, "read-error", "denied", true, nil),
		skillUse(t, "ordinary", "Read", map[string]any{"file_path": "/skills/review/README.md"}), skillResult(t, "ordinary", "not a skill", false, nil), skillMeta(t, "unmatched", "not loaded"),
	}
	path := filepath.Join(t.TempDir(), "11111111-1111-1111-1111-111111111111.jsonl")
	writeOutputTranscript(t, path, bashRows(rows...))
	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	assertSkills(t, parsed.Session, nil)
}

func TestSkillsSlashAndForkedChild(t *testing.T) {
	for _, child := range []bool{false, true} {
		t.Run(map[bool]string{false: "slash", true: "forked-child"}[child], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "11111111-1111-1111-1111-111111111111.jsonl")
			rows := []string{`{"type":"user","message":{"content":"<command-name>/plugin:review</command-name><command-args>diff</command-args>"}}`}
			if child {
				path = filepath.Join(t.TempDir(), "11111111-1111-1111-1111-111111111111", "subagents", "agent-skill.jsonl")
				rows = nil
			}
			rows = append(rows, skillMeta(t, "", "# Instructions\n"))
			writeOutputTranscript(t, path, bashRows(rows...))
			parsed, err := parseTranscript(path)
			if err != nil {
				t.Fatal(err)
			}
			name := "plugin:review"
			if child {
				name = "review"
				if parsed.ParentID != "11111111-1111-1111-1111-111111111111" || !parsed.InTurn {
					t.Fatal("lost child lineage/turn")
				}
			} else if parsed.Session.FirstPrompt == nil || *parsed.Session.FirstPrompt != "/plugin:review diff" {
				t.Fatal("lost slash prompt")
			}
			assertSkills(t, parsed.Session, []session.Skill{{Name: name, Content: "# Instructions\n"}})
		})
	}
}

func TestSkillsCapturedFileReads(t *testing.T) {
	body := "---\nname: review\n---\n\n# Review\n  exact  \n"
	rows := bashRows(
		skillUse(t, "read", "Read", map[string]any{"file_path": "/unavailable/skills/review/SKILL.md"}), skillResult(t, "read", "     1→---\n     2→name: review", false, map[string]any{"type": "text", "file": map[string]any{"filePath": "/unavailable/skills/review/SKILL.md", "content": body}}),
		skillUse(t, "bash", "Bash", map[string]any{"command": "cat '/unavailable/skills/review/SKILL.md'"}), skillResult(t, "bash", body, false, nil),
		skillUse(t, "changed", "Bash", map[string]any{"command": "cat -- /unavailable/skills/review/SKILL.md"}), skillResult(t, "changed", "# Changed\n", false, nil),
	)
	path := filepath.Join(t.TempDir(), "11111111-1111-1111-1111-111111111111.jsonl")
	writeOutputTranscript(t, path, rows)
	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	assertSkills(t, parsed.Session, []session.Skill{{Name: "review", Content: body}, {Name: "review", Content: "# Changed\n"}})
	if len(parsed.Session.CommandOutputs) != 2 || *parsed.Session.CommandOutputs[0] != body {
		t.Fatal("lost command output pairing")
	}
}

func TestSkillsReadBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, tool   string
		input        map[string]any
		output, want string
	}{
		{"numbered-read", "Read", map[string]any{"file_path": `C:\skills\review\SKILL.md`}, "     1→# Review\n     2→\n     3→  indented  \n\n<system-reminder>read wrapper</system-reminder>", "# Review\n\n  indented  \n"},
		{"bash-keeps-numbers", "Bash", map[string]any{"command": `cat "/skills/review/SKILL.md"`}, "1→literal\n", "1→literal\n"},
		{"persisted-body-unknown", "Read", map[string]any{"file_path": "/skills/review/SKILL.md"}, "<persisted-output>\nnot instruction content\n</persisted-output>", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "11111111-1111-1111-1111-111111111111.jsonl")
			writeOutputTranscript(t, path, bashRows(skillUse(t, "read", test.tool, test.input), skillResult(t, "read", test.output, false, nil)))
			parsed, err := parseTranscript(path)
			if err != nil {
				t.Fatal(err)
			}
			assertSkills(t, parsed.Session, []session.Skill{{Name: "review", Content: test.want}})
		})
	}
}

type skillBackupSource struct{ fstest.MapFS }

func (source skillBackupSource) Open(name string) (io.ReadCloser, error) {
	return source.MapFS.Open(name)
}

func TestSkillsBackupFamilyAndCache(t *testing.T) {
	root := "11111111-1111-1111-1111-111111111111"
	path := root + ".jsonl"
	child := root + "/subagents/agent-skill.jsonl"
	rows := bashRows(skillUse(t, "load", "Skill", map[string]any{"skill": "review"}), skillResult(t, "load", "Launching skill: review", false, map[string]any{"success": true}), skillMeta(t, "load", "# Frozen\n"))
	source := skillBackupSource{fstest.MapFS{
		path:  &fstest.MapFile{Data: []byte(rows)},
		child: &fstest.MapFile{Data: []byte(bashRows(skillMeta(t, "", "# Child\n")))},
	}}
	if _, err := source.Stat("unavailable/skills/review/SKILL.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("fixture unexpectedly has live skill files")
	}
	parsed, err := ParseFamilyFilesSourceContext(t.Context(), source, []string{path, child})
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 2 || parsed[1].ParentID != root {
		t.Fatalf("lost family: %+v", parsed)
	}
	assertSkills(t, parsed[0].Session, []session.Skill{{Name: "review", Content: "# Frozen\n"}})
	assertSkills(t, parsed[1].Session, []session.Skill{{Name: "review", Content: "# Child\n"}})
	for _, file := range []string{path, child} {
		original, err := parseSource(source, file)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := encodeCached(original)
		if err != nil {
			t.Fatal(err)
		}
		restored, err := decodeCached(payload)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(original.transcript.Session.Skills, restored.transcript.Session.Skills) {
			t.Fatal("cache lost captured skills")
		}
	}
}
