package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func execCall(kind, id, cmd string) string {
	args, _ := json.Marshal(map[string]string{"cmd": cmd})
	if kind == "custom_tool_call" {
		return `{"timestamp":"2026-10-23T10:00:01Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"` + id + `","name":"exec","input":` + string(mustJSON(`tools.exec_command({cmd: `+string(mustJSON(cmd))+`})`)) + `}}`
	}
	return `{"timestamp":"2026-10-23T10:00:01Z","type":"response_item","payload":{"type":"function_call","call_id":"` + id + `","name":"exec_command","arguments":` + string(mustJSON(string(args))) + `}}`
}

func execResult(kind, id string, output any) string {
	return `{"timestamp":"2026-10-23T10:00:02Z","type":"response_item","payload":{"type":"` + kind + `","call_id":"` + id + `","output":` + string(mustJSON(output)) + `}}`
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func ptr(s string) *string { return &s }

func wantOutputs(t *testing.T, got, want []*string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("outputs = %v, want %v", got, want)
	}
	for i, w := range want {
		if (w == nil) != (got[i] == nil) || (w != nil && *w != *got[i]) {
			t.Fatalf("output %d = %v, want %v", i, got[i], w)
		}
	}
}

func TestCommandOutputsPairByCallID(t *testing.T) {
	long := strings.Repeat("x", session.CommandOutputLimit*2)
	parsed := parseLifecycleRows(t, []string{
		execCall("function_call", "a", "go test"),
		execCall("function_call", "b", "sleep 9"),
		execCall("function_call", "c", "false"),
		execCall("function_call", "d", "true"),
		execCall("function_call", "e", "cat big"),
		execCall("custom_tool_call", "f", "ls"),
		execResult("function_call_output", "d", ""),
		execResult("function_call_output", "a", "PASS\n"),
		execResult("function_call_output", "c", `{"output":"boom","metadata":{"exit_code":1}}`),
		execResult("function_call_output", "e", long),
		execResult("custom_tool_call_output", "f", []map[string]string{{"type": "input_text", "text": "Script completed"}, {"type": "input_text", "text": "a.txt"}}),
	})
	if len(parsed.Session.Commands) != 6 {
		t.Fatalf("commands = %v", parsed.Session.Commands)
	}
	wantOutputs(t, parsed.Session.CommandOutputs, []*string{
		ptr("PASS\n"), nil, ptr("boom"), ptr(""), ptr(session.TruncateOutput(long)), ptr("Script completed\na.txt"),
	})
	if len(*parsed.Session.CommandOutputs[4]) >= len(long) {
		t.Fatal("long output was not truncated")
	}
	for i, c := range parsed.Commands {
		if (c.Output == nil) != (parsed.Session.CommandOutputs[i] == nil) {
			t.Fatalf("labelled command %d output drifted from CommandOutputs", i)
		}
	}
}

func TestEmptyOutputInWrapperStaysEmpty(t *testing.T) {
	parsed := parseLifecycleRows(t, []string{
		execCall("function_call", "a", "true"),
		execResult("function_call_output", "a", `{"output":"","metadata":{"exit_code":0}}`),
	})
	wantOutputs(t, parsed.Session.CommandOutputs, []*string{ptr("")})
}

func TestSubagentCommandsCarryOutputs(t *testing.T) {
	parsed := parseLifecycleRows(t, []string{
		execCall("function_call", "a", "git status"),
		execResult("function_call_output", "a", "clean"),
	})
	if parsed.ParentID == "" || len(parsed.Commands) != 1 || parsed.Commands[0].Output == nil || *parsed.Commands[0].Output != "clean" {
		t.Fatalf("subagent commands = %+v", parsed.Commands)
	}
}

func TestSessionWithoutResultsStillParses(t *testing.T) {
	parsed := parseLifecycleRows(t, []string{execCall("function_call", "a", "ls")})
	if len(parsed.Session.Commands) != 1 {
		t.Fatalf("commands = %v", parsed.Session.Commands)
	}
	wantOutputs(t, parsed.Session.CommandOutputs, []*string{nil})
}

func TestPullRequestLinksFromSuccessfulCreates(t *testing.T) {
	create := "gh pr create --fill"
	ok := func(url string) string { return `{"output":"` + url + `\n","exit_code":0}` }
	rows := []string{
		execCall("function_call", "a", create), execResult("function_call_output", "a", ok("https://github.com/acme/api/pull/2")),
		execCall("function_call", "b", create), execResult("function_call_output", "b", ok("https://github.com/acme/api/pull/2")),
		execCall("function_call", "c", create), execResult("function_call_output", "c", `{"output":"https://github.com/acme/failed/pull/3","exit_code":1}`),
		execCall("function_call", "e", "gh pr create --dry-run"), execResult("function_call_output", "e", ok("https://github.com/acme/dry/pull/9")),
		execCall("function_call", "d", "gh pr view 5"), execResult("function_call_output", "d", ok("https://github.com/acme/api/pull/5")),
		`{"timestamp":"2026-10-23T10:00:03Z","type":"response_item","payload":{"type":"function_call","call_id":"m","name":"mcp__github__create_pull_request","arguments":"{}"}}`,
		execResult("function_call_output", "m", `{"url":"https://github.com/acme/web/pull/4"}`),
		`{"timestamp":"2026-10-23T10:00:04Z","type":"response_item","payload":{"type":"function_call","call_id":"n","name":"mcp__github__create_pull_request","arguments":"{}"}}`,
		execResult("function_call_output", "n", `{"isError":true,"url":"https://github.com/acme/web/pull/6"}`),
		`{"timestamp":"2026-10-23T10:00:05Z","type":"event_msg","payload":{"item":{"type":"CommandExecution","command":["bash","-lc","gh pr create"],"exit_code":0,"aggregated_output":"https://github.com/acme/api/pull/2"}}}`,
		`{"timestamp":"2026-10-23T10:00:06Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"::git-create-pr{cwd=\"/w\" branch=\"b\" url=\"https://github.com/acme/web/pull/8\" isDraft=false}"}]}}`,
	}
	parsed := parseLifecycleRows(t, rows)
	want := []session.PullRequestLink{
		{URL: "https://github.com/acme/api/pull/2", Repository: "acme/api", Number: 2},
		{URL: "https://github.com/acme/web/pull/4", Repository: "acme/web", Number: 4},
		{URL: "https://github.com/acme/web/pull/8", Repository: "acme/web", Number: 8},
	}
	if got := parsed.Session.PullRequestLinks; !reflect.DeepEqual(got, want) || parsed.Session.PullRequests != 3 {
		t.Fatalf("links = %#v, count = %d, want %#v", got, parsed.Session.PullRequests, want)
	}
}

