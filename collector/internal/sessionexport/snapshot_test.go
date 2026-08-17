package sessionexport

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	patterns := `{"Authorization":"Bearer json-bearer-secret","password":"json password secret"} ` +
		`password="quoted-prefix quoted-shell-tail-9382" token='single-prefix single-shell-tail-4721' ` +
		`secret="escaped-prefix \"inside\" escaped-shell-tail-2846" ` +
		`Authorization="Bearer auth-prefix auth-shell-tail-6153" ` +
		"ghp_1234567890abcdef sk-1234567890abcdef https://person:password@example.com " +
		"-----BEGIN PRIVATE KEY-----\nprivate-material\n-----END PRIVATE KEY-----"
	local := session.Session{
		Agent: "codex", ID: "source", Repository: &repository, StartedAt: 1,
		Summary: &patterns, Tokens: map[string]session.ModelTokens{},
	}
	data, err := Marshal(local, BuildOptions{CollectorVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		"json-bearer-secret", "json password secret",
		"quoted-prefix", "quoted-shell-tail-9382",
		"single-prefix", "single-shell-tail-4721",
		"escaped-prefix", "inside", "escaped-shell-tail-2846",
		"auth-prefix", "auth-shell-tail-6153",
		"ghp_1234567890abcdef", "sk-1234567890abcdef", "person:password", "private-material",
	} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("snapshot leaked %q", secret)
		}
	}
	if !bytes.Contains(data, []byte("credential_pattern")) {
		t.Fatal("credential redaction was not recorded")
	}
}

