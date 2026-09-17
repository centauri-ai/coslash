package fullsessionrecord

import (
	"reflect"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestSessionRoundTripPreservesOrderedChangeBodies(t *testing.T) {
	edits := session.NewFileEditSet()
	edits.Add("main.go", 1, 1, false)
	edits.Patch("main.go", "@@\n-old\n+new\n")
	edits.Add("main.go", 2, 0, false)
	edits.Write("main.go", "package main\n")
	name, summary, model := "complete", "summary", "gpt-5"
	original := session.Session{
		Agent: "codex", ID: "session-1", Name: &name, Summary: &summary,
		WorkingDirectory: "/workspace", EditedFileCount: 1, StartedAt: 1000, LastActivityTime: 2000,
		Tokens:    map[string]session.ModelTokens{"gpt-5": {InputTokens: 10, OutputTokens: 5, Cost: .25}},
		Subagents: []session.Subagent{}, SessionDetails: session.SessionDetails{
			Model: &model, Commands: []string{"go test ./..."}, Commits: []string{}, CommitSHAs: []string{},
			Todos: []session.Todo{}, Digest: []session.DigestEntry{}, FileEdits: edits.Edits,
		},
	}
	record, err := FromSession("r_0123456789abcdef", original)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Session.FileEdits) != 1 || len(record.Session.FileEdits[0].Changes) != 2 ||
		record.Session.FileEdits[0].Changes[0].Text != "@@\n-old\n+new\n" ||
		record.Session.FileEdits[0].Changes[1].Text != "package main\n" {
		t.Fatalf("ordered changes = %#v", record.Session.FileEdits)
	}
	restored, err := ToSession(record)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.FileEdits[0].Changes(), original.FileEdits[0].Changes()) {
		t.Fatalf("restored changes = %#v, want %#v", restored.FileEdits[0].Changes(), original.FileEdits[0].Changes())
	}
}

func TestFullRecordInventoryAccountsForEveryPrivateSessionField(t *testing.T) {
	decisions := map[string]string{
		"Agent": "record envelope", "ID": "record envelope", "Name": "included", "Summary": "included",
		"Status": "included", "WorkingDirectory": "included", "Branch": "parser or portable metadata only",
		"Repository": "excluded local filesystem enrichment", "RepositoryLocalOnly": "excluded local filesystem enrichment",
		"EditedFileCount": "included", "DurationMs": "included", "Tokens": "included",
		"Cost": "included", "UnpricedModels": "included", "Subagents": "included", "StartedAt": "included",
		"LastActivityTime": "included", "Entrypoint": "included", "CommitLog": "parser-only source observation",
		"SessionDetails": "included field-by-field",
	}
	typeOf := reflect.TypeOf(session.Session{})
	for index := 0; index < typeOf.NumField(); index++ {
		field := typeOf.Field(index)
		if decisions[field.Name] == "" {
			t.Errorf("session field %s has no full-record decision", field.Name)
		}
	}
	if len(decisions) != typeOf.NumField() {
		t.Fatalf("stale session inventory: decisions=%d fields=%d", len(decisions), typeOf.NumField())
	}

	detailDecisions := map[string]string{
		"Model": "included", "ContextTokens": "included", "ContextWindow": "included", "Turns": "included",
		"ToolUses": "included", "Errors": "included", "Compactions": "included", "FirstPrompt": "included",
		"Commands": "included", "Commits": "included", "CommitSHAs": "included", "PullRequests": "included",
		"Todos": "included", "Digest": "included", "FileEdits": "included with bodies", "Git": "excluded local filesystem enrichment",
		"GitProbed": "runtime probe marker", "LastEditAt": "excluded local filesystem enrichment", "Synthesis": "included",
		"SynthesisPending": "included", "DeclaredGoal": "included", "CompactionSeed": "parser-only composition seed",
	}
	detailsType := reflect.TypeOf(session.SessionDetails{})
	for index := 0; index < detailsType.NumField(); index++ {
		field := detailsType.Field(index)
		if detailDecisions[field.Name] == "" {
			t.Errorf("detail field %s has no full-record decision", field.Name)
		}
	}
	if len(detailDecisions) != detailsType.NumField() {
		t.Fatalf("stale detail inventory: decisions=%d fields=%d", len(detailDecisions), detailsType.NumField())
	}
}
