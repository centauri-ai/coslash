package fullsessionrecord

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"reflect"
	"testing"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/grok"
)

func TestCostValidationBeforeMicroRounding(t *testing.T) {
	maximum := float64(fullsessionv1.MaxCostMicroUSD) / 1e6
	for _, agent := range []string{"codex", "claude", "cursor", "opencode", "grok"} {
		for _, field := range []string{"model", "session", "child model", "child session"} {
			for _, cost := range []float64{-1, -0.00000049, -1e-10, 0, 1e-10, 0.00000049, 0.0000005, 0.0000015, math.Nextafter(maximum, 0), maximum} {
				t.Run(fmt.Sprintf("%s/%s/%g", agent, field, cost), func(t *testing.T) {
					value := session.Session{Agent: agent, ID: "root", StartedAt: 1, LastActivityTime: 2}
					switch field {
					case "model":
						value.Tokens = map[string]session.ModelTokens{"gpt-5": {InputTokens: 1, Cost: cost}}
					case "session":
						value.Cost = &cost
					case "child model":
						value.Subagents = []session.Subagent{{ID: "child", Tokens: map[string]session.ModelTokens{"gpt-5": {InputTokens: 1, Cost: cost}}}}
					case "child session":
						value.Subagents = []session.Subagent{{ID: "child", Cost: &cost}}
					}
					before := session.Clone(&value)
					record, err := FromSession("local", value)
					if !reflect.DeepEqual(&value, before) {
						t.Fatal("adapter mutated caller accounting")
					}
					if cost < 0 {
						if err == nil || !reflect.DeepEqual(record, fullsessionv1.Record{}) {
							t.Fatalf("negative cost returned a record: err=%v", err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					var got int64
					switch field {
					case "model":
						got = record.Session.Usage[0].CostMicroUSD
					case "session":
						got = *record.Session.CostMicroUSD
					case "child model":
						got = record.Session.Subagents[0].Usage[0].CostMicroUSD
					case "child session":
						got = *record.Session.Subagents[0].CostMicroUSD
					}
					if want := int64(math.Round(cost * 1e6)); got != want {
						t.Fatalf("cost=%d want=%d", got, want)
					}
				})
			}
		}
	}
}

func TestNonfiniteCostsRejected(t *testing.T) {
	for _, cost := range []float64{math.NaN(), math.Inf(-1), math.Inf(1)} {
		for _, child := range []bool{false, true} {
			for _, model := range []bool{false, true} {
				t.Run(fmt.Sprintf("%g/child=%t/model=%t", cost, child, model), func(t *testing.T) {
					value := session.Session{Agent: "grok", ID: "root", StartedAt: 1, LastActivityTime: 2}
					var tokens map[string]session.ModelTokens
					var total *float64
					if model {
						tokens = map[string]session.ModelTokens{"gpt-5": {InputTokens: 1, Cost: cost}}
					} else {
						total = &cost
					}
					if child {
						value.Subagents = []session.Subagent{{ID: "child", Tokens: tokens, Cost: total}}
					} else {
						value.Tokens, value.Cost = tokens, total
					}
					record, err := FromSession("local", value)
					if !errors.Is(err, fullsessionv1.ErrInvalid) || !reflect.DeepEqual(record, fullsessionv1.Record{}) {
						t.Fatalf("nonfinite cost returned a complete record: err=%v", err)
					}
				})
			}
		}
	}
}

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
		"TokensKnown":        "local accounting unavailable formats refused",
		"TokensUnavailable":  "local accounting unavailable formats refused",
		"CostUnavailable":    "local accounting unavailable formats refused",
		"UnattributedTokens": "included for Grok with enclosing nullable cost; other agents refused",
		"TranscriptPath":     "excluded private resume path",
		"Agent":              "record envelope", "ID": "record envelope", "ParentSessionID": "record envelope", "Name": "included", "Summary": "included",
		"Status": "included", "WorkingDirectory": "included", "Branch": "parser or portable metadata only",
		"Repository": "excluded local filesystem enrichment", "RepositoryLocalOnly": "excluded local filesystem enrichment",
		"EditedFileCount": "included", "DurationMs": "included", "Tokens": "included",
		"Cost": "included", "UnpricedModels": "included", "Subagents": "included", "StartedAt": "included",
		"LastActivityTime": "included", "SynthesisRevision": "excluded local synthesis revision",
		"ActivityFallback": "excluded local collection-time marker",
		"LocalSSHMirror":   "excluded local Desktop SSH mirror marker",
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
		"AgentError":        "excluded local provider failure diagnostic",
		"DetailsIncomplete": "unrepresentable detail completeness refused",
		"Model":             "included", "ObservedModels": "excluded local Cursor IDE enrichment", "ContextTokens": "included", "ContextWindow": "included", "Turns": "included",
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

func TestPiFullRecordRefusesUnrepresentableLocalFacts(t *testing.T) {
	for _, value := range []session.Session{{Agent: "pi"}, {TokensUnavailable: true}, {UnattributedTokens: &session.ModelTokens{InputTokens: 2}}, {SessionDetails: session.SessionDetails{Digest: []session.DigestEntry{{BranchID: "branch"}}}}} {
		if _, err := FromSession("source", value); err == nil {
			t.Fatal("unrepresentable local facts silently lost")
		}
	}
}

func TestGrokUnattributedRoundTrip(t *testing.T) {
	original := session.Session{Agent: "grok", ID: "root", StartedAt: 1, LastActivityTime: 2,
		UnattributedTokens: &session.ModelTokens{InputTokens: 12, OutputTokens: 3, CacheReadInputTokens: 4},
		Subagents:          []session.Subagent{{ID: "child", UnattributedTokens: &session.ModelTokens{InputTokens: 7}}},
	}
	record, err := FromSession("local", original)
	if err != nil {
		t.Fatal(err)
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
	if !reflect.DeepEqual(restored.UnattributedTokens, original.UnattributedTokens) || !reflect.DeepEqual(restored.Subagents[0].UnattributedTokens, original.Subagents[0].UnattributedTokens) || restored.Cost != nil || restored.Subagents[0].Cost != nil || len(restored.Tokens) != 0 {
		t.Fatalf("unattributed accounting lost or fabricated: %+v, child %+v", restored, restored.Subagents[0])
	}
}

func TestGrokUnattributedCostAndZeroRoundTrip(t *testing.T) {
	zero, positive, maximum := 0.0, 0.123456, float64(fullsessionv1.MaxCostMicroUSD)/1_000_000
	for _, cost := range []*float64{nil, &zero, &positive, &maximum} {
		for _, input := range []int{0, 12} {
			used := &session.ModelTokens{}
			if input != 0 {
				used = &session.ModelTokens{InputTokens: input, OutputTokens: 3, CacheCreationInputTokens: 5, CacheCreation1hInputTokens: 6, CacheReadInputTokens: 4}
			}
			original := session.Session{Agent: "grok", ID: "root", StartedAt: 1, LastActivityTime: 2, Cost: cost,
				UnattributedTokens: used,
				Subagents:          []session.Subagent{{ID: "child", Cost: cost, UnattributedTokens: &session.ModelTokens{InputTokens: input}}},
			}
			record, err := FromSession("local", original)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := ToSession(record)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(restored.Cost, cost) || !reflect.DeepEqual(restored.Subagents[0].Cost, cost) || !reflect.DeepEqual(restored.UnattributedTokens, original.UnattributedTokens) {
				t.Fatalf("cost or counters changed: %+v", restored)
			}
			repeated, err := FromSession("local", *restored)
			if err != nil {
				t.Fatal(err)
			}
			a, err := fullsessionv1.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			b, err := fullsessionv1.Marshal(repeated)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(a, b) {
				t.Fatal("valid portable accounting did not round-trip byte for byte")
			}
			original.UnattributedTokens.InputTokens = 99
			original.Subagents[0].UnattributedTokens.InputTokens = 99
			restored.UnattributedTokens.InputTokens = 88
			restored.Subagents[0].UnattributedTokens.InputTokens = 88
			if record.Session.UnattributedTokens.InputTokens != input || record.Session.Subagents[0].UnattributedTokens.InputTokens != input {
				t.Fatal("adapter aliases unattributed tokens")
			}
		}
	}
}

func TestGrokUnattributedRejectsInvalidPrivateCosts(t *testing.T) {
	for _, child := range []bool{false, true} {
		for _, cost := range []float64{-0.0000001, math.NaN(), math.Inf(1), math.Inf(-1), float64(fullsessionv1.MaxCostMicroUSD)/1_000_000 + 1} {
			value := session.Session{Agent: "grok", ID: "root", StartedAt: 1, LastActivityTime: 2}
			if child {
				value.Subagents = []session.Subagent{{ID: "child", UnattributedTokens: &session.ModelTokens{}, Cost: &cost}}
			} else {
				value.UnattributedTokens = &session.ModelTokens{}
				value.Cost = &cost
			}
			if _, err := FromSession("local", value); err == nil {
				t.Fatalf("child=%v accepted cost %v", child, cost)
			}
		}
		value := session.Session{Agent: "grok", ID: "root", StartedAt: 1, LastActivityTime: 2}
		if child {
			value.Subagents = []session.Subagent{{ID: "child", UnattributedTokens: &session.ModelTokens{Cost: 1}}}
		} else {
			value.UnattributedTokens = &session.ModelTokens{Cost: 1}
		}
		if _, err := FromSession("local", value); err == nil {
			t.Fatalf("child=%v discarded independently populated float cost", child)
		}
	}
}

func TestGrokSourceAccountingSurvivesPortableFamily(t *testing.T) {
	for _, tt := range []struct {
		name, usage                     string
		tokens                          int
		attributed, unattributed, known bool
	}{
		{name: "attributed", tokens: 170, attributed: true, known: true},
		{name: "unattributed19", usage: `{"session":{"inputTokens":12,"outputTokens":3,"cachedReadTokens":4}}`, tokens: 19, unattributed: true},
		{name: "known zero cost", usage: `{"session":{"inputTokens":12,"costUsdTicks":0}}`, tokens: 12, unattributed: true, known: true},
		{name: "partial cost", usage: `{"session":{"inputTokens":12,"costUsdTicks":10000000000,"costIsPartial":true}}`, tokens: 12, unattributed: true},
		{name: "incomplete cost", usage: `{"session":{"inputTokens":12,"costUsdTicks":10000000000,"usageIsIncomplete":true}}`, tokens: 12, unattributed: true},
		{name: "absent", usage: "absent"},
		{name: "source known zero", usage: `{"session":{"inputTokens":0,"outputTokens":0}}`, unattributed: true},
		{name: "source null remains absent", usage: `{"session":{"inputTokens":null,"outputTokens":null,"cachedReadTokens":null,"cacheCreationTokens":null}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("GROK_HOME", home)
			dir := filepath.Join(home, "sessions", "fixture", "root")
			if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "vendors", "grok", "testdata", "finished"))); err != nil {
				t.Fatal(err)
			}
			if tt.usage == "absent" {
				if err := os.Remove(filepath.Join(dir, "usage.json")); err != nil {
					t.Fatal(err)
				}
			} else if tt.usage != "" {
				if err := os.WriteFile(filepath.Join(dir, "usage.json"), []byte(tt.usage), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			parsed, metadata, err := grok.CollectContext(t.Context(), 0)
			if err != nil {
				t.Fatal(err)
			}
			records, err := FromParsedFamily("local", "grok", vendors.LocalReadSource, parsed, metadata)
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 {
				t.Fatalf("records=%d", len(records))
			}
			record := records[0]
			if (len(record.Session.Usage) > 0) != tt.attributed || (record.Session.UnattributedTokens != nil) != tt.unattributed || (record.Session.CostMicroUSD != nil) != tt.known {
				t.Fatalf("accounting presence changed: %+v", record.Session)
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
			total := 0
			for _, used := range restored.Tokens {
				total += used.InputTokens + used.OutputTokens + used.CacheReadInputTokens + used.CacheCreationInputTokens + used.CacheCreation1hInputTokens
			}
			if used := restored.UnattributedTokens; used != nil {
				total += used.InputTokens + used.OutputTokens + used.CacheReadInputTokens + used.CacheCreationInputTokens + used.CacheCreation1hInputTokens
			}
			if total != tt.tokens {
				t.Fatalf("tokens=%d,want %d", total, tt.tokens)
			}
			repeated, err := FromSession("local", *restored)
			if err != nil {
				t.Fatal(err)
			}
			if repeated.RevisionID != record.RevisionID {
				t.Fatal("source accounting revision changed on round trip")
			}
		})
	}
}

func TestGrokNestedFamilyAccountingIsNotRolledIntoParent(t *testing.T) {
	parsed := []*vendors.ParsedSession{
		{Session: &session.Session{Agent: "grok", ID: "root", StartedAt: 1, LastActivityTime: 2, UnattributedTokens: &session.ModelTokens{InputTokens: 12, OutputTokens: 3, CacheReadInputTokens: 4}}},
		{ParentID: "root", Session: &session.Session{Agent: "grok", ID: "child", StartedAt: 1, LastActivityTime: 2, UnattributedTokens: &session.ModelTokens{InputTokens: 7}}},
		{ParentID: "child", Session: &session.Session{Agent: "grok", ID: "grandchild", StartedAt: 1, LastActivityTime: 2, Tokens: map[string]session.ModelTokens{"grok-4.7": {InputTokens: 5}}}},
	}
	records, err := FromParsedFamily("local", "grok", vendors.LocalReadSource, parsed, vendors.EmptySessionMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[1].ParentSessionID != "root" || records[2].ParentSessionID != "child" {
		t.Fatalf("incomplete lineage: %+v", records)
	}
	if got := records[0].Session.UnattributedTokens; got == nil || got.InputTokens != 12 || got.OutputTokens != 3 || got.CacheReadInputTokens != 4 {
		t.Fatalf("root totals changed: %+v", got)
	}
	if len(records[0].Session.Subagents) != 1 || records[0].Session.Subagents[0].UnattributedTokens.InputTokens != 7 || records[1].Session.UnattributedTokens.InputTokens != 7 || len(records[1].Session.Subagents) != 1 || records[1].Session.Subagents[0].Usage[0].InputTokens != 5 || records[2].Session.Usage[0].InputTokens != 5 {
		t.Fatalf("nested accounting lost: %+v", records)
	}
	total := 0
	for _, record := range records {
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
		repeated, err := FromSession("local", *restored)
		if err != nil {
			t.Fatal(err)
		}
		if repeated.RevisionID != record.RevisionID {
			t.Fatal("nested accounting changed revision")
		}
		if used := restored.UnattributedTokens; used != nil {
			total += used.InputTokens + used.OutputTokens + used.CacheReadInputTokens
		}
		for _, used := range restored.Tokens {
			total += used.InputTokens
		}
	}
	if total != 31 {
		t.Fatalf("family total=%d,want 19+7+5=31", total)
	}
	if len(parsed[0].Session.Subagents) != 0 || len(parsed[1].Session.Subagents) != 0 {
		t.Fatal("composition mutated caller family")
	}
}

func TestGrokPreservesUnrepresentableFactGuards(t *testing.T) {
	falseValue, text := false, "context"
	for name, mutate := range map[string]func(*session.Session){
		"Pi":                       func(s *session.Session) { s.Agent = "pi" },
		"tokens unavailable":       func(s *session.Session) { s.TokensUnavailable = true },
		"cost unavailable":         func(s *session.Session) { s.CostUnavailable = true },
		"details incomplete":       func(s *session.Session) { s.DetailsIncomplete = true },
		"child tokens unavailable": func(s *session.Session) { s.Subagents = []session.Subagent{{ID: "child", TokensUnavailable: true}} },
		"child cost unavailable":   func(s *session.Session) { s.Subagents = []session.Subagent{{ID: "child", CostUnavailable: true}} },
		"source entry":             func(s *session.Session) { s.Digest = []session.DigestEntry{{SourceEntryID: "entry"}} },
		"parent entry":             func(s *session.Session) { s.Digest = []session.DigestEntry{{ParentEntryID: "entry"}} },
		"branch":                   func(s *session.Session) { s.Digest = []session.DigestEntry{{BranchID: "branch"}} },
		"active":                   func(s *session.Session) { s.Digest = []session.DigestEntry{{Active: &falseValue}} },
		"inherited":                func(s *session.Session) { s.Digest = []session.DigestEntry{{Inherited: &falseValue}} },
		"selected":                 func(s *session.Session) { s.Digest = []session.DigestEntry{{ContextSelected: &falseValue}} },
		"description":              func(s *session.Session) { s.Digest = []session.DigestEntry{{ContextDescription: &text}} },
		"non-Grok root":            func(s *session.Session) { s.Agent = "codex"; s.UnattributedTokens = &session.ModelTokens{} },
		"non-Grok child": func(s *session.Session) {
			s.Agent = "codex"
			s.Subagents = []session.Subagent{{ID: "child", UnattributedTokens: &session.ModelTokens{}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := session.Session{Agent: "grok", ID: "root", StartedAt: 1, LastActivityTime: 2}
			if _, err := FromSession("local", value); err != nil {
				t.Fatalf("valid control rejected: %v", err)
			}
			mutate(&value)
			if _, err := FromSession("local", value); err == nil {
				t.Fatal("unrepresentable fact accepted")
			}
		})
	}
}

func TestCloneParsedFamilyPreservesIndependentSourceCostPresence(t *testing.T) {
	original := &vendors.ParsedSession{
		Session: &session.Session{}, CostIncomplete: true,
		RecordedModelCosts: map[string]float64{"positive": 1, "zero": 0},
	}
	cloned := cloneParsedFamily([]*vendors.ParsedSession{original})[0]
	if !cloned.CostIncomplete || !reflect.DeepEqual(cloned.RecordedModelCosts, original.RecordedModelCosts) {
		t.Fatalf("source cost provenance lost: %+v", cloned)
	}
	cloned.RecordedModelCosts["zero"] = 2
	delete(cloned.RecordedModelCosts, "positive")
	if original.RecordedModelCosts["zero"] != 0 || original.RecordedModelCosts["positive"] != 1 {
		t.Fatal("clone changed source cost provenance")
	}
}

func TestGrokCounterlessModelCostRejectsWithoutHidingRoot(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cost  float64
		mixed bool
	}{
		{"negative", -1, false}, {"positive", 1, false}, {"zero", 0, false}, {"mixed", 2, true},
	} {
		for _, member := range []string{"root", "child"} {
			for _, aggregate := range []string{"absent", "known"} {
				t.Run(tc.name+"/"+member+"/aggregate-"+aggregate, func(t *testing.T) {
					parsed := []*vendors.ParsedSession{
						{Session: &session.Session{Agent: "grok", ID: "root", StartedAt: 1, LastActivityTime: 2, SessionDetails: session.SessionDetails{Turns: 1}}},
						{ParentID: "root", Session: &session.Session{Agent: "grok", ID: "child", StartedAt: 1, LastActivityTime: 2, SessionDetails: session.SessionDetails{Turns: 1}}},
					}
					for _, item := range parsed {
						if item.Session.ID != member {
							continue
						}
						item.RecordedModelCosts = map[string]float64{"grok-4.7": tc.cost}
						if tc.mixed {
							item.Session.Tokens = map[string]session.ModelTokens{"gpt-5": {InputTokens: 12}}
							item.RecordedModelCosts["gpt-5"] = 1
						}
						if aggregate == "known" {
							cost := 3.0
							item.RecordedCost = &cost
						}
					}
					records, servable, err := FromParsedFamilyWithServabilityContext(t.Context(), "root", "local", "grok", vendors.LocalReadSource, parsed, vendors.EmptySessionMetadata())
					if err == nil || !servable || len(records) != 0 {
						t.Fatalf("records=%d servable=%v err=%v", len(records), servable, err)
					}
				})
			}
		}
	}
}
