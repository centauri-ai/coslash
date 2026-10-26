package opencode

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func toolPart(tool, state string) string {
	return fmt.Sprintf(`{"type":"tool","tool":%q,"state":%s}`, tool, state)
}

func parseToolParts(t *testing.T, parts ...string) *vendors.ParsedSession {
	t.Helper()
	db := testDB(t)
	insertMessage(t, db, 0, `{"role":"assistant","time":{"created":1,"completed":2}}`)
	for index, part := range parts {
		if _, err := db.Exec(
			`INSERT INTO part (id, message_id, time_updated, data) VALUES (?, 'message-0', 0, ?)`,
			fmt.Sprintf("part-%02d", index), part,
		); err != nil {
			t.Fatal(err)
		}
	}
	return parseDB(t, db)
}

func outputStrings(outputs []*string) []string {
	got := make([]string, len(outputs))
	for i, output := range outputs {
		if output == nil {
			got[i] = "<nil>"
		} else {
			got[i] = *output
		}
	}
	return got
}

func TestV1ShellOutputsPairWithCommands(t *testing.T) {
	long := strings.Repeat("x", session.CommandOutputLimit+100)
	longState, err := json.Marshal(long)
	if err != nil {
		t.Fatal(err)
	}
	parsed := parseToolParts(t,
		toolPart("bash", `{"status":"completed","input":{"command":"echo hi"},"output":"hi\n","metadata":{"exit":0}}`),
		toolPart("read", `{"status":"completed","input":{},"output":"not a command"}`),
		toolPart("bash", `{"status":"completed","input":{"command":"false"},"output":"boom: failed","metadata":{"exit":1}}`),
		toolPart("shell", `{"status":"completed","input":{"command":"true"},"output":"","metadata":{"exit":0}}`),
		toolPart("bash", `{"status":"completed","input":{"command":"legacy"},"metadata":{"exit":0}}`),
		toolPart("bash", `{"status":"running","input":{"command":"sleep 9"}}`),
		toolPart("bash", `{"status":"completed","input":{"command":"big"},"output":`+string(longState)+`}`),
	)
	details := parsed.Session.SessionDetails
	if want := []string{"echo hi", "false", "true", "legacy", "big"}; !reflect.DeepEqual(details.Commands, want) {
		t.Fatalf("commands = %q, want %q", details.Commands, want)
	}
	got := outputStrings(details.CommandOutputs)
	if len(got) != 5 || got[0] != "hi\n" || got[1] != "boom: failed" || got[2] != "" || got[3] != "<nil>" {
		t.Fatalf("outputs = %q", got)
	}
	if details.CommandOutputs[2] == nil {
		t.Fatal("empty output must stay a real empty string")
	}
	if want := session.TruncateOutput(long); got[4] != want || len(got[4]) >= len(long) {
		t.Fatalf("long output was not truncated: %d bytes", len(got[4]))
	}
	for i, command := range parsed.Commands {
		if command.Command != details.Commands[i] || (command.Output == nil) != (details.CommandOutputs[i] == nil) {
			t.Fatalf("labelled command %d = %#v", i, command)
		}
	}
	if parsed.Commands[0].Output == nil || *parsed.Commands[0].Output != "hi\n" {
		t.Fatalf("subagent projection output = %v", parsed.Commands[0].Output)
	}
}

func TestOlderFormatWithoutResultsStillParses(t *testing.T) {
	parsed := parseToolParts(t,
		toolPart("bash", `{"status":"completed","input":{"command":"ls"}}`),
	)
	details := parsed.Session.SessionDetails
	if !reflect.DeepEqual(details.Commands, []string{"ls"}) || len(details.CommandOutputs) != 1 || details.CommandOutputs[0] != nil {
		t.Fatalf("commands = %q, outputs = %#v", details.Commands, details.CommandOutputs)
	}
	if empty := parseToolParts(t, toolPart("read", `{"status":"completed","input":{}}`)); empty.Session.SessionDetails.CommandOutputs != nil {
		t.Fatalf("no commands must not record outputs: %#v", empty.Session.SessionDetails.CommandOutputs)
	}
}

