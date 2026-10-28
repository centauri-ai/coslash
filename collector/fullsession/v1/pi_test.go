package fullsessionv1

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPiAccountingKnownZeroAndUnavailable(t *testing.T) {
	for _, usage := range [][]ModelUsage{nil, {}, {{Model: "provider/model", InputTokens: 9007199254740993}}} {
		for _, unattributed := range []*UnattributedUsage{nil, {InputTokens: 7}} {
			record, err := Freeze(Record{SourceID: "local", Agent: "pi", SessionID: "fork", Session: Session{
				StartedAtMs: 1, LastActivityAtMs: 2, Usage: usage, UnattributedTokens: unattributed,
			}})
			if err != nil {
				t.Fatal(err)
			}
			data, err := Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			if (decoded.Session.Usage == nil) != (usage == nil) || decoded.Session.CostMicroUSD != nil {
				t.Fatal("unknown and zero accounting collapsed")
			}
			if len(usage) != 0 && decoded.Session.Usage[0].InputTokens != 9007199254740993 {
				t.Fatal("integer precision lost")
			}
			if len(usage) != 0 && decoded.Session.Usage[0].CostMicroUSD != nil {
				t.Fatal("unknown cost became zero")
			}
			var roundTrip Record
			if err := json.Unmarshal(data, &roundTrip); err != nil {
				t.Fatal(err)
			}
			again, err := Marshal(roundTrip)
			if err != nil || !bytes.Equal(data, again) {
				t.Fatalf("canonical round trip: %v", err)
			}
		}
	}
}

func TestPiContextSnapshotIsImmutableAndRevisionBound(t *testing.T) {
	yes, no, description, cost := true, false, "", int64(0)
	input := Record{SourceID: "local", Agent: "pi", SessionID: "fork", Session: Session{
		StartedAtMs: 1, LastActivityAtMs: 2, DetailsIncomplete: true,
		Usage: []ModelUsage{{Model: "model", CostMicroUSD: &cost}},
		Digest: []DigestEntry{
			{Turn: 1, TimeMs: 1, SourceEntryID: "e1", ParentEntryID: "p1", BranchID: "b1", Inherited: &yes, Active: &no, ContextSelected: &yes, ContextDescription: &description},
			{Turn: 2, TimeMs: 2, SourceEntryID: "e2", ParentEntryID: "e1", BranchID: "b2", Inherited: &no, Active: &yes, ContextSelected: &no},
		},
	}}
	frozen, err := Freeze(input)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{`"detailsIncomplete":true`, `"inherited":true,"active":false,"contextSelected":true,"contextDescription":""`, `"sourceEntryId":"e2","parentEntryId":"e1","branchId":"b2"`} {
		if !bytes.Contains(data, []byte(text)) {
			t.Fatalf("annotation lost: %s", text)
		}
	}
	input.Session.Digest[0].SourceEntryID = "changed"
	*input.Session.Digest[0].Active = true
	*input.Session.Digest[0].ContextSelected = false
	*input.Session.Digest[0].Inherited = false
	*input.Session.Digest[0].ContextDescription = "changed"
	*input.Session.Usage[0].CostMicroUSD = 1
	again, err := Marshal(frozen)
	if err != nil || !bytes.Equal(data, again) {
		t.Fatalf("Freeze aliases caller: %v", err)
	}
	decoded, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	*decoded.Session.Digest[0].ContextSelected = false
	changed, err := Freeze(decoded)
	if err != nil || changed.RevisionID == frozen.RevisionID {
		t.Fatalf("context selection not revision-bound: %v", err)
	}
}

func TestPiValidityBoundariesPreserveOtherAgents(t *testing.T) {
	for name, mutate := range map[string]func(*Record){
		"fork as subagent":   func(r *Record) { r.ParentSessionID = "parent" },
		"extension subagent": func(r *Record) { r.Session.Subagents = []Subagent{{ID: "child"}} },
		"source path":        func(r *Record) { r.Session.Digest = []DigestEntry{{SourceEntryID: "/private/transcript"}} },
		"parent whitespace":  func(r *Record) { r.Session.Digest = []DigestEntry{{ParentEntryID: "bad id"}} },
		"branch oversize":    func(r *Record) { r.Session.Digest = []DigestEntry{{BranchID: strings.Repeat("b", 513)}} },
		"description UTF8":   func(r *Record) { text := "\xff"; r.Session.Digest = []DigestEntry{{ContextDescription: &text}} },
		"description limit": func(r *Record) {
			text := strings.Repeat("x", MaxStringBytes+1)
			r.Session.Digest = []DigestEntry{{ContextDescription: &text}}
		},
		"negative component cost": func(r *Record) { cost := int64(-1); r.Session.Usage = []ModelUsage{{Model: "m", CostMicroUSD: &cost}} },
		"component cost limit": func(r *Record) {
			cost := MaxCostMicroUSD + 1
			r.Session.UnattributedTokens = &UnattributedUsage{CostMicroUSD: &cost}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := Record{SourceID: "local", Agent: "pi", SessionID: "s", Session: Session{StartedAtMs: 1, LastActivityAtMs: 1}}
			mutate(&r)
			if _, err := Freeze(r); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid Pi record accepted: %v", err)
			}
		})
	}
	for _, agent := range []string{"codex", "claude", "cursor", "opencode", "grok"} {
		for _, field := range []string{"model cost", "completeness", "context"} {
			t.Run(agent+"/"+field, func(t *testing.T) {
				r := Record{SourceID: "local", Agent: agent, SessionID: "s", Session: Session{StartedAtMs: 1, LastActivityAtMs: 1}}
				switch field {
				case "model cost":
					r.Session.Usage = []ModelUsage{{Model: "model"}}
				case "completeness":
					r.Session.DetailsIncomplete = true
				case "context":
					no := false
					r.Session.Digest = []DigestEntry{{Active: &no}}
				}
				if _, err := Freeze(r); !errors.Is(err, ErrInvalid) {
					t.Fatalf("legacy constraint relaxed: %v", err)
				}
			})
		}
	}
}
