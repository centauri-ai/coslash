package fullsessionv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFreezeDoesNotMutateOrAliasInput(t *testing.T) {
	name, model := "original name", "original model"
	duration, turn := 10, 2
	record := Record{
		SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: Session{
			Name: &name, EditedFileCount: 1, StartedAtMs: 1, LastActivityAtMs: 2,
			Usage: []ModelUsage{}, Commands: []string{"original command"},
			Subagents: []Subagent{{
				ID: "child-1", Name: "child", Model: &model, Status: "returned",
				DurationMs: &duration, SpawnedAtTurn: &turn,
				Commands: []SubagentCommand{{Label: "check", Command: "go test ./..."}},
				Usage:    []ModelUsage{},
			}},
			FileEdits: []FileEdit{{
				Path: "main.go", Edits: 1,
				Changes: []FileChange{{Kind: "content", Text: "original body", Operation: "Write"}},
			}},
			Synthesis: &SessionSynthesis{Goals: []string{"original goal"}, KeyDecisions: []string{}},
		},
	}

	frozen, err := Freeze(record)
	if err != nil {
		t.Fatal(err)
	}
	if record.Session.FileEdits[0].Changes[0].ID != "" ||
		record.Session.FileEdits[0].Changes[0].ByteCount != 0 ||
		record.Session.FileEdits[0].Changes[0].SHA256 != "" {
		t.Fatalf("Freeze mutated input change: %#v", record.Session.FileEdits[0].Changes[0])
	}

	*record.Session.Name = "changed name"
	record.Session.Commands[0] = "changed command"
	*record.Session.Subagents[0].Model = "changed model"
	record.Session.Subagents[0].Commands[0].Command = "changed subagent command"
	record.Session.FileEdits[0].Changes[0].Text = "changed body"
	record.Session.Synthesis.Goals[0] = "changed goal"

	if *frozen.Session.Name != "original name" || frozen.Session.Commands[0] != "original command" ||
		*frozen.Session.Subagents[0].Model != "original model" ||
		frozen.Session.Subagents[0].Commands[0].Command != "go test ./..." ||
		frozen.Session.FileEdits[0].Changes[0].Text != "original body" ||
		frozen.Session.Synthesis.Goals[0] != "original goal" {
		t.Fatalf("frozen record aliases caller storage: %#v", frozen.Session)
	}
	if err := Validate(frozen); err != nil {
		t.Fatalf("caller mutation invalidated frozen record: %v", err)
	}
}

func TestFreezeValidationFailureDoesNotMutateInput(t *testing.T) {
	record := Record{
		SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: Session{
			EditedFileCount: 1,
			FileEdits: []FileEdit{{Path: "main.go", Edits: 1, Changes: []FileChange{{
				Kind: "content", Text: "body", Operation: "Write",
			}}}},
		},
	}
	if _, err := Freeze(record); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Freeze error = %v; want %v", err, ErrInvalid)
	}
	change := record.Session.FileEdits[0].Changes[0]
	if change.ID != "" || change.ByteCount != 0 || change.SHA256 != "" {
		t.Fatalf("failed Freeze mutated input change: %#v", change)
	}
}

func TestFreezeGeneratedChangeIDsSkipSuppliedIDs(t *testing.T) {
	record := Record{
		SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: Session{
			StartedAtMs: 1, LastActivityAtMs: 1, EditedFileCount: 2,
			FileEdits: []FileEdit{
				{Path: "first.go", Edits: 1, Changes: []FileChange{{Kind: "content", Text: "first", Operation: "Write"}}},
				{Path: "second.go", Edits: 1, Changes: []FileChange{{ID: "change-000000-000000", Kind: "content", Text: "second", Operation: "Write"}}},
			},
		},
	}

	frozen, err := Freeze(record)
	if err != nil {
		t.Fatal(err)
	}
	generated := frozen.Session.FileEdits[0].Changes[0].ID
	if generated != "change-000000-000000-000001" {
		t.Fatalf("generated ID = %q, want collision-free deterministic ID", generated)
	}
}