func TestV2CommandOutputs(t *testing.T) {
	db := testDB(t)
	for _, statement := range []string{
		`CREATE TABLE session_v2 (id TEXT, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_created INTEGER, time_updated INTEGER, time_archived INTEGER)`,
		`CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER DEFAULT 0, data TEXT)`,
		`INSERT INTO session_v2 VALUES ('ses_out', NULL, '/work', 'out', NULL, NULL, NULL, NULL, 0, 100, 900, NULL)`,
		`INSERT INTO session_message (id, session_id, type, seq, time_created, data) VALUES ('u', 'ses_out', 'user', 1, 100, '{"text":"Go"}')`,
		`INSERT INTO session_message (id, session_id, type, seq, time_created, data) VALUES ('a', 'ses_out', 'assistant', 2, 200, '{"content":[` +
			`{"type":"tool","name":"bash","state":{"status":"completed","input":{"command":"echo a"},"content":[{"type":"text","text":"line1"},{"type":"text","text":"line2"}]}},` +
			`{"type":"tool","name":"bash","state":{"status":"completed","input":{"command":"true"},"content":[]}},` +
			`{"type":"tool","name":"bash","state":{"status":"completed","input":{"command":"legacy"}}}` +
			`],"finish":"stop","time":{"created":200,"completed":300}}')`,
		`INSERT INTO session_message (id, session_id, type, seq, time_created, data) VALUES ('s1', 'ses_out', 'shell', 3, 400, '{"command":"echo b","status":"exited","exit":0,"output":{"output":"b\n"}}')`,
		`INSERT INTO session_message (id, session_id, type, seq, time_created, data) VALUES ('s2', 'ses_out', 'shell', 4, 500, '{"command":"bad","status":"exited","exit":2,"output":{"output":"bad: error"}}')`,
		`INSERT INTO session_message (id, session_id, type, seq, time_created, data) VALUES ('s3', 'ses_out', 'shell', 5, 600, '{"command":"quiet","status":"exited","exit":0,"output":{"output":""}}')`,
		`INSERT INTO session_message (id, session_id, type, seq, time_created, data) VALUES ('s4', 'ses_out', 'shell', 6, 700, '{"command":"killed","status":"killed"}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	parsed, skipped, err := load(db, activeFamiliesQuery)
	if err != nil || len(skipped) != 0 || len(parsed) != 1 {
		t.Fatalf("parsed = %#v, skipped = %#v, error = %v", parsed, skipped, err)
	}
	details := parsed[0].Session.SessionDetails
	want := []string{"line1\nline2", "", "<nil>", "b\n", "bad: error", "", "<nil>"}
	if len(details.Commands) != len(want) || !reflect.DeepEqual(outputStrings(details.CommandOutputs), want) {
		t.Fatalf("commands = %q, outputs = %q, want %q", details.Commands, outputStrings(details.CommandOutputs), want)
	}
}

func TestV1PullRequestLinksFromSuccessfulCreates(t *testing.T) {
	parsed := parseToolParts(t,
		toolPart("bash", `{"status":"completed","input":{"command":"gh pr create --fill"},"output":"https://github.com/acme/api/pull/2\n","metadata":{"exit":0}}`),
		toolPart("bash", `{"status":"completed","input":{"command":"gh pr create --fill"},"output":"https://github.com/acme/api/pull/2\n","metadata":{"exit":0}}`),
		toolPart("bash", `{"status":"completed","input":{"command":"gh pr create --fill"},"output":"https://github.com/acme/failed/pull/3","metadata":{"exit":1}}`),
		toolPart("bash", `{"status":"completed","input":{"command":"gh pr create --dry-run"},"output":"https://github.com/acme/dry/pull/9","metadata":{"exit":0}}`),
		toolPart("bash", `{"status":"completed","input":{"command":"gh pr view 5"},"output":"https://github.com/acme/api/pull/5","metadata":{"exit":0}}`),
		toolPart("github_create_pull_request", `{"status":"completed","input":{},"output":"{\"url\":\"https://github.com/acme/web/pull/4\"}"}`),
		toolPart("github_create_pull_request", `{"status":"error","input":{},"output":"https://github.com/acme/web/pull/6"}`),
	)
	want := []session.PullRequestLink{
		{URL: "https://github.com/acme/api/pull/2", Repository: "acme/api", Number: 2},
		{URL: "https://github.com/acme/web/pull/4", Repository: "acme/web", Number: 4},
	}
	if got := parsed.Session.PullRequestLinks; !reflect.DeepEqual(got, want) || parsed.Session.PullRequests != 2 {
		t.Fatalf("links = %#v, count = %d, want %#v", got, parsed.Session.PullRequests, want)
	}
}

type opencodeTestParseCache map[vendors.CacheKey]json.RawMessage

func (cache opencodeTestParseCache) Lookup(key vendors.CacheKey) (json.RawMessage, bool) {
	payload, ok := cache[key]
	return payload, ok
}

func (cache opencodeTestParseCache) Store(key vendors.CacheKey, payload json.RawMessage) error {
	cache[key] = append(json.RawMessage(nil), payload...)
	return nil
}

func TestParseCacheEntryFromBeforePullRequestLinksIsReparsed(t *testing.T) {
	db := testDB(t)
	for _, statement := range []string{
		`CREATE TABLE session_v2 (id TEXT, parent_id TEXT, directory TEXT, title TEXT, summary_files INTEGER, summary_diffs TEXT, agent TEXT, model TEXT, cost REAL, time_created INTEGER, time_updated INTEGER, time_archived INTEGER)`,
		`CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER DEFAULT 0, data TEXT)`,
		`INSERT INTO session_v2 VALUES ('ses_shell', NULL, '/work', 'shell', NULL, NULL, NULL, NULL, 0, 100, 400, NULL)`,
		`INSERT INTO session_message (id, session_id, type, seq, time_created, data) VALUES ('u', 'ses_shell', 'user', 1, 100, '{"text":"Do work"}')`,
		`INSERT INTO session_message (id, session_id, type, seq, time_created, data) VALUES ('a', 'ses_shell', 'assistant', 2, 200, '{"time":{"created":200,"completed":300},"finish":"stop"}')`,
		`INSERT INTO session_message (id, session_id, type, seq, time_created, data) VALUES ('s', 'ses_shell', 'shell', 3, 400, '{"command":"gh pr create","status":"exited","exit":0,"output":{"output":"https://github.com/acme/api/pull/7\n"}}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	current := opencodeTestParseCache{}
	vendors.SetParseCache(current)
	t.Cleanup(func() { vendors.SetParseCache(nil) })
	if parsed, _, err := load(db, activeFamiliesQuery); err != nil || len(parsed) != 1 || len(parsed[0].Session.PullRequestLinks) != 1 {
		t.Fatalf("first parse = %#v, %v", parsed, err)
	}
	stale := opencodeTestParseCache{}
	for key, payload := range current {
		family, err := decodeCachedFamily(payload)
		if err != nil {
			t.Fatal(err)
		}
		family[0].transcript.Session.PullRequests, family[0].transcript.Session.PullRequestLinks = 0, nil
		if payload, err = encodeCachedFamily(family); err != nil {
			t.Fatal(err)
		}
		key.Version = "opencode-2"
		stale[key] = payload
	}
	vendors.SetParseCache(stale)
	parsed, _, err := load(db, activeFamiliesQuery)
	if err != nil || len(parsed) != 1 || len(parsed[0].Session.PullRequestLinks) != 1 || parsed[0].Session.PullRequests != 1 {
		t.Fatalf("entry cached before PR links was not reparsed: %#v, %v", parsed, err)
	}
}