func TestParseCacheEntryFromBeforePullRequestLinksIsReparsed(t *testing.T) {
	id := "01a0c51b-94c2-72b0-90ef-b23ea631af2f"
	path := filepath.Join(t.TempDir(), "rollout-2026-09-21T10-55-14-"+id+".jsonl")
	content := `{"timestamp":"2026-09-21T17:55:14Z","type":"session_meta","payload":{"id":"` + id + `","session_id":"parent","parent_thread_id":"parent"}}` + "\n" +
		execCall("function_call", "a", "gh pr create --fill") + "\n" +
		execResult("function_call_output", "a", `{"output":"https://github.com/acme/api/pull/2\n","exit_code":0}`) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	approve := func(string, string) bool { return false }
	parsed, err := parseSourceContext(t.Context(), vendors.LocalReadSource, path, approve)
	if err != nil {
		t.Fatal(err)
	}
	parsed.transcript.Session.PullRequests, parsed.transcript.Session.PullRequestLinks = 0, nil
	payload, err := encodeCached(parsed)
	if err != nil {
		t.Fatal(err)
	}
	info, err := vendors.LocalReadSource.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	cache := codexTestParseCache{{Agent: vendors.AgentCodex, Identity: path, Version: "codex-3", Fingerprint: vendors.StatFingerprint(info)}: payload}
	vendors.SetParseCache(cache)
	t.Cleanup(func() { vendors.SetParseCache(nil) })
	got, err := parseSourceCachedContext(t.Context(), vendors.LocalReadSource, path, approve)
	if err != nil || len(got.transcript.Session.PullRequestLinks) != 1 || got.transcript.Session.PullRequests != 1 {
		t.Fatalf("entry cached before PR links was not reparsed: %#v, %v", got.transcript.Session, err)
	}
}

type codexTestParseCache map[vendors.CacheKey]json.RawMessage

func (cache codexTestParseCache) Lookup(key vendors.CacheKey) (json.RawMessage, bool) {
	payload, ok := cache[key]
	return payload, ok
}

func (cache codexTestParseCache) Store(key vendors.CacheKey, payload json.RawMessage) error {
	cache[key] = append(json.RawMessage(nil), payload...)
	return nil
}

func TestCodeModeScriptPairsEachCommandWithItsResult(t *testing.T) {
	script := func(commands ...string) string {
		var text strings.Builder
		for _, command := range commands {
			text.WriteString("text(await tools.exec_command({cmd:" + string(mustJSON(command)) + "}));\n")
		}
		return text.String()
	}
	call := func(id string, commands ...string) string {
		return `{"timestamp":"2026-10-23T10:00:01Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"` + id + `","name":"exec","input":` + string(mustJSON(script(commands...))) + `}}`
	}
	result := func(id string, blocks ...string) string {
		output := []map[string]string{{"type": "input_text", "text": "Script completed\nWall time 1.0 seconds\nOutput:\n"}}
		for _, block := range blocks {
			output = append(output, map[string]string{"type": "input_text", "text": block})
		}
		return `{"timestamp":"2026-10-23T10:00:02Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"` + id + `","output":` + string(mustJSON(output)) + `}}`
	}
	block := func(code int, output string) string {
		return string(mustJSON(map[string]any{"chunk_id": "a1", "exit_code": code, "output": output}))
	}
	rows := []string{
		call("a", "rtk git push", "rtk proxy gh pr create --fill", "rtk git status"),
		result("a", "pushed", block(0, "https://github.com/acme/api/pull/401\n"), block(0, "clean\n")),
		call("b", "gh pr create --fill"),
		result("b", block(1, "a pull request already exists: https://github.com/acme/api/pull/402\n")),
		call("c", "rtk git push", "gh pr create --fill"),
		result("c", block(0, "pushed\n"), "https://github.com/acme/api/pull/403\n"),
		call("d", "gh pr create --fill", "gh pr create --fill"),
		result("d", block(0, "https://github.com/acme/api/pull/404\n")),
	}
	want := []session.PullRequestLink{{URL: "https://github.com/acme/api/pull/401", Repository: "acme/api", Number: 401}}
	if got := parseLifecycleRows(t, rows).Session.PullRequestLinks; !reflect.DeepEqual(got, want) {
		t.Fatalf("links = %#v, want %#v", got, want)
	}
}
