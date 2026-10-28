package fullsessionrecord

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestPiRecordAdapterRoundTripsAccountingAvailability(t *testing.T) {
	zero, maximum := int64(0), fullsessionv1.MaxCostMicroUSD
	for _, usage := range [][]fullsessionv1.ModelUsage{nil, {}, {{Model: "model", InputTokens: 9007199254740993}}, {{Model: "model", CostMicroUSD: &zero}}, {{Model: "model", CostMicroUSD: &maximum}}} {
		for _, componentCost := range []*int64{nil, &zero, &maximum} {
			for _, aggregateCost := range []*int64{nil, &zero, &maximum} {
				record, err := fullsessionv1.Freeze(fullsessionv1.Record{SourceID: "local", Agent: "pi", SessionID: "s", Session: fullsessionv1.Session{
					StartedAtMs: 1, LastActivityAtMs: 2, Usage: usage, CostMicroUSD: aggregateCost,
					UnattributedTokens: &fullsessionv1.UnattributedUsage{InputTokens: 7, CostMicroUSD: componentCost},
				}})
				if err != nil {
					t.Fatal(err)
				}
				value, err := ToSession(record)
				if err != nil {
					t.Fatal(err)
				}
				again, err := FromSession("local", *value)
				if err != nil {
					t.Fatal(err)
				}
				a, _ := fullsessionv1.Marshal(record)
				b, _ := fullsessionv1.Marshal(again)
				if !bytes.Equal(a, b) {
					t.Fatalf("accounting changed:\n%s\n%s", a, b)
				}
			}
		}
	}
	for _, known := range []bool{false, true} {
		value := session.Session{Agent: "pi", ID: "s", StartedAt: 1, LastActivityTime: 2, TokensKnown: known, TokensUnavailable: !known, CostUnavailable: true}
		record, err := FromSession("local", value)
		if err != nil || (record.Session.Usage != nil) != known {
			t.Fatalf("zero/unknown collapsed: %+v, %v", record.Session, err)
		}
		restored, err := ToSession(record)
		if err != nil || restored.TokensKnown != known || restored.TokensUnavailable == known || !restored.CostUnavailable {
			t.Fatalf("availability lost: %+v, %v", restored, err)
		}
	}
}

func TestPublishedRecordsRetainCanonicalBytesThroughAdapter(t *testing.T) {
	for _, name := range []string{"codex", "grok", "skills", "pull-request-links", "pi", "pi-unavailable", "pi-zero"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "fullsession", "v1", "testdata", "fixtures", "valid", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			record, err := fullsessionv1.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			value, err := ToSession(record)
			if err != nil {
				t.Fatal(err)
			}
			again, err := FromSession(record.SourceID, *value)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := fullsessionv1.Marshal(again)
			if err != nil || !bytes.Equal(data, encoded) {
				t.Fatalf("canonical record changed: %v", err)
			}
		})
	}
}

func TestPiAdapterRejectsContradictoryAvailability(t *testing.T) {
	for name, mutate := range map[string]func(*session.Session){
		"unavailable with model": func(s *session.Session) {
			s.TokensUnavailable = true
			s.Tokens = map[string]session.ModelTokens{"m": {InputTokens: 1}}
		},
		"unavailable with unattributed": func(s *session.Session) {
			s.TokensUnavailable = true
			s.UnattributedTokens = &session.ModelTokens{InputTokens: 1}
		},
		"unavailable and known":      func(s *session.Session) { s.TokensUnavailable, s.TokensKnown = true, true },
		"unavailable cost":           func(s *session.Session) { cost := 0.0; s.CostUnavailable, s.Cost = true, &cost },
		"unmarked model cost":        func(s *session.Session) { s.Tokens = map[string]session.ModelTokens{"m": {Cost: 1}} },
		"unmarked unattributed cost": func(s *session.Session) { s.UnattributedTokens = &session.ModelTokens{Cost: 1} },
		"NaN component": func(s *session.Session) {
			s.Tokens = map[string]session.ModelTokens{"m": {CostKnown: true, Cost: math.NaN()}}
		},
		"negative component": func(s *session.Session) { s.UnattributedTokens = &session.ModelTokens{CostKnown: true, Cost: -1e-10} },
	} {
		t.Run(name, func(t *testing.T) {
			value := session.Session{Agent: "pi", ID: "s", StartedAt: 1, LastActivityTime: 2}
			mutate(&value)
			if _, err := FromSession("local", value); err == nil {
				t.Fatal("contradictory accounting accepted")
			}
		})
	}
}

func TestPiAdapterPreservesSelectedContextInterpretation(t *testing.T) {
	yes, no, replacement := true, false, ""
	record, err := fullsessionv1.Freeze(fullsessionv1.Record{SourceID: "local", Agent: "pi", SessionID: "s", Session: fullsessionv1.Session{
		StartedAtMs: 1, LastActivityAtMs: 2,
		Digest: []fullsessionv1.DigestEntry{
			{Turn: 1, Description: "removed", Active: &yes, ContextSelected: &no},
			{Turn: 2, Description: "original", Active: &no, Inherited: &yes, ContextSelected: &yes, ContextDescription: &replacement},
			{Turn: 3, Description: "legacy"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	value, err := ToSession(record)
	if err != nil {
		t.Fatal(err)
	}
	selected := session.SelectedDigest(value.Digest)
	if len(selected) != 2 || selected[0].Turn != 2 || selected[0].Description != "" || selected[1].Turn != 3 || selected[1].Description != "legacy" {
		t.Fatalf("wrong selected context: %+v", selected)
	}
	if len(value.Digest) != 3 || value.Digest[1].Description != "original" || record.Session.Digest[1].Description != "original" {
		t.Fatal("selected context overwrote chronological history")
	}
}