func TestBuildResolvesRelativeFileEditsAgainstWorkingDirectory(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	repository := "github.com/centauri-ai/coslash"
	local := session.Session{
		Agent: "opencode", ID: "source", Repository: &repository, StartedAt: 1,
		WorkingDirectory: filepath.Join(root, "pkg"), Tokens: map[string]session.ModelTokens{},
		SessionDetails: session.SessionDetails{FileEdits: []session.FileEdit{{Path: "main.go"}}},
	}

	snapshot, err := Build(local, BuildOptions{CollectorVersion: "0.1.0", RepositoryRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Session.FileEdits; len(got) != 1 || got[0].Path != "pkg/main.go" {
		t.Fatalf("file edits = %#v", got)
	}

	local.WorkingDirectory = filepath.Join(string(filepath.Separator), "outside")
	snapshot, err = Build(local, BuildOptions{CollectorVersion: "0.1.0", RepositoryRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Session.FileEdits) != 0 {
		t.Fatalf("outside-cwd file edits = %#v", snapshot.Session.FileEdits)
	}
}

func TestBuildKeepsTruncatedPathsValid(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	repository := "github.com/centauri-ai/coslash"
	paths := []string{
		strings.Repeat("a/", maxPathBytes/2) + "x",
		strings.Repeat("a", maxPathBytes-3) + "/...x",
		strings.Repeat("a", maxPathBytes-2) + "/.x",
	}
	for _, path := range paths {
		local := session.Session{
			Agent: "codex", ID: "source", Repository: &repository, StartedAt: 1,
			WorkingDirectory: root, Tokens: map[string]session.ModelTokens{},
			SessionDetails: session.SessionDetails{FileEdits: []session.FileEdit{{Path: path}}},
		}

		snapshot, err := Build(local, BuildOptions{CollectorVersion: "0.1.0", RepositoryRoot: root})
		if err != nil {
			t.Fatal(err)
		}
		if got := snapshot.Session.FileEdits; len(got) != 1 || strings.HasSuffix(got[0].Path, "/") || strings.HasSuffix(got[0].Path, "/.") || strings.HasSuffix(got[0].Path, "/..") || len(got[0].Path) > maxPathBytes {
			t.Fatalf("truncated file edits = %#v", got)
		}
		if _, err := snapshotv1.Marshal(snapshot); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBuildOmitsUnprovenRelativeFileEdits(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	local := session.Session{
		Agent: "codex", ID: "source", Repository: &repository, StartedAt: 1,
		Tokens:         map[string]session.ModelTokens{},
		SessionDetails: session.SessionDetails{FileEdits: []session.FileEdit{{Path: "unproven.go"}}},
	}

	snapshot, err := Build(local, BuildOptions{CollectorVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Session.FileEdits) != 0 || len(snapshot.Redactions) != 1 || snapshot.Redactions[0].Path != "/session/fileEdits" {
		t.Fatalf("unproven edits were not structurally redacted: edits=%#v redactions=%#v", snapshot.Session.FileEdits, snapshot.Redactions)
	}
}

func TestBuildAppliesFileEditBudgetAfterPathRedaction(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	repository := "github.com/centauri-ai/coslash"
	edits := make([]session.FileEdit, 0, maxFileEditItems*2+1)
	for range maxFileEditItems {
		edits = append(edits, session.FileEdit{Path: filepath.Join(string(filepath.Separator), "outside", "secret.go")})
	}
	for i := 0; i < maxFileEditItems+1; i++ {
		edits = append(edits, session.FileEdit{Path: fmt.Sprintf("safe/%04d.go", i), Edits: 1})
	}
	local := session.Session{
		Agent: "opencode", ID: "source", Repository: &repository, StartedAt: 1,
		WorkingDirectory: root, Tokens: map[string]session.ModelTokens{},
		SessionDetails: session.SessionDetails{FileEdits: edits},
	}

	snapshot, err := Build(local, BuildOptions{CollectorVersion: "0.1.0", RepositoryRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Session.FileEdits; len(got) != maxFileEditItems || got[0].Path != "safe/0000.go" || got[len(got)-1].Path != "safe/1999.go" {
		t.Fatalf("file edits = first/last/count %#v %#v %d", got[0], got[len(got)-1], len(got))
	}
	if len(snapshot.Redactions) != 1 || snapshot.Redactions[0].Path != "/session/fileEdits" || snapshot.Redactions[0].Reason != "outside_repository" {
		t.Fatalf("file-edit redactions = %#v", snapshot.Redactions)
	}
	for _, item := range snapshot.Truncation {
		if item.Path == "/session/fileEdits" && item.Reason == snapshotv1.TruncationReasonItemBudget {
			if item.OriginalItems == nil || *item.OriginalItems != maxFileEditItems+1 || item.ExportedItems == nil || *item.ExportedItems != maxFileEditItems {
				t.Fatalf("file-edit item metadata = %#v", item)
			}
			return
		}
	}
	t.Fatal("file-edit item truncation was not recorded")
}

func TestBuildMergesModelNamesThatCollideAfterTruncation(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	prefix := strings.Repeat("m", maxModelBytes)
	first, second := prefix+"-a", prefix+"-b"
	local := session.Session{
		Agent: "codex", ID: "source", Repository: &repository, StartedAt: 1,
		Tokens: map[string]session.ModelTokens{
			first:  {InputTokens: 2, Cost: 0.25},
			second: {OutputTokens: 3, Cost: 0.50},
		},
		Cost: 0.75, UnpricedModels: []string{first, second},
	}

	snapshot, err := Build(local, BuildOptions{CollectorVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Session.Usage.Models; len(got) != 1 || got[0].Model != prefix || got[0].InputTokens != 2 || got[0].OutputTokens != 3 || got[0].EstimatedCostMicroUSD != 750_000 {
		t.Fatalf("merged usage = %#v", got)
	}
	if got := snapshot.Session.Usage.UnpricedModels; len(got) != 1 || got[0] != prefix {
		t.Fatalf("merged unpriced models = %#v", got)
	}
	if _, err := snapshotv1.Marshal(snapshot); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSortsAndMergesModelsAfterTransformation(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	secretModel := "ghp_1234567890abcdef"
	secondSecret := "sk-1234567890abcdef"
	local := session.Session{
		Agent: "codex", ID: "source", Repository: &repository, StartedAt: 1,
		Tokens: map[string]session.ModelTokens{
			"":           {InputTokens: 100},
			"a-model":    {InputTokens: 1},
			secretModel:  {InputTokens: 2},
			secondSecret: {OutputTokens: 3},
		},
		UnpricedModels: []string{"", "a-model", secretModel, secondSecret},
	}

	snapshot, err := Build(local, BuildOptions{CollectorVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	models := snapshot.Session.Usage.Models
	if len(models) != 2 || models[0].Model != "[REDACTED]" || models[0].InputTokens != 2 || models[0].OutputTokens != 3 || models[1].Model != "a-model" {
		t.Fatalf("transformed models = %#v", models)
	}
	if got := snapshot.Session.Usage.UnpricedModels; len(got) != 2 || got[0] != "[REDACTED]" || got[1] != "a-model" {
		t.Fatalf("transformed unpriced models = %#v", got)
	}
	data, err := snapshotv1.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{secretModel, secondSecret} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("snapshot leaked model credential %q", secret)
		}
	}
}

func TestBuildUsesWirePathsForSubagentUsageAndOmitsEmptyModels(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	emptyModel := ""
	longModel := strings.Repeat("m", maxModelBytes+1)
	local := session.Session{
		Agent: "codex", ID: "source", Repository: &repository, StartedAt: 1,
		Tokens:         map[string]session.ModelTokens{},
		SessionDetails: session.SessionDetails{Model: &emptyModel},
		Subagents: []session.Subagent{{
			ID: "child", Name: "worker", Model: &emptyModel, Status: session.SubagentReturned,
			Tokens: map[string]session.ModelTokens{longModel: {InputTokens: 1}},
		}},
	}

	snapshot, err := Build(local, BuildOptions{CollectorVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Session.Model != nil || snapshot.Session.Subagents[0].Model != nil {
		t.Fatalf("empty models were exported: session=%v subagent=%v", snapshot.Session.Model, snapshot.Session.Subagents[0].Model)
	}
	if got := snapshot.Truncation; len(got) != 1 || got[0].Path != "/session/subagents/0/usage/0/model" {
		t.Fatalf("subagent usage truncation = %#v", got)
	}
	if _, err := snapshotv1.Marshal(snapshot); err != nil {
		t.Fatal(err)
	}
}

func TestBuildRetainsNewestDigestEntriesAtItemBudget(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	digest := make([]session.DigestEntry, maxDigestItems+5)
	for i := range digest {
		digest[i] = session.DigestEntry{Turn: i, Category: session.DigestUser, Description: "entry"}
	}
	local := session.Session{
		Agent: "codex", ID: "source", Repository: &repository, StartedAt: 1,
		Tokens:         map[string]session.ModelTokens{},
		SessionDetails: session.SessionDetails{Digest: digest},
	}

	snapshot, err := Build(local, BuildOptions{CollectorVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Session.Digest; len(got) != maxDigestItems || got[0].Turn != 5 || got[len(got)-1].Turn != maxDigestItems+4 {
		t.Fatalf("retained digest range = first %d, last %d, count %d", got[0].Turn, got[len(got)-1].Turn, len(got))
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
