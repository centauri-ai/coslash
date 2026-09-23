package fullsessionrecord

import (
	"bytes"
	"reflect"
	"testing"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestSessionRoundTripPreservesOrderedChangeBodies(t *testing.T) {
	edits := session.NewFileEditSet()
	edits.Add("main.go", 1, 1, false)
	edits.Patch("main.go", "@@\n-old\n+new\n")
	edits.Add("main.go", 2, 0, false)
	edits.Write("main.go", "package main\n")
	name, summary, model := "complete", "summary", "gpt-5"
	cost := .25
	original := session.Session{
		Agent: "codex", ID: "session-1", Name: &name, Summary: &summary,
		WorkingDirectory: "/workspace", EditedFileCount: 1, StartedAt: 1000, LastActivityTime: 2000,
		Cost: &cost, Tokens: map[string]session.ModelTokens{"gpt-5": {InputTokens: 10, OutputTokens: 5, Cost: .25}},
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
	if record.Session.CostMicroUSD == nil || *record.Session.CostMicroUSD != 250_000 {
		t.Fatalf("cost = %v micro-USD, want 250000", record.Session.CostMicroUSD)
	}
	restored, err := ToSession(record)
	if err != nil {
		t.Fatal(err)
	}
	wantChanges := original.FileEdits[0].Changes()
	for i := range wantChanges {
		wantChanges[i].ID = record.Session.FileEdits[0].Changes[i].ID
	}
	if !reflect.DeepEqual(restored.FileEdits[0].Changes(), wantChanges) {
		t.Fatalf("restored changes = %#v, want %#v", restored.FileEdits[0].Changes(), wantChanges)
	}
	if !reflect.DeepEqual(restored.FileEdits[0].ChangeIDs, []string{"change-000000-000000", "change-000000-000001"}) {
		t.Fatalf("restored change IDs = %#v", restored.FileEdits[0].ChangeIDs)
	}
	if restored.Cost == nil || *restored.Cost != cost {
		t.Fatalf("restored cost = %v, want %v", restored.Cost, cost)
	}
}

func TestSessionRoundTripPreservesOpaqueChangeIDs(t *testing.T) {
	original := session.Session{
		Agent: "codex", ID: "session-1", StartedAt: 1000, LastActivityTime: 2000,
		EditedFileCount: 1, Tokens: map[string]session.ModelTokens{},
		SessionDetails: session.SessionDetails{FileEdits: []session.FileEdit{
			session.FileEditWithChanges("main.go", 1, 0, 1, false, []session.FileChange{{
				ID: "patch-abcd", Kind: "content", Text: "package main\n", Operation: "Write", Additions: 1,
			}}),
		}},
	}
	record, err := FromSession("r_0123456789abcdef", original)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ToSession(record)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := FromSession("r_0123456789abcdef", *restored)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.Session.FileEdits[0].Changes[0].ID != "patch-abcd" || roundTrip.RevisionID != record.RevisionID {
		t.Fatalf("round trip change = %#v, revision = %s; want opaque ID and revision %s",
			roundTrip.Session.FileEdits[0].Changes[0], roundTrip.RevisionID, record.RevisionID)
	}
}

func TestSessionRoundTripPreservesUnknownCosts(t *testing.T) {
	original := session.Session{
		Agent: "codex", ID: "session-1", StartedAt: 1000, LastActivityTime: 2000,
		Tokens:    map[string]session.ModelTokens{},
		Subagents: []session.Subagent{{ID: "subagent-1", Tokens: map[string]session.ModelTokens{}}},
	}
	record, err := FromSession("r_0123456789abcdef", original)
	if err != nil {
		t.Fatal(err)
	}
	if record.Session.CostMicroUSD != nil || record.Session.Subagents[0].CostMicroUSD != nil {
		t.Fatalf("unknown costs encoded as session=%v subagent=%v",
			record.Session.CostMicroUSD, record.Session.Subagents[0].CostMicroUSD)
	}
	data, err := fullsessionv1.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := fullsessionv1.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ToSession(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Cost != nil || restored.Subagents[0].Cost != nil {
		t.Fatalf("unknown costs restored as session=%v subagent=%v",
			restored.Cost, restored.Subagents[0].Cost)
	}
}

func TestRecordRoundTripPreservesMaximumExactMicroUSDCost(t *testing.T) {
	cost := fullsessionv1.MaxCostMicroUSD
	record, err := fullsessionv1.Freeze(fullsessionv1.Record{
		SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: fullsessionv1.Session{
			StartedAtMs: 1, LastActivityAtMs: 1, CostMicroUSD: &cost,
			Usage: []fullsessionv1.ModelUsage{{Model: "gpt-5", CostMicroUSD: cost}},
			Subagents: []fullsessionv1.Subagent{{
				ID: "subagent-1", CostMicroUSD: &cost,
				Usage: []fullsessionv1.ModelUsage{{Model: "gpt-5", CostMicroUSD: cost}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ToSession(record)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := FromSession(record.SourceID, *restored)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.RevisionID != record.RevisionID {
		t.Fatalf("round trip revision = %s, want %s", roundTrip.RevisionID, record.RevisionID)
	}
}

func TestRecordRoundTripPreservesNullStringArrays(t *testing.T) {
	record, err := fullsessionv1.Freeze(fullsessionv1.Record{
		SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: fullsessionv1.Session{
			StartedAtMs: 1, LastActivityAtMs: 1,
			Synthesis: &fullsessionv1.SessionSynthesis{Outcome: "complete"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	restored, err := ToSession(record)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := FromSession(record.SourceID, *restored)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.RevisionID != record.RevisionID {
		t.Fatalf("round trip revision = %s, want %s", roundTrip.RevisionID, record.RevisionID)
	}
}

func TestRecordRoundTripPreservesParentIdentity(t *testing.T) {
	record, err := fullsessionv1.Freeze(fullsessionv1.Record{
		SourceID: "source-1", Agent: "codex", SessionID: "child-1", ParentSessionID: "parent-1",
		Session: fullsessionv1.Session{StartedAtMs: 1, LastActivityAtMs: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ToSession(record)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := FromSession(record.SourceID, *restored)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ParentSessionID != record.ParentSessionID || roundTrip.RevisionID != record.RevisionID {
		t.Fatalf("parent = %q, revision = %q; want %q and %q",
			restored.ParentSessionID, roundTrip.RevisionID, record.ParentSessionID, record.RevisionID)
	}
}

func TestRecordRoundTripPreservesEmptyStructuralArrays(t *testing.T) {
	tests := map[string]func(*fullsessionv1.Session){
		"usage":     func(s *fullsessionv1.Session) { s.Usage = []fullsessionv1.ModelUsage{} },
		"subagents": func(s *fullsessionv1.Session) { s.Subagents = []fullsessionv1.Subagent{} },
		"todos":     func(s *fullsessionv1.Session) { s.Todos = []fullsessionv1.Todo{} },
		"digest":    func(s *fullsessionv1.Session) { s.Digest = []fullsessionv1.DigestEntry{} },
		"file edits": func(s *fullsessionv1.Session) {
			s.FileEdits = []fullsessionv1.FileEdit{}
		},
		"subagent commands and usage": func(s *fullsessionv1.Session) {
			s.Subagents = []fullsessionv1.Subagent{{
				ID: "subagent-1", Commands: []fullsessionv1.SubagentCommand{}, Usage: []fullsessionv1.ModelUsage{},
			}}
		},
		"file changes": func(s *fullsessionv1.Session) {
			s.EditedFileCount = 1
			s.FileEdits = []fullsessionv1.FileEdit{{Path: "main.go", Edits: 1, Changes: []fullsessionv1.FileChange{}}}
		},
	}
	for name, configure := range tests {
		t.Run(name, func(t *testing.T) {
			record := fullsessionv1.Record{
				SourceID: "source-1", Agent: "codex", SessionID: "session-1",
				Session: fullsessionv1.Session{StartedAtMs: 1, LastActivityAtMs: 1},
			}
			configure(&record.Session)
			frozen, err := fullsessionv1.Freeze(record)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := ToSession(frozen)
			if err != nil {
				t.Fatal(err)
			}
			roundTrip, err := FromSession(frozen.SourceID, *restored)
			if err != nil {
				t.Fatal(err)
			}
			if roundTrip.RevisionID != frozen.RevisionID {
				t.Fatalf("round trip revision = %s, want %s", roundTrip.RevisionID, frozen.RevisionID)
			}
		})
	}
}

func TestParsedFamilyRejectsMissingPortableTimestamps(t *testing.T) {
	parsed := []*vendors.ParsedSession{{
		Session: &session.Session{
			Agent: "codex", ID: "session-1", Tokens: map[string]session.ModelTokens{},
			SessionDetails: session.SessionDetails{Turns: 1},
		},
		Spawns: map[string]vendors.SpawnState{},
	}}

	if _, err := FromParsedFamily(
		"r_0123456789abcdef", "codex", vendors.LocalReadSource,
		parsed, vendors.EmptySessionMetadata(),
	); err == nil {
		t.Fatal("portable family without source-derived timestamps was accepted")
	}
}

func TestParsedFamilyPreservesSubagentTextAndIgnoresLiveMetadata(t *testing.T) {
	prompt := "first line\n\n" + string(bytes.Repeat([]byte("task "), 80))
	summary := "result line one\nresult line two"
	family := func() []*vendors.ParsedSession {
		return []*vendors.ParsedSession{
			{
				Session: &session.Session{
					Agent: "codex", ID: "root", StartedAt: 10, LastActivityTime: 20,
					Tokens: map[string]session.ModelTokens{}, SessionDetails: session.SessionDetails{Turns: 1, Commands: []string{}},
				},
				Spawns: map[string]vendors.SpawnState{},
			},
			{
				Session: &session.Session{
					Agent: "codex", ID: "child", StartedAt: 12, LastActivityTime: 25,
					Tokens:         map[string]session.ModelTokens{},
					SessionDetails: session.SessionDetails{FirstPrompt: &prompt}, Summary: &summary,
				},
				ParentID: "root", InTurn: true, Spawns: map[string]vendors.SpawnState{}, Commands: []session.SubagentCommand{},
			},
		}
	}

	parsed := family()
	withoutLive, err := FromParsedFamily(
		"r_0123456789abcdef", "codex", vendors.LocalReadSource,
		parsed, vendors.EmptySessionMetadata(),
	)
	if err != nil {
		t.Fatal(err)
	}
	live := vendors.EmptySessionMetadata()
	live.Session("root").Live = "interactive"
	live.Session("child").Live = "interactive"
	withLive, err := FromParsedFamily(
		"r_0123456789abcdef", "codex", vendors.LocalReadSource, family(), live,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(withoutLive, withLive) {
		t.Fatalf("portable records differ with live metadata:\nwithout = %#v\nwith = %#v", withoutLive, withLive)
	}
	repeated, err := FromParsedFamily(
		"r_0123456789abcdef", "codex", vendors.LocalReadSource,
		parsed, vendors.EmptySessionMetadata(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(withoutLive, repeated) || len(parsed[0].Session.Subagents) != 0 {
		t.Fatalf("portable composition mutated its input or changed on repeat:\nfirst = %#v\nsecond = %#v\ninput = %#v",
			withoutLive, repeated, parsed[0].Session)
	}
	if len(withoutLive) != 2 || withoutLive[1].ParentSessionID != "root" || withoutLive[1].SessionID != "child" {
		t.Fatalf("flattened family = %#v; want root and child with parent identity", withoutLive)
	}
	child := withoutLive[0].Session.Subagents[0]
	if child.Task != prompt || child.Result != summary {
		t.Fatalf("subagent text = (%q, %q); want (%q, %q)", child.Task, child.Result, prompt, summary)
	}
	if child.Status != session.SubagentAborted || withoutLive[0].Session.Status != nil {
		t.Fatalf("portable statuses = root %v, child %q; want nil, aborted", withoutLive[0].Session.Status, child.Status)
	}
	if withoutLive[0].Session.Commands == nil || child.Commands == nil {
		t.Fatalf("portable empty slices became null: commands=%#v subagent commands=%#v",
			withoutLive[0].Session.Commands, child.Commands)
	}
}

func TestParsedFamilyPreservesCompleteDescendantSessions(t *testing.T) {
	goal := "finish child work"
	childName := "named child"
	childEdits := session.FileEditWithChanges("child.go", 1, 0, 1, true, []session.FileChange{{
		ID: "child-patch", Kind: "content", Text: "package child\n", Operation: "Write", Additions: 1,
	}})
	parsed := []*vendors.ParsedSession{
		{Session: &session.Session{Agent: "codex", ID: "root", StartedAt: 10, LastActivityTime: 20, Tokens: map[string]session.ModelTokens{}, SessionDetails: session.SessionDetails{Turns: 1}}, Spawns: map[string]vendors.SpawnState{}},
		{Session: &session.Session{Agent: "codex", ID: "child", StartedAt: 11, LastActivityTime: 21, EditedFileCount: 1, Tokens: map[string]session.ModelTokens{}, SessionDetails: session.SessionDetails{
			Turns: 1, Todos: []session.Todo{{Text: "child todo"}}, FileEdits: []session.FileEdit{childEdits},
			Synthesis: &session.SessionSynthesis{Goals: []string{goal}, Outcome: "done"},
		}}, Name: childName, ParentID: "root", Spawns: map[string]vendors.SpawnState{}},
		{Session: &session.Session{Agent: "codex", ID: "grandchild", StartedAt: 12, LastActivityTime: 22, Tokens: map[string]session.ModelTokens{}, SessionDetails: session.SessionDetails{Turns: 1}}, ParentID: "child", Spawns: map[string]vendors.SpawnState{}},
	}

	records, err := FromParsedFamily("r_0123456789abcdef", "codex", vendors.LocalReadSource, parsed, vendors.EmptySessionMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[1].ParentSessionID != "root" || records[2].ParentSessionID != "child" {
		t.Fatalf("record lineage = %#v", records)
	}
	child := records[1].Session
	if child.Name == nil || *child.Name != childName || len(child.FileEdits) != 1 || child.FileEdits[0].Changes[0].ID != "child-patch" ||
		len(child.Todos) != 1 || child.Synthesis == nil || len(child.Subagents) != 1 || child.Subagents[0].ID != "grandchild" {
		t.Fatalf("complete child record = %#v", child)
	}
}

func TestParsedFamilyPopulatesPerModelCosts(t *testing.T) {
	parsed := []*vendors.ParsedSession{{
		Session: &session.Session{
			Agent: "codex", ID: "session-1", StartedAt: 10, LastActivityTime: 20,
			Tokens: map[string]session.ModelTokens{"gpt-5": {InputTokens: 1_000_000}},
		},
		Spawns: map[string]vendors.SpawnState{},
	}}

	records, err := FromParsedFamily(
		"r_0123456789abcdef", "codex", vendors.LocalReadSource, parsed, vendors.EmptySessionMetadata(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || len(records[0].Session.Usage) != 1 ||
		records[0].Session.Usage[0].CostMicroUSD <= 0 || records[0].Session.CostMicroUSD == nil ||
		records[0].Session.Usage[0].CostMicroUSD != *records[0].Session.CostMicroUSD {
		t.Fatalf("portable costs = %#v", records)
	}
}

func TestParsedFamilyDoesNotMutateMetadataTokenCosts(t *testing.T) {
	parsed := []*vendors.ParsedSession{{
		Session: &session.Session{
			Agent: "codex", ID: "session-1", StartedAt: 10, LastActivityTime: 20,
			Tokens: map[string]session.ModelTokens{}, SessionDetails: session.SessionDetails{Turns: 1},
		},
		Spawns: map[string]vendors.SpawnState{},
	}}
	metadata := vendors.EmptySessionMetadata()
	metadata.Session("session-1").Usage.Tokens = map[string]session.ModelTokens{
		"gpt-5": {InputTokens: 1_000_000},
	}

	if _, err := FromParsedFamily(
		"r_0123456789abcdef", "codex", vendors.LocalReadSource, parsed, metadata,
	); err != nil {
		t.Fatal(err)
	}
	if got := metadata.Session("session-1").Usage.Tokens["gpt-5"].Cost; got != 0 {
		t.Fatalf("metadata model cost mutated to %v", got)
	}
}

func TestFullRecordInventoryAccountsForEveryPrivateSessionField(t *testing.T) {
	decisions := map[string]string{
		"Agent": "record envelope", "ID": "record envelope", "ParentSessionID": "record envelope", "Name": "included", "Summary": "included",
		"Status": "included", "WorkingDirectory": "included", "Branch": "parser or portable metadata only",
		"Repository": "excluded local filesystem enrichment", "RepositoryLocalOnly": "excluded local filesystem enrichment",
		"EditedFileCount": "included", "DurationMs": "included", "Tokens": "included",
		"Cost": "included", "UnpricedModels": "included", "Subagents": "included", "StartedAt": "included",
		"LastActivityTime": "included", "SynthesisRevision": "excluded local synthesis revision",
		"ActivityFallback": "excluded local collection-time marker",
		"DetailRevision":   "excluded local detail cache revision", "Entrypoint": "included", "CommitLog": "parser-only source observation",
		"ReviewPending": "excluded transient local review state", "ReviewError": "excluded transient local review state",
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
		"Model": "included", "ObservedModels": "excluded local Cursor IDE enrichment", "ContextTokens": "included", "ContextWindow": "included", "Turns": "included",
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
