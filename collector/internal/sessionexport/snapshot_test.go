package sessionexport

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	snapshotv1 "github.com/centauri-ai/coslash/collector/snapshot/v1"
)

func TestBuildUsesExplicitAllowListAndStructuralRedaction(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "work", "coslash")
	repository := "github.com/centauri-ai/coslash"
	name := "Implement export"
	summary := "Never retain Authorization: Bearer prose-secret"
	firstPrompt := "Use TOKEN=prompt-secret only for this local test"
	model := "gpt-5"
	local := session.Session{
		Agent: "codex", ID: "source-1", Name: &name, Summary: &summary,
		WorkingDirectory: filepath.Join(root, "collector"),
		Repository:       &repository, StartedAt: 7_000, LastActivityTime: 10_000, DurationMs: intPointerForTest(2_000),
		Tokens: map[string]session.ModelTokens{"gpt-5": {InputTokens: 12, OutputTokens: 3, Cost: 0.25}},
		Cost:   0.25,
		Subagents: []session.Subagent{{
			ID: "child", Name: "reviewer", Status: session.SubagentReturned,
			Commands: []session.SubagentCommand{
				{Label: "Run unit tests", Command: "go test ./..."},
				{Label: "cat $TOKEN", Command: "cat $TOKEN"},
			},
			Tokens: map[string]session.ModelTokens{},
		}},
		SessionDetails: session.SessionDetails{
			Model: &model, FirstPrompt: &firstPrompt,
			Commands: []string{"curl -H 'Authorization: Bearer fake-secret'"},
			FileEdits: []session.FileEdit{
				{Path: filepath.Join(root, "collector", "main.go"), Additions: 3, Edits: 1},
				{Path: filepath.Join(string(filepath.Separator), "Users", "person", ".ssh", "config"), Edits: 1},
			},
			Synthesis:        &session.SessionSynthesis{Outcome: "must stay local"},
			SynthesisPending: true,
			CompactionSeed:   "must stay local",
		},
	}

	snapshot, err := Build(local, BuildOptions{CollectorVersion: "0.1.0", RepositoryRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Session.WorkingDirectory == nil || *snapshot.Session.WorkingDirectory != "collector" {
		t.Fatalf("cwd = %v", snapshot.Session.WorkingDirectory)
	}
	if snapshot.SessionStartedAtMs != 7_000 {
		t.Fatalf("session start = %d", snapshot.SessionStartedAtMs)
	}
	if len(snapshot.Session.FileEdits) != 1 || snapshot.Session.FileEdits[0].Path != "collector/main.go" {
		t.Fatalf("file edits = %#v", snapshot.Session.FileEdits)
	}
	if got := snapshot.Session.Subagents[0].CommandLabels; len(got) != 1 || got[0] != "Run unit tests" {
		t.Fatalf("command labels = %#v", got)
	}
	if snapshot.Session.Counts.Commands != 1 {
		t.Fatalf("command count = %d", snapshot.Session.Counts.Commands)
	}
	data, err := snapshotv1.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{"fake-secret", "prose-secret", "prompt-secret", "cat $TOKEN", ".ssh", "must stay local", root} {
		if bytes.Contains(data, []byte(excluded)) {
			t.Errorf("serialized snapshot leaked %q", excluded)
		}
	}
	if _, err := snapshotv1.Decode(data); err != nil {
		t.Fatal(err)
	}
	assertMetadataPointersResolve(t, data, snapshot)
}

func TestCredentialPatternsAreRedactedWithoutLeakingMetadata(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	patterns := "ghp_1234567890abcdef sk-1234567890abcdef https://person:password@example.com " +
		"-----BEGIN PRIVATE KEY-----\nprivate-material\n-----END PRIVATE KEY-----"
	local := session.Session{
		Agent: "codex", ID: "source", Repository: &repository, StartedAt: 1,
		Summary: &patterns, Tokens: map[string]session.ModelTokens{},
	}
	data, err := Marshal(local, BuildOptions{CollectorVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"ghp_1234567890abcdef", "sk-1234567890abcdef", "person:password", "private-material"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("snapshot leaked %q", secret)
		}
	}
	if !bytes.Contains(data, []byte("credential_pattern")) {
		t.Fatal("credential redaction was not recorded")
	}
}

func TestBuildAppliesUTF8AndItemBudgetsWithoutSilentTruncation(t *testing.T) {
	repository := "local-repository"
	long := strings.Repeat("界", maxNameBytes)
	commits := make([]string, maxCommitItems+1)
	for i := range commits {
		commits[i] = "commit"
	}
	local := session.Session{
		Agent: "claude", ID: "source-2", Name: &long, Repository: &repository,
		RepositoryLocalOnly: true, StartedAt: 1, LastActivityTime: 1, Tokens: map[string]session.ModelTokens{},
		SessionDetails: session.SessionDetails{Commits: commits},
	}
	snapshot, err := Build(local, BuildOptions{CollectorVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(*snapshot.Session.Name); got > maxNameBytes || !strings.HasPrefix(long, *snapshot.Session.Name) {
		t.Fatalf("name exported bytes = %d", got)
	}
	if len(snapshot.Session.Commits) != maxCommitItems {
		t.Fatalf("commits = %d", len(snapshot.Session.Commits))
	}
	if len(snapshot.Truncation) != 2 {
		t.Fatalf("truncation = %#v", snapshot.Truncation)
	}
	data, err := snapshotv1.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	assertMetadataPointersResolve(t, data, snapshot)
}

func TestMarshalIsStableAcrossTokenMapOrder(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	base := session.Session{Agent: "codex", ID: "source", Repository: &repository, StartedAt: 1, Tokens: map[string]session.ModelTokens{}, UnpricedModels: []string{"b", "a"}}
	base.Tokens["z"] = session.ModelTokens{InputTokens: 1}
	base.Tokens["a"] = session.ModelTokens{OutputTokens: 1}
	first, err := Marshal(base, BuildOptions{CollectorVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	base.Tokens = map[string]session.ModelTokens{"a": {OutputTokens: 1}, "z": {InputTokens: 1}}
	second, err := Marshal(base, BuildOptions{CollectorVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("map insertion order changed canonical bytes")
	}
}

func TestMetadataPointersUseExportedStructure(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "work", "coslash")
	repository := "github.com/centauri-ai/coslash"
	model := strings.Repeat("model/segment", 30)
	commands := func(count int) []session.SubagentCommand {
		result := make([]session.SubagentCommand, count)
		for i := range result {
			result[i] = session.SubagentCommand{Label: "safe label", Command: "raw"}
		}
		return result
	}
	secondCommands := commands(100)
	secondCommands[0].Label = secondCommands[0].Command
	local := session.Session{
		Agent: "future-agent", ID: "source", Repository: &repository, StartedAt: 1,
		WorkingDirectory: filepath.Join(string(filepath.Separator), "outside"),
		Tokens:           map[string]session.ModelTokens{model: {}},
		Subagents: []session.Subagent{
			{ID: "one", Name: "one", Status: session.SubagentReturned, Commands: commands(150), Tokens: map[string]session.ModelTokens{}},
			{ID: "two", Name: "two", Status: session.SubagentReturned, Commands: secondCommands, Tokens: map[string]session.ModelTokens{}},
		},
		SessionDetails: session.SessionDetails{FileEdits: []session.FileEdit{
			{Path: filepath.Join(string(filepath.Separator), "outside", "secret")},
			{Path: filepath.Join(root, strings.Repeat("p", maxPathBytes+10))},
		}},
	}
	snapshot, err := Build(local, BuildOptions{CollectorVersion: "0.1.0", RepositoryRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	data, err := snapshotv1.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	assertMetadataPointersResolve(t, data, snapshot)
	for _, item := range snapshot.Truncation {
		if strings.Contains(item.Path, "*") || strings.Contains(item.Path, "model/segment") {
			t.Fatalf("unstable truncation pointer: %s", item.Path)
		}
	}
	for _, item := range snapshot.Redactions {
		if strings.Contains(item.Path, "*") {
			t.Fatalf("wildcard redaction pointer: %s", item.Path)
		}
	}
}

func intPointerForTest(value int) *int { return &value }

func assertMetadataPointersResolve(t *testing.T, data []byte, snapshot snapshotv1.Snapshot) {
	t.Helper()
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for _, item := range snapshot.Truncation {
		if !pointerResolves(document, item.Path) {
			t.Errorf("truncation pointer does not resolve: %s", item.Path)
		}
	}
	for _, item := range snapshot.Redactions {
		if !pointerResolves(document, item.Path) {
			t.Errorf("redaction pointer does not resolve: %s", item.Path)
		}
	}
}

func pointerResolves(value any, pointer string) bool {
	for _, encoded := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		segment := strings.ReplaceAll(strings.ReplaceAll(encoded, "~1", "/"), "~0", "~")
		switch current := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = current[segment]
			if !ok {
				return false
			}
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(current) {
				return false
			}
			value = current[index]
		default:
			return false
		}
	}
	return true
}
