package collector

import (
	"io"
	"io/fs"
	"reflect"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

type unavailableReadSource struct{}

func (unavailableReadSource) Open(string) (io.ReadCloser, error)    { return nil, fs.ErrNotExist }
func (unavailableReadSource) ReadDir(string) ([]fs.DirEntry, error) { return nil, fs.ErrNotExist }
func (unavailableReadSource) Stat(string) (fs.FileInfo, error)      { return nil, fs.ErrNotExist }

func parityFixture() ([]*vendors.ParsedSession, *vendors.SessionMetadata) {
	model, status := "gpt-5", "waiting"
	contextTokens, contextWindow, cost := 12, 128_000, 1.25
	turn := 2
	parsed := []*vendors.ParsedSession{
		{Session: &session.Session{Agent: "codex", ID: "root", StartedAt: 10, LastActivityTime: 20, Tokens: map[string]session.ModelTokens{"gpt-5": {InputTokens: 4}}, SessionDetails: session.SessionDetails{Model: &model, Turns: 2}}, Name: "root name", StatusHint: &status, Spawns: map[string]vendors.SpawnState{"spawn": {Turn: &turn}}},
		{Session: &session.Session{Agent: "codex", ID: "child", StartedAt: 12, LastActivityTime: 25, Tokens: map[string]session.ModelTokens{}}, ParentID: "root", SpawnKey: "spawn", Name: "child name", Spawns: map[string]vendors.SpawnState{}, Commands: []session.SubagentCommand{{Label: "check tests"}}},
	}
	metadata := vendors.EmptySessionMetadata()
	metadata.Session("root").Name = "metadata name"
	metadata.Session("root").Summary = "metadata summary"
	metadata.Session("root").Entrypoint = "sdk"
	metadata.Session("root").WorkingDirectory = "/workspace"
	metadata.Session("root").StartedAt = 5
	metadata.Session("root").LastActivityAt = 30
	metadata.Session("root").Model = "metadata-model"
	metadata.Session("root").FileEdits = []session.FileEdit{{Path: "main.go", Additions: 2}}
	metadata.Session("root").PullRequests = 3
	metadata.Session("root").Usage = vendors.SessionUsage{
		Tokens:        map[string]session.ModelTokens{"metadata-model": {InputTokens: 7}},
		ContextTokens: &contextTokens, ContextWindow: &contextWindow, RecordedCost: &cost,
	}
	metadata.Session("child").Summary = "child metadata summary"
	metadata.Session("child").Name = "metadata child name"
	return parsed, metadata
}

func TestLocalSFTPAndHelperNormalizedFactsComposeEquivalentCards(t *testing.T) {
	directParsed, directMetadata := parityFixture()
	direct := ListRemote(vendors.LocalReadSource, map[string]vendors.RemoteCollection{"codex": {Sessions: directParsed, Metadata: directMetadata}}, 0)
	if len(direct) != 1 || direct[0].StartedAt != 5 || direct[0].LastActivityTime != 30 ||
		direct[0].WorkingDirectory != "/workspace" || direct[0].Summary == nil || *direct[0].Summary != "metadata summary" ||
		direct[0].Model == nil || *direct[0].Model != "metadata-model" || direct[0].PullRequests != 3 ||
		len(direct[0].FileEdits) != 1 || len(direct[0].Subagents) != 1 || direct[0].Subagents[0].Result != "child metadata summary" ||
		direct[0].Subagents[0].Name != "metadata child name" {
		t.Fatalf("direct enrichment = %#v", direct)
	}

	helperInput, helperMetadata := parityFixture()
	family, err := remotefacts.FromParsed("codex", "root", "parser-v1", remotefacts.StateComplete, "", helperInput, helperMetadata, []vendors.FileFingerprint{{Key: "opaque-root", Size: 100, ModifiedAtMs: 25}})
	if err != nil {
		t.Fatal(err)
	}
	normalized, metadata, err := family.Parsed()
	if err != nil {
		t.Fatal(err)
	}
	helper := ListRemote(vendors.LocalReadSource, map[string]vendors.RemoteCollection{"codex": {Sessions: normalized, Metadata: metadata}}, 0)
	if !reflect.DeepEqual(direct, helper) {
		t.Fatalf("direct/SFTP card = %#v\nhelper card = %#v", direct, helper)
	}
}

func TestListRemoteProjectsResolvedChildName(t *testing.T) {
	parsed := []*vendors.ParsedSession{
		{
			Session: &session.Session{
				Agent: "codex", ID: "root", StartedAt: 10, LastActivityTime: 20,
				Tokens: map[string]session.ModelTokens{}, SessionDetails: session.SessionDetails{
					Turns: 1, Digest: []session.DigestEntry{{Category: session.DigestSubagent, SpawnKey: "spawn"}},
				},
			},
			Spawns: map[string]vendors.SpawnState{"spawn": {}},
		},
		{
			Session: &session.Session{
				Agent: "codex", ID: "child", StartedAt: 11, LastActivityTime: 21,
				Tokens: map[string]session.ModelTokens{},
			},
			ParentID: "root", SpawnKey: "spawn", Spawns: map[string]vendors.SpawnState{},
		},
	}
	metadata := vendors.EmptySessionMetadata()
	metadata.Session("child").Name = "metadata child name"

	got := ListRemote(vendors.LocalReadSource, map[string]vendors.RemoteCollection{
		"codex": {Sessions: parsed, Metadata: metadata},
	}, 0)
	if len(got) != 1 || len(got[0].Subagents) != 1 || got[0].Subagents[0].Name != "metadata child name" ||
		len(got[0].Digest) != 1 || got[0].Digest[0].Description != "metadata child name" {
		t.Fatalf("resolved child projection = %#v", got)
	}
}

func TestListRemoteUsesLiveStatus(t *testing.T) {
	parsed := []*vendors.ParsedSession{
		{Session: &session.Session{Agent: "codex", ID: "root", StartedAt: 10, LastActivityTime: 20, Tokens: map[string]session.ModelTokens{}, SessionDetails: session.SessionDetails{Turns: 1}}, InTurn: true, Spawns: map[string]vendors.SpawnState{}},
		{Session: &session.Session{Agent: "codex", ID: "child", StartedAt: 11, LastActivityTime: 21, Tokens: map[string]session.ModelTokens{}}, ParentID: "root", InTurn: true, Spawns: map[string]vendors.SpawnState{}},
	}
	metadata := vendors.EmptySessionMetadata()
	metadata.Session("root").Live = "interactive"
	metadata.Session("child").Live = "interactive"

	got := ListRemote(vendors.LocalReadSource, map[string]vendors.RemoteCollection{
		"codex": {Sessions: parsed, Metadata: metadata},
	}, 0)
	if len(got) != 1 || got[0].Status == nil || *got[0].Status != "busy" ||
		len(got[0].Subagents) != 1 || got[0].Subagents[0].Status != session.SubagentRunning {
		t.Fatalf("live remote display = %#v", got)
	}
}

func TestListRemotePreservesWaitingWithoutAuthoritativeLiveness(t *testing.T) {
	waiting := "waiting"
	parsed := []*vendors.ParsedSession{{
		Session: &session.Session{
			Agent: "codex", ID: "root", Status: &waiting, StartedAt: 10, LastActivityTime: 20,
			Tokens: map[string]session.ModelTokens{}, SessionDetails: session.SessionDetails{Turns: 1},
		},
		Spawns: map[string]vendors.SpawnState{},
	}}

	got := ListRemote(unavailableReadSource{}, map[string]vendors.RemoteCollection{
		"codex": {Sessions: parsed, Metadata: vendors.EmptySessionMetadata()},
	}, 0)
	if len(got) != 1 || got[0].Status == nil || *got[0].Status != "waiting" {
		t.Fatalf("remote status without liveness = %#v; want waiting", got)
	}
}
