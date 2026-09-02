package remotefacts

import (
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

func TestParsedRoundTripPreservesApprovedCompositionFacts(t *testing.T) {
	model, branch, status := "gpt-5", "main", "waiting"
	cost := 1.25
	turn := 3
	parsed := []*vendors.ParsedSession{{
		Session: &session.Session{Agent: "codex", ID: "root", Branch: &branch, StartedAt: 10, LastActivityTime: 20, Tokens: map[string]session.ModelTokens{"gpt-5": {InputTokens: 2, OutputTokens: 3, Cost: .5}}, SessionDetails: session.SessionDetails{Model: &model, Turns: 4}},
		Name:    "safe name", StatusHint: &status, RecordedCost: &cost, Spawns: map[string]vendors.SpawnState{"spawn": {Turn: &turn, Completed: true}}, Commands: []session.SubagentCommand{{Label: "tests", Command: "SECRET RAW COMMAND"}},
	}}
	f, err := FromParsed("codex", "root", "parser-v1", StateComplete, "", parsed, vendors.EmptySessionMetadata(), []vendors.FileFingerprint{{Key: "opaque", Size: 1, ModifiedAtMs: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.ValueOf(f).String() == "SECRET RAW COMMAND" {
		t.Fatal("raw command crossed boundary")
	}
	got, _, err := f.Parsed()
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Session.ID != "root" || got[0].Name != "safe name" || got[0].Commands[0].Label != "tests" || got[0].Commands[0].Command != "" || *got[0].RecordedCost != cost {
		t.Fatalf("round trip = %#v", got[0])
	}
}

// This reviewed census keeps new ParsedSession and Session fields excluded by
// default until their privacy decision is explicit here and in FromParsed.
func TestFieldPrivacyAllowlistIsComplete(t *testing.T) {
	assertCensus(t, reflect.TypeOf(vendors.ParsedSession{}), map[string]bool{
		"Session": true, "LogPath": false, "LogModifiedAtMs": false,
		"ParentID": true, "SpawnKey": true, "Stopped": true, "Spawns": true,
		"Commands": true, "Name": true, "InTurn": true, "StatusHint": true, "RecordedCost": true,
	})
	assertCensus(t, reflect.TypeOf(session.Session{}), map[string]bool{
		"Agent": true, "ID": true, "Name": false, "Summary": false, "Status": false,
		"WorkingDirectory": false, "Branch": true, "Repository": false, "RepositoryLocalOnly": false,
		"EditedFileCount": true, "DurationMs": true, "Tokens": true, "Cost": false,
		"UnpricedModels": false, "Subagents": false, "StartedAt": true, "LastActivityTime": true,
		"Entrypoint": true, "CommitLog": false, "SessionDetails": true,
	})
	assertCensus(t, reflect.TypeOf(session.SessionDetails{}), map[string]bool{
		"Model": true, "ContextTokens": true, "ContextWindow": true, "Turns": true,
		"ToolUses": true, "Errors": true, "Compactions": true, "FirstPrompt": false,
		"Commands": false, "Commits": false, "PullRequests": true, "Todos": false,
		"Digest": false, "FileEdits": false, "Git": false, "GitProbed": false,
		"LastEditAt": false, "Synthesis": false, "SynthesisPending": false,
		"DeclaredGoal": false, "CompactionSeed": false,
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
