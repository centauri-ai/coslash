package collector

import (
	"reflect"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func parityFixture() ([]*vendors.ParsedSession, *vendors.SessionMetadata) {
	model, status := "gpt-5", "waiting"
	turn := 2
	parsed := []*vendors.ParsedSession{
		{Session: &session.Session{Agent: "codex", ID: "root", StartedAt: 10, LastActivityTime: 20, Tokens: map[string]session.ModelTokens{"gpt-5": {InputTokens: 4}}, SessionDetails: session.SessionDetails{Model: &model, Turns: 2}}, Name: "root name", StatusHint: &status, Spawns: map[string]vendors.SpawnState{"spawn": {Turn: &turn}}},
		{Session: &session.Session{Agent: "codex", ID: "child", StartedAt: 12, LastActivityTime: 25, Tokens: map[string]session.ModelTokens{}}, ParentID: "root", SpawnKey: "spawn", Name: "child name", Spawns: map[string]vendors.SpawnState{}, Commands: []session.SubagentCommand{{Label: "check tests"}}},
	}
	metadata := vendors.EmptySessionMetadata()
	metadata.Names["root"] = "metadata name"
	return parsed, metadata
}

func TestLocalSFTPAndHelperNormalizedFactsComposeEquivalentCards(t *testing.T) {
	directParsed, directMetadata := parityFixture()
	direct := ListRemote(vendors.LocalReadSource, map[string]vendors.RemoteCollection{"codex": {Sessions: directParsed, Metadata: directMetadata}}, 0)

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