func TestDecodeRejectsOversizedCollectionBeforeTypedDecode(t *testing.T) {
	data := make([]byte, 0, 3*MaxItems+4)
	data = append(data, '[')
	data = append(data, bytes.Repeat([]byte("{},"), MaxItems)...)
	data = append(data, '{', '}', ']')

	if _, err := Decode(data); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Decode error = %v; want %v", err, ErrInvalid)
	}
}

func TestDecodeRejectsOversizedAggregateCollectionBeforeTypedDecode(t *testing.T) {
	items := strings.Repeat("{},", MaxItems/2-1) + "{}"
	data := []byte("[[" + items + "],[" + items + "]]")

	if err := validateCollectionSizes(data); !errors.Is(err, ErrInvalid) {
		t.Fatalf("validateCollectionSizes error = %v; want %v", err, ErrInvalid)
	}
}

func TestFreezeRejectsOversizedAggregateCollection(t *testing.T) {
	usage := make([]ModelUsage, MaxItems)
	for index := range usage {
		usage[index].Model = fmt.Sprintf("model-%06d", index)
	}
	record := Record{
		SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: Session{
			StartedAtMs: 1, LastActivityAtMs: 1,
			Usage: usage, Commands: []string{"go test ./..."},
		},
	}

	if _, err := Freeze(record); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Freeze error = %v; want %v", err, ErrInvalid)
	}
}

func TestValidCollectionSizesCountsSubagentCommandOutputs(t *testing.T) {
	output := "result"
	session := Session{
		Subagents: []Subagent{{Commands: []SubagentCommand{{Output: &output}}}},
		Synthesis: &SessionSynthesis{Goals: make([]string, MaxItems-2)},
	}
	if validCollectionSizes(session) {
		t.Fatal("aggregate with a subagent command output exceeded the item limit without rejection")
	}

	session.Subagents[0].Commands[0].Output = nil
	if !validCollectionSizes(session) {
		t.Fatal("aggregate without a subagent command output did not fit the item limit")
	}
}

func TestFreezeRejectsUnsortedUsage(t *testing.T) {
	record := Record{
		SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: Session{StartedAtMs: 1, LastActivityAtMs: 1, Usage: []ModelUsage{
			{Model: "z-model"}, {Model: "a-model"},
		}},
	}

	if _, err := Freeze(record); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Freeze error = %v; want %v", err, ErrInvalid)
	}
}

func TestFreezeRejectsUnsortedSubagentUsage(t *testing.T) {
	record := Record{
		SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: Session{StartedAtMs: 1, LastActivityAtMs: 1, Subagents: []Subagent{{
			ID: "subagent-1", Usage: []ModelUsage{{Model: "z-model"}, {Model: "a-model"}},
		}}},
	}

	if _, err := Freeze(record); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Freeze error = %v; want %v", err, ErrInvalid)
	}
}

func TestFreezeRejectsCostAboveExactAdapterLimit(t *testing.T) {
	record := Record{
		SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: Session{StartedAtMs: 1, LastActivityAtMs: 1, Usage: []ModelUsage{{
			Model: "gpt-5", CostMicroUSD: MaxCostMicroUSD + 1,
		}}},
	}
	if _, err := Freeze(record); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Freeze error = %v; want %v", err, ErrInvalid)
	}
}

func TestValidateRejectsOversizedRecord(t *testing.T) {
	commands := make([]string, MaxItems)
	command := strings.Repeat("x", MaxRecordBytes/MaxItems+1)
	for index := range commands {
		commands[index] = command
	}
	record := Record{
		SchemaVersion: SchemaVersion, SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: Session{StartedAtMs: 1, LastActivityAtMs: 1, Commands: commands},
	}
	preimage, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(preimage)
	record.RevisionID = hex.EncodeToString(digest[:])

	if err := Validate(record); !errors.Is(err, ErrOversized) {
		t.Fatalf("Validate error = %v; want %v", err, ErrOversized)
	}
}

