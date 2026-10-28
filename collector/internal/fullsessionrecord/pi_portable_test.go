package fullsessionrecord

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/pi"
)

func TestPiPortableRevisionExcludesRuntimeEntrypoint(t *testing.T) {
	tui, rpc := "pi-tui", "pi-rpc"
	for _, initial := range []*string{nil, &tui, &rpc} {
		selected := true
		parsed := []*vendors.ParsedSession{{Session: &session.Session{
			Agent: "pi", ID: "s", StartedAt: 1, LastActivityTime: 2, Entrypoint: initial,
			SessionDetails: session.SessionDetails{Turns: 1, Digest: []session.DigestEntry{{Description: "prompt", ContextSelected: &selected}}},
		}}}
		before := session.Clone(parsed[0].Session)
		var canonical []byte
		var revision string
		for _, modality := range []string{"absent", "pi-tui", "pi-rpc", "conflicting"} {
			metadata := vendors.EmptySessionMetadata()
			if modality != "absent" {
				metadata.Session("s").Live = "idle"
				if modality != "conflicting" {
					metadata.Session("s").Entrypoint = modality
				}
			}
			metadataBefore, err := json.Marshal(metadata)
			if err != nil {
				t.Fatal(err)
			}
			records, err := FromParsedFamily("local", "pi", vendors.LocalReadSource, parsed, metadata)
			if err != nil || len(records) != 1 {
				t.Fatalf("%s: records=%d err=%v", modality, len(records), err)
			}
			if records[0].Session.Entrypoint != nil {
				t.Fatalf("%s: runtime modality entered portable content", modality)
			}
			data, err := fullsessionv1.Marshal(records[0])
			if err != nil {
				t.Fatal(err)
			}
			if canonical != nil && !bytes.Equal(canonical, data) {
				t.Fatalf("%s: runtime modality changed canonical revision", modality)
			}
			canonical, revision = data, records[0].RevisionID
			metadataAfter, err := json.Marshal(metadata)
			if err != nil || !bytes.Equal(metadataBefore, metadataAfter) || !reflect.DeepEqual(before, parsed[0].Session) {
				t.Fatalf("%s: composition mutated caller inputs: %v", modality, err)
			}
		}
		*parsed[0].Session.Digest[0].ContextSelected = false
		changed, err := FromParsedFamily("local", "pi", vendors.LocalReadSource, parsed, nil)
		if err != nil || len(changed) != 1 {
			t.Fatalf("changed context: records=%d err=%v", len(changed), err)
		}
		data, err := fullsessionv1.Marshal(changed[0])
		if err != nil || bytes.Equal(canonical, data) || changed[0].RevisionID == revision {
			t.Fatalf("context selection did not change canonical revision: %v", err)
		}
	}
}

func TestPortableCompositionPreservesOtherVendorEntrypoints(t *testing.T) {
	for _, agent := range []string{"codex", "claude", "cursor", "opencode", "grok"} {
		t.Run(agent, func(t *testing.T) {
			metadata := vendors.EmptySessionMetadata()
			metadata.Session("s").Entrypoint = "sdk"
			parsed := []*vendors.ParsedSession{{Session: &session.Session{Agent: agent, ID: "s", StartedAt: 1, LastActivityTime: 2, SessionDetails: session.SessionDetails{Turns: 1}}}}
			records, err := FromParsedFamily("local", agent, vendors.LocalReadSource, parsed, metadata)
			if err != nil || len(records) != 1 {
				t.Fatalf("records=%d err=%v", len(records), err)
			}
			if records[0].Session.Entrypoint == nil || *records[0].Session.Entrypoint != "sdk" || parsed[0].Session.Entrypoint != nil || metadata.Lookup("s").Entrypoint != "sdk" {
				t.Fatal("vendor entrypoint lost or caller inputs changed")
			}
		})
	}
}

