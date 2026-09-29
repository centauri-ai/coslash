package vendors

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestParsedSessionEnvelopeRoundTripsHiddenFields(t *testing.T) {
	t.Parallel()
	name, status := "Fix retries", "waiting"
	cost := 1.25
	turn := 3
	edit := session.FileEditWithIdentifiedChanges("a.go", 2, 1, 1, false, []string{"c1"}, []session.FileChange{{ID: "c1", Kind: "diff", Text: "-a\n+b", Operation: "Patch", Additions: 1, Deletions: 1}})
	parsed := &ParsedSession{
		Session: &session.Session{
			Agent: AgentClaude, ID: "root", Name: &name, Status: &status, ParentSessionID: "parent",
			StartedAt: 100, LastActivityTime: 900, ActivityFallback: true,
			CommitLog: []session.CommitObservation{{Hash: "abc", Subject: "fix", Amend: true}},
			Tokens:    map[string]session.ModelTokens{"m": {InputTokens: 1}},
			SessionDetails: session.SessionDetails{
				Digest:         []session.DigestEntry{{Turn: 1, Category: session.DigestSubagent, Description: "child", SpawnKey: "tool-1"}},
				FileEdits:      []session.FileEdit{edit},
				CommitSHAs:     []string{"abc"},
				CompactionSeed: "seed",
			},
		},
		LogPath: "/tmp/root.jsonl", LogModifiedAtMs: 900, ParentID: "", SpawnKey: "k", Stopped: true, Result: "done",
		Spawns:   map[string]SpawnState{"tool-1": {Turn: &turn, Task: "task", Completed: true}},
		Commands: []session.SubagentCommand{{Label: "run", Command: "go test"}},
		Name:     "prompt name", InTurn: true, StatusHint: &status, RecordedCost: &cost,
	}
	type extra struct {
		Rows int `json:"rows"`
	}
	payload, err := EncodeParsedSession(parsed, extra{Rows: 7})
	if err != nil {
		t.Fatal(err)
	}
	var decodedExtra extra
	got, err := DecodeParsedSession(payload, &decodedExtra)
	if err != nil {
		t.Fatal(err)
	}
	if decodedExtra.Rows != 7 {
		t.Fatalf("extra = %+v", decodedExtra)
	}
	if !reflect.DeepEqual(got.Session.FileEdits[0].Changes(), parsed.Session.FileEdits[0].Changes()) {
		t.Fatalf("file changes = %#v", got.Session.FileEdits[0].Changes())
	}
	if got.Session.StartedAt != 100 || got.Session.ParentSessionID != "parent" || !got.Session.ActivityFallback ||
		got.Session.CompactionSeed != "seed" || got.Session.Digest[0].SpawnKey != "tool-1" ||
		!reflect.DeepEqual(got.Session.CommitLog, parsed.Session.CommitLog) || !reflect.DeepEqual(got.Session.CommitSHAs, parsed.Session.CommitSHAs) {
		t.Fatalf("hidden fields lost: %+v", got.Session)
	}
	if !reflect.DeepEqual(got.Spawns, parsed.Spawns) || !reflect.DeepEqual(got.Commands, parsed.Commands) ||
		got.Name != parsed.Name || !got.InTurn || !got.Stopped || got.Result != "done" || *got.RecordedCost != cost ||
		*got.StatusHint != status || got.LogPath != parsed.LogPath || got.LogModifiedAtMs != 900 || got.SpawnKey != "k" {
		t.Fatalf("parsed fields lost: %+v", got)
	}
	before, _ := json.Marshal(parsed.Session)
	after, _ := json.Marshal(got.Session)
	if string(before) != string(after) {
		t.Fatalf("public JSON changed:\n%s\n%s", before, after)
	}
}

func TestParsedSessionEnvelopeKeepsNilCollectionsNil(t *testing.T) {
	t.Parallel()
	payload, err := EncodeParsedSession(&ParsedSession{Session: &session.Session{Agent: AgentCodex, ID: "x"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeParsedSession(payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Spawns != nil || got.Commands != nil || got.Session.Tokens != nil {
		t.Fatalf("nil collections became non-nil: %+v", got)
	}
}

func TestParsedSessionEnvelopeEncodesAbsence(t *testing.T) {
	t.Parallel()
	payload, err := EncodeParsedSession(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeParsedSession(payload, nil)
	if err != nil || got != nil {
		t.Fatalf("absence = %v, %v", got, err)
	}
	if _, err := DecodeParsedSession(json.RawMessage(`{"session":{"session":{}}}`), nil); err == nil {
		t.Fatal("empty session decoded without error")
	}
	if _, err := DecodeParsedSession(json.RawMessage(`{}`), nil); err == nil {
		t.Fatal("missing session decoded as a cached absence")
	}
}
