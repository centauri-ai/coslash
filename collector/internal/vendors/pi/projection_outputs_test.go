package pi

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func assertCommandOutputs(t *testing.T, parsed *vendors.ParsedSession, want []*string) {
	t.Helper()
	if !reflect.DeepEqual(parsed.Session.CommandOutputs, want) {
		t.Fatalf("command outputs = %#v, want %#v", parsed.Session.CommandOutputs, want)
	}
	if len(parsed.Session.Commands) != len(want) || len(parsed.Commands) != len(want) {
		t.Fatalf("commands = %q, labelled = %+v, want %d", parsed.Session.Commands, parsed.Commands, len(want))
	}
	for i, command := range parsed.Commands {
		if command.Command != parsed.Session.Commands[i] || !reflect.DeepEqual(command.Output, want[i]) {
			t.Fatalf("labelled command %d = %+v, want output %v", i, command, want[i])
		}
	}
}

func TestCommandOutputsCollected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_HOME", t.TempDir())
	tr := projectionFixture(t, "command-outputs", "",
		userRow("u", "", "Run commands", "2026-01-01T00:00:01Z"),
		`{"type":"message","id":"a","parentId":"u","timestamp":"2026-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"first","name":"bash","arguments":{"command":"pwd"}},{"type":"toolCall","id":"second","name":"bash","arguments":{"command":"pwd"}},{"type":"toolCall","id":"pending","name":"bash","arguments":{"command":"sleep 10"}},{"type":"toolCall","id":"empty","name":"bash","arguments":{"command":"true"}},{"type":"toolCall","id":"failed","name":"bash","arguments":{"command":"false"}},{"type":"toolCall","id":"null","name":"bash","arguments":{"command":"null-result"}},{"type":"toolCall","id":"missing","name":"bash","arguments":{"command":"missing-content"}},{"type":"toolCall","id":"pr","name":"bash","arguments":{"command":"gh pr create --fill"}},{"type":"toolCall","id":"commit","name":"bash","arguments":{"command":"git commit -m done"}}],"stopReason":"toolUse"}}`,
		`{"type":"message","id":"r2","parentId":"a","message":{"role":"toolResult","toolCallId":"second","content":[{"type":"text","text":"second\n"}],"isError":false}}`,
		`{"type":"message","id":"r1","parentId":"r2","message":{"role":"toolResult","toolCallId":"first","content":"first\n","isError":false}}`,
		`{"type":"message","id":"re","parentId":"r1","message":{"role":"toolResult","toolCallId":"empty","content":[],"isError":false}}`,
		`{"type":"message","id":"rf","parentId":"re","message":{"role":"toolResult","toolCallId":"failed","content":[{"type":"text","text":"permission denied\n"}],"isError":true}}`,
		`{"type":"message","id":"rn","parentId":"rf","message":{"role":"toolResult","toolCallId":"null","content":null,"isError":false}}`,
		`{"type":"message","id":"rm","parentId":"rn","message":{"role":"toolResult","toolCallId":"missing","isError":false}}`,
		`{"type":"message","id":"rp","parentId":"rm","message":{"role":"toolResult","toolCallId":"pr","content":[{"type":"text","text":"https://github.com/example/project/pull/42"}],"isError":false}}`,
		`{"type":"message","id":"rc","parentId":"rp","message":{"role":"toolResult","toolCallId":"commit","content":"[main abcdef1] done","isError":false}}`,
	)
	t.Setenv("COSLASH_PI_SESSION_ROOTS", filepath.Dir(tr.Path))
	items, _, err := Collect(0)
	if err != nil || len(items) != 1 {
		t.Fatalf("collect = %d sessions, %v", len(items), err)
	}
	first, second, empty, failed := "first\n", "second\n", "", "permission denied\n"
	pr, commit := "https://github.com/example/project/pull/42", "[main abcdef1] done"
	want := []*string{&first, &second, nil, &empty, &failed, nil, nil, &pr, &commit}
	assertCommandOutputs(t, items[0], want)
	if got := items[0].Session.Commands; !reflect.DeepEqual(got, []string{"pwd", "pwd", "sleep 10", "true", "false", "null-result", "missing-content", "gh pr create --fill", "git commit -m done"}) {
		t.Fatalf("commands = %q", got)
	}
	if got := items[0].Session; got.Errors != 1 || got.PullRequests != 1 || len(got.CommitLog) != 1 {
		t.Fatalf("command observations: %+v", got)
	}
	if got := items[0].Session.PullRequestLinks; !reflect.DeepEqual(got, []session.PullRequestLink{{URL: pr, Repository: "example/project", Number: 42}}) {
		t.Fatalf("PR links = %+v", got)
	}
	facts, err := GetSessionFacts(tr.Header.ID)
	if err != nil || facts == nil {
		t.Fatalf("session facts = %v, %v", facts, err)
	}
	assertCommandOutputs(t, facts, want)
}