func TestGrokRecordAdmission(t *testing.T) {
	for _, agent := range []string{"codex", "claude", "cursor", "opencode", "grok", "pi", "Grok", "unknown"} {
		t.Run(agent, func(t *testing.T) {
			_, err := Freeze(Record{SourceID: "local", Agent: agent, SessionID: "root", Session: Session{StartedAtMs: 1, LastActivityAtMs: 2}})
			valid := agent == "codex" || agent == "claude" || agent == "cursor" || agent == "opencode" || agent == "grok"
			if (err == nil) != valid {
				t.Fatalf("agent %q: error = %v, want valid=%v", agent, err, valid)
			}
		})
	}
}

func TestGrokUnattributedCanonicalAccounting(t *testing.T) {
	for _, accounting := range []string{
		`{"inputTokens":12,"outputTokens":3,"cacheCreationInputTokens":0,"cacheCreation1hInputTokens":0,"cacheReadInputTokens":4,"costMicroUsd":null}`,
		`{"inputTokens":0,"outputTokens":0,"cacheCreationInputTokens":0,"cacheCreation1hInputTokens":0,"cacheReadInputTokens":0,"costMicroUsd":0}`,
		`{"inputTokens":9223372036854775807,"outputTokens":0,"cacheCreationInputTokens":0,"cacheCreation1hInputTokens":0,"cacheReadInputTokens":0,"costMicroUsd":4294967295999999}`,
	} {
		t.Run(accounting, func(t *testing.T) {
			record := Record{SourceID: "local", Agent: "grok", SessionID: "root"}
			body := `{"startedAtMs":1,"lastActivityAtMs":2,"unattributedTokens":` + accounting + `,"subagents":[{"id":"child","unattributedTokens":` + accounting + `}]}`
			if err := json.Unmarshal([]byte(body), &record.Session); err != nil {
				t.Fatal(err)
			}
			var used UnattributedUsage
			if err := json.Unmarshal([]byte(accounting), &used); err != nil {
				t.Fatal(err)
			}
			record.Session.CostMicroUSD = used.CostMicroUSD
			record.Session.Subagents[0].CostMicroUSD = used.CostMicroUSD
			frozen, err := Freeze(record)
			if err != nil {
				t.Fatal(err)
			}
			data, err := Marshal(frozen)
			if err != nil {
				t.Fatal(err)
			}
			if got := bytes.Count(data, []byte(`"unattributedTokens":`+accounting)); got != 2 {
				t.Fatalf("accounting occurrences = %d, want root and child", got)
			}
			decoded, err := Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			roundTrip, err := Marshal(decoded)
			if err != nil || !bytes.Equal(data, roundTrip) {
				t.Fatalf("canonical round trip changed: %v", err)
			}
		})
	}
}

func TestGrokRejectsInvalidUnattributedAccounting(t *testing.T) {
	for _, field := range []string{"inputTokens", "outputTokens", "cacheCreationInputTokens", "cacheCreation1hInputTokens", "cacheReadInputTokens", "costMicroUsd"} {
		for _, child := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/child=%v", field, child), func(t *testing.T) {
				record := Record{SourceID: "local", Agent: "grok", SessionID: "root", Session: Session{StartedAtMs: 1, LastActivityAtMs: 2}}
				accounting := `{"` + field + `":-1}`
				body := `{"unattributedTokens":` + accounting + `}`
				if child {
					body = `{"subagents":[{"id":"child","unattributedTokens":` + accounting + `}]}`
				}
				if err := json.Unmarshal([]byte(body), &record.Session); err != nil {
					t.Fatal(err)
				}
				if _, err := Freeze(record); !errors.Is(err, ErrInvalid) {
					t.Fatalf("invalid accounting accepted: %v", err)
				}
			})
		}
	}
}