func TestPiPortableCompositionPreservesAccountingAndContext(t *testing.T) {
	active, selected, inherited, description, cost := false, true, true, "selected after compaction", 0.25
	value := &session.Session{Agent: "pi", ID: "fork", StartedAt: 1, LastActivityTime: 2, TokensKnown: true,
		Tokens:             map[string]session.ModelTokens{"provider/model": {InputTokens: 12}},
		UnattributedTokens: &session.ModelTokens{OutputTokens: 3}, Cost: &cost,
		SessionDetails: session.SessionDetails{Turns: 1, DetailsIncomplete: true, Skills: []session.Skill{{Name: "review", Content: "body"}},
			Digest: []session.DigestEntry{{Turn: 1, Category: session.DigestUser, Description: "prompt", SourceEntryID: "e1", ParentEntryID: "p1", BranchID: "b1", Inherited: &inherited, Active: &active, ContextSelected: &selected, ContextDescription: &description}},
		},
	}
	value.SetPullRequestLinks([]session.PullRequestLink{{URL: "https://github.com/acme/api/pull/2", Repository: "acme/api", Number: 2}})
	before := session.Clone(value)
	parsed := []*vendors.ParsedSession{{Session: value, RecordedCost: &cost}}
	records, err := FromParsedFamily("local", "pi", vendors.LocalReadSource, parsed, vendors.EmptySessionMetadata())
	if err != nil || len(records) != 1 {
		t.Fatalf("records=%d err=%v", len(records), err)
	}
	if !reflect.DeepEqual(value, before) {
		t.Fatal("composition mutated input")
	}
	record := records[0]
	if record.ParentSessionID != "" || len(record.Session.Subagents) != 0 || len(record.Session.Usage) != 1 || record.Session.Usage[0].CostMicroUSD != nil || record.Session.UnattributedTokens.CostMicroUSD != nil || *record.Session.CostMicroUSD != 250000 {
		t.Fatalf("invented lineage or accounting: %+v", record.Session)
	}
	restored, err := ToSession(record)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.TokensKnown || restored.TokensUnavailable || restored.CostUnavailable || !restored.DetailsIncomplete || !reflect.DeepEqual(restored.Digest, before.Digest) || !reflect.DeepEqual(restored.Skills, before.Skills) || !reflect.DeepEqual(restored.PullRequestLinks, before.PullRequestLinks) {
		t.Fatalf("portable facts lost: %+v", restored)
	}
	again, err := FromSession("local", *restored)
	if err != nil || again.RevisionID != record.RevisionID {
		t.Fatalf("round trip: %v", err)
	}
	*restored.Digest[0].Active = true
	changed, err := FromSession("local", *restored)
	if err != nil || changed.RevisionID == record.RevisionID || *record.Session.Digest[0].Active {
		t.Fatalf("context revision/ownership: %v", err)
	}
	remote, err := FromParsedFamily("remote", "pi", struct{ vendors.ReadSource }{vendors.LocalReadSource}, parsed, vendors.EmptySessionMetadata())
	if err != nil || len(remote) != 0 {
		t.Fatalf("Pi entered SSH composition: %v", err)
	}
}

func TestPiCollectedForksComposeAsIndependentRecords(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", root)
	t.Setenv("COSLASH_PI_SESSION_ROOTS", "")
	t.Setenv("COSLASH_HOME", t.TempDir())
	shared := []string{
		`{"type":"message","id":"u","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":"synthetic prompt"}}`,
		`{"type":"message","id":"a","parentId":"u","timestamp":"2026-01-01T00:00:02Z","message":{"role":"assistant","model":"m","content":"response","usage":{"input":10,"output":2,"cacheRead":0,"cacheWrite":0,"cost":{"total":1}}}}`,
		`{"type":"usage","id":"usage","parentId":"a","timestamp":"2026-01-01T00:00:03Z","usage":{"input":7,"output":0,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.5}}}`,
	}
	parentPath := filepath.Join(root, "parent.jsonl")
	for _, id := range []string{"parent", "fork", "unverified"} {
		parent := ""
		rows := append([]string(nil), shared...)
		if id != "parent" {
			parent = parentPath
			rows = append(rows, `{"type":"message","id":"new","parentId":"usage","timestamp":"2026-01-01T00:00:04Z","message":{"role":"assistant","model":"m","content":"fork response","usage":{"input":3,"output":1,"cacheRead":0,"cacheWrite":0,"cost":{"total":2}}}}`)
		}
		if id == "unverified" {
			parent = filepath.Join(root, "missing.jsonl")
		}
		header, err := json.Marshal(map[string]any{"type": "session", "id": id, "version": 3, "timestamp": "2026-01-01T00:00:00Z", "cwd": root, "parentSession": parent})
		if err != nil {
			t.Fatal(err)
		}
		body := string(header) + "\n" + strings.Join(rows, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	parsed, metadata, err := pi.CollectContext(t.Context(), 0)
	if err != nil || len(parsed) != 3 {
		t.Fatalf("collected=%d err=%v", len(parsed), err)
	}
	records, err := FromParsedFamily("local", "pi", vendors.LocalReadSource, parsed, metadata)
	if err != nil || len(records) != 3 {
		t.Fatalf("records=%d err=%v", len(records), err)
	}
	for _, record := range records {
		if record.ParentSessionID != "" || len(record.Session.Subagents) != 0 {
			t.Fatal("fork hidden as a subagent")
		}
		switch record.SessionID {
		case "parent":
			if record.Session.Usage[0].InputTokens != 10 || record.Session.UnattributedTokens.InputTokens != 7 || *record.Session.CostMicroUSD != 1500000 {
				t.Fatalf("parent accounting: %+v", record.Session)
			}
		case "fork":
			if record.Session.Usage[0].InputTokens != 3 || record.Session.UnattributedTokens != nil || *record.Session.CostMicroUSD != 2000000 {
				t.Fatalf("inherited usage counted: %+v", record.Session)
			}
		case "unverified":
			if record.Session.Usage != nil || record.Session.UnattributedTokens != nil || record.Session.CostMicroUSD != nil {
				t.Fatal("unverified inheritance became zero or known accounting")
			}
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if records, err := FromParsedFamilyContext(ctx, "local", "pi", vendors.LocalReadSource, parsed, metadata); err != context.Canceled || records != nil {
		t.Fatalf("cancellation: records=%d err=%v", len(records), err)
	}
}