func TestCommandOutputsFollowBranchCallIdentity(t *testing.T) {
	tr := projectionFixture(t, "branches", "",
		userRow("u", "", "Run commands", "2026-01-01T00:00:01Z"),
		`{"type":"message","id":"a","parentId":"u","message":{"role":"assistant","content":[{"type":"toolCall","id":"same","name":"bash","arguments":{"command":"pwd"}}]}}`,
		`{"type":"message","id":"b","parentId":"u","message":{"role":"assistant","content":[{"type":"toolCall","id":"same","name":"bash","arguments":{"command":"pwd"}}]}}`,
		`{"type":"message","id":"orphan","parentId":"missing","message":{"role":"toolResult","toolCallId":"same","content":"unrelated"}}`,
		`{"type":"message","id":"rb","parentId":"b","message":{"role":"toolResult","toolCallId":"same","content":"branch B"}}`,
		`{"type":"message","id":"ra","parentId":"a","message":{"role":"toolResult","toolCallId":"same","content":"branch A"}}`,
		`{"type":"message","id":"unknown","parentId":"ra","message":{"role":"toolResult","toolCallId":"other","content":"wrong call"}}`,
		`{"type":"message","id":"duplicate","parentId":"ra","message":{"role":"toolResult","toolCallId":"same","content":"duplicate result"}}`,
		`{"type":"message","id":"no-id","parentId":"u","message":{"role":"assistant","content":[{"type":"toolCall","name":"bash","arguments":{"command":"ls"}}]}}`,
		`{"type":"message","id":"no-result-id","parentId":"no-id","message":{"role":"toolResult","content":"unidentified"}}`,
	)
	parsed, err := projectLeaf(tr, nil, "ra", false)
	if err != nil {
		t.Fatal(err)
	}
	a, b := "branch A", "branch B"
	assertCommandOutputs(t, parsed, []*string{&a, &b, nil})
}

func TestCommandOutputsBoundedUTF8(t *testing.T) {
	for _, tc := range []struct {
		name, output, want string
	}{
		{"empty string", "", ""},
		{"at limit", strings.Repeat("x", 16384), strings.Repeat("x", 16384)},
		{"multibyte", strings.Repeat("界", 6000), strings.Repeat("界", 2730) + "\n… [1620 bytes truncated] …\n" + strings.Repeat("界", 2730)},
		{"NUL", "a\x00b\n", "a\uFFFDb\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, err := json.Marshal(tc.output)
			if err != nil {
				t.Fatal(err)
			}
			tr := projectionFixture(t, "bounded", "",
				`{"type":"message","id":"a","message":{"role":"assistant","content":[{"type":"toolCall","id":"call","name":"bash","arguments":{"command":"cat output"}}]}}`,
				fmt.Sprintf(`{"type":"message","id":"r","parentId":"a","message":{"role":"toolResult","toolCallId":"call","content":%s}}`, output),
			)
			parsed, err := projectLeaf(tr, nil, "r", false)
			if err != nil {
				t.Fatal(err)
			}
			assertCommandOutputs(t, parsed, []*string{&tc.want})
			if !utf8.ValidString(*parsed.Session.CommandOutputs[0]) {
				t.Fatal("output is not valid UTF-8")
			}
		})
	}
	parsed, err := projectLeaf(projectionFixture(t, "no-commands", "", userRow("u", "", "hello", "2026-01-01T00:00:01Z")), nil, "u", false)
	if err != nil {
		t.Fatal(err)
	}
	assertCommandOutputs(t, parsed, nil)
}

func TestCommandOutputsKeepFullResultPRLinks(t *testing.T) {
	url := "https://github.com/example/project/pull/43"
	output := strings.Repeat("x", 16384) + "\n" + url + "\n" + strings.Repeat("y", 16384)
	tr := projectionFixture(t, "long-pr", "",
		`{"type":"message","id":"a","message":{"role":"assistant","content":[{"type":"toolCall","id":"pr","name":"bash","arguments":{"command":"gh pr create --fill"}}]}}`,
		fmt.Sprintf(`{"type":"message","id":"r","parentId":"a","message":{"role":"toolResult","toolCallId":"pr","content":%q}}`, output),
	)
	parsed, err := projectLeaf(tr, nil, "r", false)
	if err != nil {
		t.Fatal(err)
	}
	want := []session.PullRequestLink{{URL: url, Repository: "example/project", Number: 43}}
	if !reflect.DeepEqual(parsed.Session.PullRequestLinks, want) || parsed.Session.PullRequests != 1 {
		t.Fatalf("PR links = %+v, count = %d", parsed.Session.PullRequestLinks, parsed.Session.PullRequests)
	}
	if len(parsed.Session.CommandOutputs) != 1 || parsed.Session.CommandOutputs[0] == nil || strings.Contains(*parsed.Session.CommandOutputs[0], url) || !strings.Contains(*parsed.Session.CommandOutputs[0], "bytes truncated") {
		t.Fatal("expected the PR link inside the truncated portion of the result")
	}
}
