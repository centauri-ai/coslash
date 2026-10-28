package pi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestSkillsCollectedFromSuccessfulReads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_HOME", t.TempDir())
	skillPath, err := json.Marshal(filepath.Join(t.TempDir(), "review", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	tr := projectionFixture(t, "observed-skills", "",
		fmt.Sprintf(`{"type":"message","id":"a","message":{"role":"assistant","content":[{"type":"toolCall","id":"read","name":"read","arguments":{"path":%s,"offset":1,"limit":3}},{"type":"toolCall","id":"pr","name":"bash","arguments":{"command":"gh pr create --fill"}}]}}`, skillPath),
		`{"type":"message","id":"r","parentId":"a","message":{"role":"toolResult","toolCallId":"read","toolName":"read","isError":false,"content":[{"type":"text","text":"---\nname: ignore-frontmatter\n---\n  # Captured\n"},{"type":"text","text":"[2 more lines in file. Use offset=4 to continue.]\n"}]}}`,
		`{"type":"message","id":"p","parentId":"r","message":{"role":"toolResult","toolCallId":"pr","isError":false,"content":[{"type":"text","text":"https://github.com/example/project/pull/42"}]}}`,
	)
	t.Setenv("COSLASH_PI_SESSION_ROOTS", filepath.Dir(tr.Path))
	items, _, err := Collect(0)
	if err != nil || len(items) != 1 {
		t.Fatalf("collect = %d sessions, %v", len(items), err)
	}
	want := []session.Skill{{Name: "review", Content: "---\nname: ignore-frontmatter\n---\n  # Captured\n\n[2 more lines in file. Use offset=4 to continue.]\n"}}
	if !reflect.DeepEqual(items[0].Session.Skills, want) {
		t.Fatalf("collected skills = %#v, want %#v", items[0].Session.Skills, want)
	}
	pr := "https://github.com/example/project/pull/42"
	assertCommandOutputs(t, items[0], []*string{&pr})
	if items[0].Session.PullRequests != 1 || len(items[0].Session.PullRequestLinks) != 1 {
		t.Fatalf("PR links = %+v", items[0].Session.PullRequestLinks)
	}
	facts, err := GetSessionFacts(tr.Header.ID)
	if err != nil || facts == nil {
		t.Fatalf("session facts = %v, %v", facts, err)
	}
	if !reflect.DeepEqual(facts.Session.Skills, want) {
		t.Fatalf("exact skills = %#v, want %#v", facts.Session.Skills, want)
	}
}

func TestSkillsIgnoreFailedMalformedAndUnmatchedResults(t *testing.T) {
	for _, tc := range []struct{ name, message string }{
		{"failed", `{"role":"toolResult","toolCallId":"read","isError":true,"content":"denied"}`},
		{"missing ID", `{"role":"toolResult","content":"body"}`},
		{"wrong ID", `{"role":"toolResult","toolCallId":"other","content":"body"}`},
		{"missing content", `{"role":"toolResult","toolCallId":"read"}`},
		{"null content", `{"role":"toolResult","toolCallId":"read","content":null}`},
		{"object content", `{"role":"toolResult","toolCallId":"read","content":{}}`},
		{"number content", `{"role":"toolResult","toolCallId":"read","content":42}`},
		{"null block", `{"role":"toolResult","toolCallId":"read","content":[null]}`},
		{"missing text", `{"role":"toolResult","toolCallId":"read","content":[{"type":"text"}]}`},
		{"null text", `{"role":"toolResult","toolCallId":"read","content":[{"type":"text","text":null}]}`},
		{"invalid text", `{"role":"toolResult","toolCallId":"read","content":[{"type":"text","text":42}]}`},
		{"invalid success", `{"role":"toolResult","toolCallId":"read","isError":"failed","content":"body"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := []string{
				`{"type":"message","id":"a","message":{"role":"assistant","content":[{"type":"toolCall","id":"read","name":"read","arguments":{"path":"skills/review/SKILL.md"}}]}}`,
				fmt.Sprintf(`{"type":"message","id":"bad","parentId":"a","message":%s}`, tc.message),
			}
			parsed, err := projectLeaf(projectionFixture(t, "invalid-skill", "", rows...), nil, "bad", false)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Session.Skills != nil {
				t.Fatalf("invalid evidence produced skills: %#v", parsed.Session.Skills)
			}
			rows = append(rows, `{"type":"message","id":"good","parentId":"bad","message":{"role":"toolResult","toolCallId":"read","content":"observed"}}`)
			parsed, err = projectLeaf(projectionFixture(t, "recovered-skill", "", rows...), nil, "good", false)
			if err != nil {
				t.Fatal(err)
			}
			if want := []session.Skill{{Name: "review", Content: "observed"}}; !reflect.DeepEqual(parsed.Session.Skills, want) {
				t.Fatalf("recovered skills = %#v, want %#v", parsed.Session.Skills, want)
			}
		})
	}
}

func TestSkillsRequireNativeManifestCalls(t *testing.T) {
	for _, tc := range []struct{ tool, path, name string }{
		{"read", "/skills/review/SKILL.md", "review"},
		{"read", `C:\skills\windows\SKILL.md`, "windows"},
		{"read", "skills/relative/SKILL.md", "relative"},
		{"read", "skills/review/README.md", ""},
		{"read", "skills/review/SKILL.md.bak", ""},
		{"read", "SKILL.md", ""},
		{"write", "skills/review/SKILL.md", ""},
		{"bash", "skills/review/SKILL.md", ""},
	} {
		t.Run(tc.tool+"/"+tc.path, func(t *testing.T) {
			path, err := json.Marshal(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			tr := projectionFixture(t, "manifest-call", "",
				fmt.Sprintf(`{"type":"message","id":"a","message":{"role":"assistant","content":[{"type":"toolCall","id":"read","name":%q,"arguments":{"path":%s}}]}}`, tc.tool, path),
				`{"type":"message","id":"r","parentId":"a","message":{"role":"toolResult","toolCallId":"read","content":"body"}}`,
			)
			parsed, err := projectLeaf(tr, nil, "a", false)
			if err != nil {
				t.Fatal(err)
			}
			var want []session.Skill
			if tc.name != "" {
				want = []session.Skill{{Name: tc.name, Content: "body"}}
			}
			if !reflect.DeepEqual(parsed.Session.Skills, want) {
				t.Fatalf("skills = %#v, want %#v", parsed.Session.Skills, want)
			}
		})
	}
	tr := projectionFixture(t, "malformed-call", "",
		`{"type":"message","id":"a","message":{"role":"assistant","content":[{"type":"toolCall","id":"read","name":"read","arguments":{"path":"skills/review/SKILL.md","command":42}}]}}`,
		`{"type":"message","id":"r","parentId":"a","message":{"role":"toolResult","toolCallId":"read","content":"body"}}`,
	)
	parsed, err := projectLeaf(tr, nil, "r", false)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Session.Skills != nil {
		t.Fatalf("malformed call produced skills: %#v", parsed.Session.Skills)
	}
}

func TestSkillsKeepObservationOrderAcrossBranches(t *testing.T) {
	tr := projectionFixture(t, "skill-branches", "",
		userRow("u", "", "Use skills/review/SKILL.md", "2026-01-01T00:00:01Z"),
		`{"type":"message","id":"a","parentId":"u","message":{"role":"assistant","content":[{"type":"toolCall","id":"shared","name":"read","arguments":{"path":"skills/review/SKILL.md"}},{"type":"toolCall","id":"pending","name":"read","arguments":{"path":"skills/pending/SKILL.md"}}]}}`,
		`{"type":"message","id":"b","parentId":"u","message":{"role":"assistant","content":[{"type":"toolCall","id":"shared","name":"read","arguments":{"path":"skills/other/SKILL.md"}}]}}`,
		`{"type":"message","id":"rb","parentId":"b","message":{"role":"toolResult","toolCallId":"shared","content":"first"}}`,
		`{"type":"message","id":"ra","parentId":"a","message":{"role":"toolResult","toolCallId":"shared","content":"first"}}`,
		`{"type":"message","id":"duplicate","parentId":"ra","message":{"role":"toolResult","toolCallId":"shared","content":"first"}}`,
		`{"type":"message","id":"changed","parentId":"duplicate","message":{"role":"toolResult","toolCallId":"shared","content":"second"}}`,
		`{"type":"message","id":"failed","parentId":"changed","message":{"role":"toolResult","toolCallId":"shared","isError":true,"content":"denied"}}`,
		`{"type":"message","id":"unrelated","parentId":"u","message":{"role":"toolResult","toolCallId":"pending","content":"unrelated"}}`,
		`{"type":"message","id":"orphan","parentId":"missing","message":{"role":"toolResult","toolCallId":"shared","content":"orphan"}}`,
		`{"type":"context_edit","id":"edited","parentId":"changed","targetId":"ra","replacement":{"content":"replacement is not a load"}}`,
		`{"type":"compaction","id":"compacted","parentId":"edited","summary":"checkpoint","firstKeptEntryId":"edited"}`,
	)
	want := []session.Skill{{Name: "other", Content: "first"}, {Name: "review", Content: "first"}, {Name: "review", Content: "second"}}
	for _, leaf := range []string{"rb", "compacted"} {
		parsed, err := projectLeaf(tr, nil, leaf, false)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(parsed.Session.Skills, want) {
			t.Fatalf("leaf %s skills = %#v, want %#v", leaf, parsed.Session.Skills, want)
		}
	}
}

func TestSkillsRequireNativeMessageEvidence(t *testing.T) {
	for _, entryType := range []string{"message", "context_edit", "compaction", "branch_summary", "model_change", "session_info"} {
		for _, endpoint := range []string{"call", "result"} {
			t.Run(entryType+"/"+endpoint, func(t *testing.T) {
				callType, resultType := "message", "message"
				if endpoint == "call" {
					callType = entryType
				} else {
					resultType = entryType
				}
				tr := projectionFixture(t, "native-evidence", "",
					fmt.Sprintf(`{"type":%q,"id":"a","message":{"role":"assistant","content":[{"type":"toolCall","id":"read","name":"read","arguments":{"path":"skills/review/SKILL.md"}}]}}`, callType),
					fmt.Sprintf(`{"type":%q,"id":"r","parentId":"a","message":{"role":"toolResult","toolCallId":"read","content":"body"}}`, resultType),
				)
				parsed, err := projectLeaf(tr, nil, "r", false)
				if err != nil {
					t.Fatal(err)
				}
				var want []session.Skill
				if entryType == "message" {
					want = []session.Skill{{Name: "review", Content: "body"}}
				}
				if !reflect.DeepEqual(parsed.Session.Skills, want) {
					t.Fatalf("skills = %#v, want %#v", parsed.Session.Skills, want)
				}
			})
		}
	}
	tr := projectionFixture(t, "native-ancestry", "",
		`{"type":"message","id":"a","message":{"role":"assistant","content":[{"type":"toolCall","id":"read","name":"read","arguments":{"path":"skills/review/SKILL.md"}},{"type":"toolCall","id":"pr","name":"bash","arguments":{"command":"gh pr create --fill"}}]}}`,
		`{"type":"context_edit","id":"metadata","parentId":"a","message":{"role":"assistant","content":"not an assistant message"}}`,
		`{"type":"message","id":"r","parentId":"metadata","message":{"role":"toolResult","toolCallId":"read","content":"body"}}`,
		`{"type":"message","id":"p","parentId":"r","message":{"role":"toolResult","toolCallId":"pr","content":"https://github.com/example/project/pull/42"}}`,
	)
	parsed, err := projectLeaf(tr, nil, "p", false)
	if err != nil {
		t.Fatal(err)
	}
	if want := []session.Skill{{Name: "review", Content: "body"}}; !reflect.DeepEqual(parsed.Session.Skills, want) {
		t.Fatalf("native ancestry skills = %#v, want %#v", parsed.Session.Skills, want)
	}
	pr := "https://github.com/example/project/pull/42"
	assertCommandOutputs(t, parsed, []*string{&pr})
	if parsed.Session.PullRequests != 1 || len(parsed.Session.PullRequestLinks) != 1 {
		t.Fatalf("native ancestry PR links = %+v", parsed.Session.PullRequestLinks)
	}
}

func TestSkillsRejectCollidingCallIDs(t *testing.T) {
	read := `{"type":"toolCall","id":"shared","name":"read","arguments":{"path":"skills/review/SKILL.md"}}`
	other := `{"type":"toolCall","id":"shared","name":"read","arguments":{"path":"skills/other/SKILL.md"}}`
	write := `{"type":"toolCall","id":"shared","name":"write","arguments":{"path":"unrelated.txt","content":"written"}}`
	for _, tc := range []struct{ name, calls string }{
		{"read/read", read + "," + other},
		{"read/write", read + "," + write},
		{"write/read", write + "," + read},
		{"read/write/read", read + "," + write + "," + other},
		{"identical calls", read + "," + read},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := projectionFixture(t, "colliding-calls", "",
				fmt.Sprintf(`{"type":"message","id":"a","message":{"role":"assistant","content":[%s,{"type":"toolCall","id":"unique","name":"read","arguments":{"path":"skills/valid/SKILL.md"}}]}}`, tc.calls),
				`{"type":"message","id":"r","parentId":"a","message":{"role":"toolResult","toolCallId":"shared","content":"Successfully wrote file"}}`,
				`{"type":"message","id":"valid","parentId":"r","message":{"role":"toolResult","toolCallId":"unique","content":"observed"}}`,
			)
			parsed, err := projectLeaf(tr, nil, "valid", false)
			if err != nil {
				t.Fatal(err)
			}
			if want := []session.Skill{{Name: "valid", Content: "observed"}}; !reflect.DeepEqual(parsed.Session.Skills, want) {
				t.Fatalf("skills = %#v, want %#v", parsed.Session.Skills, want)
			}
		})
	}
}

func TestSkillsKeepEmptyAndBoundedCapturedBodies(t *testing.T) {
	for _, raw := range []string{`""`, `[]`, `[{"type":"image","data":"ignored"}]`} {
		tr := projectionFixture(t, "empty-skill", "",
			`{"type":"message","id":"a","message":{"role":"assistant","content":[{"type":"toolCall","id":"read","name":"read","arguments":{"path":"skills/review/SKILL.md"}}]}}`,
			fmt.Sprintf(`{"type":"message","id":"r","parentId":"a","message":{"role":"toolResult","toolCallId":"read","content":%s}}`, raw),
		)
		parsed, err := projectLeaf(tr, nil, "r", false)
		if err != nil {
			t.Fatal(err)
		}
		if want := []session.Skill{{Name: "review", Content: ""}}; !reflect.DeepEqual(parsed.Session.Skills, want) {
			t.Fatalf("empty skills = %#v, want %#v", parsed.Session.Skills, want)
		}
	}
	for _, body := range []string{strings.Repeat("x", 20<<10), strings.Repeat("界", fullsessionv1.MaxStringBytes/3) + "\x00last"} {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		tr := projectionFixture(t, "bounded-skill", "",
			`{"type":"message","id":"a","message":{"role":"assistant","content":[{"type":"toolCall","id":"read","name":"read","arguments":{"path":"skills/review/SKILL.md"}}]}}`,
			fmt.Sprintf(`{"type":"message","id":"r","parentId":"a","message":{"role":"toolResult","toolCallId":"read","content":%s}}`, raw),
		)
		parsed, err := projectLeaf(tr, nil, "r", false)
		if err != nil {
			t.Fatal(err)
		}
		if len(parsed.Session.Skills) != 1 {
			t.Fatalf("skill count = %d", len(parsed.Session.Skills))
		}
		got := parsed.Session.Skills[0].Content
		if len(body) <= fullsessionv1.MaxStringBytes {
			if got != body {
				t.Fatal("skill body used command-output display truncation")
			}
		} else {
			marker := fmt.Sprintf("\n[Skill content truncated; sha256=%x]", sha256.Sum256([]byte(strings.ReplaceAll(body, "\x00", "\uFFFD"))))
			if len(got) > fullsessionv1.MaxStringBytes || !utf8.ValidString(got) || strings.ContainsRune(got, 0) || !strings.HasSuffix(got, marker) {
				t.Fatalf("invalid bounded skill body: %d bytes", len(got))
			}
		}
	}
}