func TestUnattributedAccountingBoundariesAndClone(t *testing.T) {
	cost := MaxCostMicroUSD
	record := Record{SourceID: "local", Agent: "grok", SessionID: "root", Session: Session{
		StartedAtMs: 1, LastActivityAtMs: 2, CostMicroUSD: &cost, UnattributedTokens: &UnattributedUsage{InputTokens: 12, CostMicroUSD: &cost},
		Subagents: []Subagent{{ID: "child", CostMicroUSD: &cost, UnattributedTokens: &UnattributedUsage{InputTokens: 7, CostMicroUSD: &cost}}},
	}}
	frozen, err := Freeze(record)
	if err != nil {
		t.Fatal(err)
	}
	record.Session.UnattributedTokens.InputTokens = 99
	*record.Session.UnattributedTokens.CostMicroUSD = 0
	record.Session.Subagents[0].UnattributedTokens.InputTokens = 99
	if frozen.Session.UnattributedTokens.InputTokens != 12 || *frozen.Session.UnattributedTokens.CostMicroUSD != MaxCostMicroUSD || frozen.Session.Subagents[0].UnattributedTokens.InputTokens != 7 || *frozen.Session.Subagents[0].UnattributedTokens.CostMicroUSD != MaxCostMicroUSD {
		t.Fatal("Freeze aliases unattributed accounting")
	}
	data, err := Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []struct{ old, new string }{
		{`"inputTokens":12`, `"inputTokens":9223372036854775808`},
		{`"costMicroUsd":4294967295999999`, `"costMicroUsd":4294967296000000`},
		{`"costMicroUsd":4294967295999999`, `"costMicroUsd":-1`},
		{`"unattributedTokens":{`, `"unattributedTokens":{"model":"invented",`},
	} {
		if _, err := Decode(bytes.Replace(data, []byte(replacement.old), []byte(replacement.new), 1)); err == nil {
			t.Fatalf("accepted invalid accounting %s", replacement.new)
		}
	}
	for _, agent := range []string{"codex", "claude", "cursor", "opencode", "pi"} {
		frozen.Agent = agent
		if _, err := Freeze(frozen); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s accepted unattributed accounting: %v", agent, err)
		}
	}
}

func TestGrokUnattributedRequiresConsistentCostAndExclusiveUsage(t *testing.T) {
	zero, other := int64(0), int64(1)
	for _, child := range []bool{false, true} {
		for _, bad := range []struct {
			inner, outer *int64
			attributed   []ModelUsage
		}{
			{inner: &zero}, {outer: &zero}, {inner: &zero, outer: &other},
			{attributed: []ModelUsage{{Model: "grok-4.7"}}},
		} {
			record := Record{SourceID: "local", Agent: "grok", SessionID: "root", Session: Session{StartedAtMs: 1, LastActivityAtMs: 2}}
			used := &UnattributedUsage{InputTokens: 12, CostMicroUSD: bad.inner}
			if child {
				record.Session.Subagents = []Subagent{{ID: "child", UnattributedTokens: used, CostMicroUSD: bad.outer, Usage: bad.attributed}}
			} else {
				record.Session.UnattributedTokens = used
				record.Session.CostMicroUSD = bad.outer
				record.Session.Usage = bad.attributed
			}
			if _, err := Freeze(record); !errors.Is(err, ErrInvalid) {
				t.Fatalf("child=%v: inconsistent accounting accepted: %+v", child, bad)
			}
		}
	}
}

func TestGrokUnattributedRejectsCostOverMaximum(t *testing.T) {
	cost := MaxCostMicroUSD + 1
	for _, child := range []bool{false, true} {
		record := Record{SourceID: "local", Agent: "grok", SessionID: "root", Session: Session{StartedAtMs: 1, LastActivityAtMs: 2}}
		used := &UnattributedUsage{InputTokens: 12, CostMicroUSD: &cost}
		if child {
			record.Session.Subagents = []Subagent{{ID: "child", UnattributedTokens: used, CostMicroUSD: &cost}}
		} else {
			record.Session.UnattributedTokens = used
			record.Session.CostMicroUSD = &cost
		}
		if _, err := Freeze(record); !errors.Is(err, ErrInvalid) {
			t.Fatalf("child=%v accepted cost over maximum: %v", child, err)
		}
	}
}
