package remotefacts

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func validFamily() Family {
	return Family{
		SchemaVersion: SchemaVersion, ParserVersion: "parser-v1", Vendor: "codex",
		FamilyID: "root", State: StateComplete,
		Sessions:     []Session{{ID: "root", StartedAtMs: 1, LastActivityAtMs: 2, Usage: []ModelUsage{}, Spawns: []Spawn{}, CommandLabels: []string{}}},
		Metadata:     Metadata{Names: []MetadataName{}, Live: []MetadataLive{}},
		Fingerprints: []Fingerprint{{Key: "opaque-1", Size: 12, ModifiedAtMs: 2}},
	}
}

func TestFromParsedCompactsOversizedFamily(t *testing.T) {
	large := strings.Repeat("x", 600<<10)
	parsed := []*vendors.ParsedSession{
		{Session: &session.Session{ID: "root", StartedAt: 1, LastActivityTime: 2, Summary: &large}},
		{Session: &session.Session{ID: "child", StartedAt: 1, LastActivityTime: 2, Summary: &large}, ParentID: "root"},
	}
	f, err := FromParsed("codex", "root", "parser-v1", StateComplete, "", parsed, vendors.EmptySessionMetadata(), []vendors.FileFingerprint{{Key: "opaque", Size: 1, ModifiedAtMs: 2}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > MaxFamilyBytes || len(f.Sessions) != 2 {
		t.Fatalf("family has %d sessions and is %d bytes", len(f.Sessions), len(encoded))
	}
}

func TestValidateRejectsUnsortedAndUnboundedFacts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Family)
	}{
		{"absolute-looking fingerprint with whitespace", func(f *Family) { f.Fingerprints[0].Key = "bad key" }},
		{"path-like fingerprint", func(f *Family) { f.Fingerprints[0].Key = "/tmp/secret" }},
		{"oversized display", func(f *Family) { f.Sessions[0].Name = strings.Repeat("x", MaxDisplayBytes+1) }},
		{"unknown parent", func(f *Family) { f.Sessions[0].ParentID = "missing" }},
		{"second root", func(f *Family) {
			f.Sessions = append(f.Sessions, Session{ID: "orphan", StartedAtMs: 1, LastActivityAtMs: 2, Usage: []ModelUsage{}, Spawns: []Spawn{}, CommandLabels: []string{}})
		}},
		{"negative count", func(f *Family) { f.Sessions[0].Counts.Errors = -1 }},
		{"unstructured stale reason", func(f *Family) {
			f.State, f.StaleReason = StateStale, "a transcript-derived failure"
		}},
		{"complete family carrying stale reason", func(f *Family) {
			f.StaleReason = StaleReasonReadFailed
		}},
		{"partial family carrying stale reason", func(f *Family) {
			f.State, f.StaleReason = StatePartial, StaleReasonReadFailed
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := validFamily()
			test.mutate(&f)
			if Validate(f) == nil {
				t.Fatal("invalid family accepted")
			}
		})
	}
}

func TestValidateHeaderMappingsMustReferenceFingerprint(t *testing.T) {
	f := validFamily()
	f.HeaderMappings = []HeaderMapping{{Key: "missing", SessionID: "root"}}
	if err := Validate(f); err == nil {
		t.Fatal("accepted header mapping without a matching fingerprint")
	}
}

func TestParsedRoundTripPreservesSessionDetails(t *testing.T) {
	model, branch, status, prompt := "gpt-5", "main", "waiting", "ship the fix"
	cost := 1.25
	turn := 3
	parsed := []*vendors.ParsedSession{{
		Session: &session.Session{Agent: "codex", ID: "root", Summary: &prompt, WorkingDirectory: "/workspace", Branch: &branch, Repository: &branch, StartedAt: 10, LastActivityTime: 20, Tokens: map[string]session.ModelTokens{"gpt-5": {InputTokens: 2, OutputTokens: 3, Cost: .5}}, SessionDetails: session.SessionDetails{Model: &model, Turns: 4, FirstPrompt: &prompt, Commands: []string{"go test ./..."}, Commits: []string{"abc123 fix"}, Todos: []session.Todo{{Text: "test", Done: true}}, Digest: []session.DigestEntry{{Turn: 1, Category: session.DigestUser, Description: prompt}}, FileEdits: []session.FileEdit{{Path: "main.go", Additions: 1}}}},
		Name:    "safe name", StatusHint: &status, RecordedCost: &cost, Spawns: map[string]vendors.SpawnState{"spawn": {Turn: &turn, Completed: true}}, Commands: []session.SubagentCommand{{Label: "tests", Command: "SECRET RAW COMMAND"}},
	}}
	f, err := FromParsed("codex", "root", "parser-v1", StateComplete, "", parsed, vendors.EmptySessionMetadata(), []vendors.FileFingerprint{{Key: "opaque", Size: 1, ModifiedAtMs: 2}})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := f.Parsed()
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Session.ID != "root" || got[0].Session.WorkingDirectory != "/workspace" || *got[0].Session.FirstPrompt != prompt || got[0].Session.Commands[0] != "go test ./..." || got[0].Session.FileEdits[0].Path != "main.go" || got[0].Name != "safe name" || got[0].Commands[0].Label != "tests" || got[0].Commands[0].Command != "SECRET RAW COMMAND" || *got[0].RecordedCost != cost {
		t.Fatalf("round trip = %#v", got[0])
	}
}

// This reviewed census requires an explicit decision for every source field.
func TestFieldPrivacyAllowlistIsComplete(t *testing.T) {
	assertCensus(t, reflect.TypeOf(vendors.ParsedSession{}), map[string]bool{
		"Session": true, "LogPath": false, "LogModifiedAtMs": false,
		"ParentID": true, "SpawnKey": true, "Stopped": true, "Spawns": true,
		"Commands": true, "Name": true, "InTurn": true, "StatusHint": true, "RecordedCost": true,
	})
	assertCensus(t, reflect.TypeOf(session.Session{}), map[string]bool{
		"Agent": true, "ID": true, "Name": true, "Summary": true, "Status": true,
		"WorkingDirectory": true, "Branch": true, "Repository": true, "RepositoryLocalOnly": true,
		"EditedFileCount": true, "DurationMs": true, "Tokens": true, "Cost": true,
		"UnpricedModels": true, "Subagents": true, "StartedAt": true, "LastActivityTime": true,
		"Entrypoint": true, "CommitLog": true, "SessionDetails": true,
	})
	assertCensus(t, reflect.TypeOf(session.SessionDetails{}), map[string]bool{
		"Model": true, "ContextTokens": true, "ContextWindow": true, "Turns": true,
		"ToolUses": true, "Errors": true, "Compactions": true, "FirstPrompt": true,
		"Commands": true, "Commits": true, "PullRequests": true, "Todos": true,
		"CommitSHAs": false, // recomputed only from local repository history
		"Digest":     true, "FileEdits": true, "Git": true, "GitProbed": true,
		"LastEditAt": true, "Synthesis": true, "SynthesisPending": true,
		"DeclaredGoal": true, "CompactionSeed": true,
	})
}

func assertCensus(t *testing.T, typ reflect.Type, decisions map[string]bool) {
	t.Helper()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if _, ok := decisions[name]; !ok {
			t.Errorf("%s.%s lacks a privacy decision", typ.Name(), name)
		}
	}
	for name := range decisions {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("privacy census contains removed field %s.%s", typ.Name(), name)
		}
	}
}
