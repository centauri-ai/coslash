package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionexport"
	snapshotv1 "github.com/centauri-ai/coslash/collector/snapshot/v1"
)

type manifest struct {
	SchemaVersion    string          `json:"schemaVersion"`
	CollectorVersion string          `json:"collectorVersion"`
	Fixtures         []manifestEntry `json:"fixtures"`
}

type manifestEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

func main() {
	entries := []struct {
		path   string
		data   []byte
		valid  bool
		reason string
	}{
		{path: "valid/codex.json", data: mustMarshal(codex()), valid: true},
		{path: "valid/claude.json", data: mustMarshal(claude()), valid: true},
		{path: "valid/redaction.json", data: mustMarshal(redaction()), valid: true},
		{path: "valid/boundary-metadata.json", data: mustMarshal(boundaryMetadata()), valid: true},
		{path: "valid/boundary-work.json", data: mustMarshal(boundaryWork()), valid: true},
		{path: "valid/boundary-subagent.json", data: mustMarshal(boundarySubagent()), valid: true},
		{path: "valid/boundary-items.json", data: mustMarshal(boundaryItems()), valid: true},
		{path: "valid/heavy-aggregate.json", data: heavyAggregate(), valid: true},
	}
	base := entries[0].data
	entries = append(entries,
		struct {
			path   string
			data   []byte
			valid  bool
			reason string
		}{
			path: "invalid/unknown-field.json", data: bytes.Replace(base, []byte(`"schemaVersion"`), []byte(`"unknown":true,"schemaVersion"`), 1), reason: "unknown field",
		},
		struct {
			path   string
			data   []byte
			valid  bool
			reason string
		}{
			path: "invalid/negative-count.json", data: bytes.Replace(base, []byte(`"turns":3`), []byte(`"turns":-1`), 1), reason: "negative count",
		},
		struct {
			path   string
			data   []byte
			valid  bool
			reason string
		}{
			path: "invalid/bad-hash.json", data: corruptHash(base), reason: "bad hash",
		},
		struct {
			path   string
			data   []byte
			valid  bool
			reason string
		}{
			path: "invalid/non-canonical.json", data: append(append([]byte(nil), base...), '\n'), reason: "non-canonical JSON",
		},
		struct {
			path   string
			data   []byte
			valid  bool
			reason string
		}{
			path: "invalid/oversized.json", data: bytes.Repeat([]byte("x"), snapshotv1.MaxPayloadBytes+1), reason: "aggregate limit",
		},
	)

	output := manifest{SchemaVersion: snapshotv1.SchemaVersion, CollectorVersion: "fixturegen/1", Fixtures: make([]manifestEntry, 0, len(entries))}
	for _, entry := range entries {
		path := filepath.Join("testdata", "fixtures", entry.path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(path, entry.data, 0o644); err != nil {
			panic(err)
		}
		sum := sha256.Sum256(entry.data)
		output.Fixtures = append(output.Fixtures, manifestEntry{
			Path: entry.path, SHA256: hex.EncodeToString(sum[:]), Valid: entry.valid, Reason: entry.reason,
		})
	}
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		panic(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join("testdata", "fixtures", "manifest.json"), data, 0o644); err != nil {
		panic(err)
	}
}

func codex() snapshotv1.Snapshot {
	name, summary, cwd, branch, model := "Implement snapshot export", "Contract and redaction boundary implemented.", "collector", "feature/eng-1340", "gpt-5"
	goal, prompt := "Ship an explicit export allow-list.", "Implement ENG-1340 without exposing raw commands."
	return snapshotv1.Snapshot{
		SchemaVersion: snapshotv1.SchemaVersion, MediaType: snapshotv1.MediaType, CollectorVersion: "0.1.0",
		Agent: "codex", SourceSessionID: "codex-fixture-1", SessionStartedAtMs: 1_700_000_000_000,
		Repository: snapshotv1.Repository{Canonical: "github.com/centauri-ai/coslash"},
		Truncation: []snapshotv1.Truncation{}, Redactions: []snapshotv1.Redaction{},
		Session: snapshotv1.Session{
			Name: &name, Summary: &summary, WorkingDirectory: &cwd, Branch: &branch, Model: &model,
			DurationMs: intp(120_000), LastActivityAtMs: 1_700_000_120_000, DeclaredGoal: &goal, FirstPrompt: &prompt,
			Counts:    snapshotv1.Counts{EditedFiles: 2, Turns: 3, ToolUses: 4, Commands: 2},
			Usage:     snapshotv1.Usage{Models: []snapshotv1.ModelUsage{{Model: "gpt-5", InputTokens: 1000, OutputTokens: 200, EstimatedCostMicroUSD: 250000}}, EstimatedCostMicroUSD: 250000, UnpricedModels: []string{}},
			Digest:    []snapshotv1.Digest{{Turn: 1, Category: "user", Description: "Requested a bounded export contract."}},
			Todos:     []snapshotv1.Todo{{Text: "Run compatibility fixtures", Done: true}},
			FileEdits: []snapshotv1.FileEdit{{Path: "collector/snapshot/v1/snapshot.go", Additions: 100, Edits: 1, IsNew: true}},
			Commits:   []string{"feat: add snapshot v1 contract"},
			Subagents: []snapshotv1.Subagent{},
		},
	}
}

func claude() snapshotv1.Snapshot {
	name := "Review contract"
	return snapshotv1.Snapshot{
		SchemaVersion: snapshotv1.SchemaVersion, MediaType: snapshotv1.MediaType, CollectorVersion: "0.1.0",
		Agent: "claude", SourceSessionID: "claude-fixture-1", SessionStartedAtMs: 1_700_001_000_000,
		Repository: snapshotv1.Repository{Canonical: "local-project", LocalOnly: true},
		Truncation: []snapshotv1.Truncation{}, Redactions: []snapshotv1.Redaction{},
		Session: snapshotv1.Session{
			Name: &name, LastActivityAtMs: 1_700_001_001_000, Counts: snapshotv1.Counts{Turns: 1},
			Usage:  snapshotv1.Usage{Models: []snapshotv1.ModelUsage{}, UnpricedModels: []string{"unknown-model"}},
			Digest: []snapshotv1.Digest{}, Todos: []snapshotv1.Todo{}, FileEdits: []snapshotv1.FileEdit{}, Commits: []string{},
			Subagents: []snapshotv1.Subagent{},
		},
	}
}

func redaction() snapshotv1.Snapshot {
	snapshot := codex()
	snapshot.SourceSessionID = "redaction-fixture-1"
	snapshot.Redactions = []snapshotv1.Redaction{
		{Path: "/session", Reason: "outside_repository"},
		{Path: "/session/fileEdits", Reason: "outside_repository"},
		{Path: "/session/subagents", Reason: "raw_command"},
	}
	snapshot.Session.WorkingDirectory = nil
	return snapshot
}

func boundaryMetadata() snapshotv1.Snapshot {
	snapshot := codex()
	snapshot.Agent = strings.Repeat("a", snapshotv1.MaxAgentBytes)
	snapshot.CollectorVersion = strings.Repeat("v", snapshotv1.MaxCollectorVersionBytes)
	snapshot.SourceSessionID = strings.Repeat("i", snapshotv1.MaxIdentifierBytes)
	snapshot.Repository.Canonical = strings.Repeat("r", snapshotv1.MaxRepositoryBytes)
	snapshot.Session.Name = stringp(strings.Repeat("n", snapshotv1.MaxNameBytes))
	snapshot.Session.Summary = stringp(strings.Repeat("s", snapshotv1.MaxSummaryBytes))
	snapshot.Session.Status = stringp(strings.Repeat("z", 64))
	snapshot.Session.WorkingDirectory = stringp(strings.Repeat("p", snapshotv1.MaxPathBytes))
	snapshot.Session.Branch = stringp(strings.Repeat("b", snapshotv1.MaxBranchBytes))
	snapshot.Session.Entrypoint = stringp(strings.Repeat("e", snapshotv1.MaxEntrypointBytes))
	snapshot.Session.Model = stringp(strings.Repeat("m", snapshotv1.MaxModelBytes))
	snapshot.Session.DeclaredGoal = stringp(strings.Repeat("g", snapshotv1.MaxGoalBytes))
	snapshot.Session.FirstPrompt = stringp(strings.Repeat("f", snapshotv1.MaxPromptBytes))
	snapshot.Session.Usage.Models = []snapshotv1.ModelUsage{{Model: strings.Repeat("u", snapshotv1.MaxModelBytes)}}
	snapshot.Session.Usage.UnpricedModels = []string{strings.Repeat("x", snapshotv1.MaxModelBytes)}
	return snapshot
}

func boundaryWork() snapshotv1.Snapshot {
	snapshot := claude()
	snapshot.SourceSessionID = "boundary-work"
	snapshot.Session.Digest = []snapshotv1.Digest{{
		Category:    strings.Repeat("c", 64),
		Description: strings.Repeat("d", snapshotv1.MaxDigestTextBytes),
		Answer:      stringp(strings.Repeat("a", snapshotv1.MaxDigestTextBytes)),
		SubagentID:  stringp(strings.Repeat("i", snapshotv1.MaxIdentifierBytes)),
	}}
	snapshot.Session.Todos = []snapshotv1.Todo{{Text: strings.Repeat("t", snapshotv1.MaxTodoTextBytes)}}
	snapshot.Session.FileEdits = []snapshotv1.FileEdit{{Path: strings.Repeat("p", snapshotv1.MaxPathBytes)}}
	snapshot.Session.Commits = []string{strings.Repeat("c", snapshotv1.MaxCommitTextBytes)}
	snapshot.Session.Git = &snapshotv1.GitDrift{BaseBranch: strings.Repeat("b", snapshotv1.MaxBranchBytes)}
	return snapshot
}

func boundarySubagent() snapshotv1.Snapshot {
	snapshot := claude()
	snapshot.SourceSessionID = "boundary-subagent"
	snapshot.Session.Subagents = []snapshotv1.Subagent{{
		ID:            strings.Repeat("i", snapshotv1.MaxIdentifierBytes),
		Name:          strings.Repeat("n", snapshotv1.MaxNameBytes),
		Model:         stringp(strings.Repeat("m", snapshotv1.MaxModelBytes)),
		Status:        strings.Repeat("s", 64),
		Task:          strings.Repeat("t", snapshotv1.MaxSubagentTextBytes),
		Result:        strings.Repeat("r", snapshotv1.MaxSubagentTextBytes),
		CommandLabels: []string{strings.Repeat("l", snapshotv1.MaxCommandLabelBytes)},
		Usage:         []snapshotv1.ModelUsage{{Model: strings.Repeat("u", snapshotv1.MaxModelBytes)}},
	}}
	return snapshot
}

func boundaryItems() snapshotv1.Snapshot {
	snapshot := claude()
	snapshot.SourceSessionID = "boundary-items"
	snapshot.Session.Usage.Models = fixtureUsage(snapshotv1.MaxUsageModels)
	snapshot.Session.Usage.UnpricedModels = fixtureStrings("u", snapshotv1.MaxUnpricedModels)
	snapshot.Session.Digest = make([]snapshotv1.Digest, snapshotv1.MaxDigestItems)
	for i := range snapshot.Session.Digest {
		snapshot.Session.Digest[i] = snapshotv1.Digest{Category: "x"}
	}
	snapshot.Session.Todos = make([]snapshotv1.Todo, snapshotv1.MaxTodoItems)
	snapshot.Session.FileEdits = make([]snapshotv1.FileEdit, snapshotv1.MaxFileEditItems)
	for i := range snapshot.Session.FileEdits {
		snapshot.Session.FileEdits[i] = snapshotv1.FileEdit{Path: "x"}
	}
	snapshot.Session.Commits = make([]string, snapshotv1.MaxCommitItems)
	snapshot.Session.Subagents = make([]snapshotv1.Subagent, snapshotv1.MaxSubagentItems)
	for i := range snapshot.Session.Subagents {
		snapshot.Session.Subagents[i] = snapshotv1.Subagent{
			ID: "i", Name: "n", Status: "s", CommandLabels: []string{}, Usage: []snapshotv1.ModelUsage{},
		}
	}
	snapshot.Session.Subagents[0].CommandLabels = fixtureStrings("l", snapshotv1.MaxCommandLabelItems)
	snapshot.Session.Subagents[0].Usage = fixtureUsage(snapshotv1.MaxUsageModels)
	return snapshot
}

func fixtureUsage(n int) []snapshotv1.ModelUsage {
	result := make([]snapshotv1.ModelUsage, n)
	for i := range result {
		result[i].Model = fmt.Sprintf("m%05d", i)
	}
	return result
}

func fixtureStrings(prefix string, n int) []string {
	result := make([]string, n)
	for i := range result {
		result[i] = fmt.Sprintf("%s%05d", prefix, i)
	}
	return result
}

// heavyAggregate starts above the aggregate ceiling after per-field budgets.
// It goes through the real private-to-wire mapper so the fixture pins
// deterministic aggregate fitting and mapper-to-server compatibility.
func heavyAggregate() []byte {
	repository := "github.com/centauri-ai/coslash"
	todoText := strings.Repeat("t", snapshotv1.MaxTodoTextBytes)
	label := strings.Repeat("l", snapshotv1.MaxCommandLabelBytes)
	commands := make([]session.SubagentCommand, snapshotv1.MaxCommandLabelItems)
	for i := range commands {
		commands[i] = session.SubagentCommand{Label: label, Command: fmt.Sprintf("raw-%d", i)}
	}
	todos := make([]session.Todo, 80)
	for i := range todos {
		todos[i] = session.Todo{Text: todoText}
	}
	local := session.Session{
		Agent: "codex", ID: "heavy-aggregate-fixture", Repository: &repository,
		StartedAt: 1_700_002_000_000, LastActivityTime: 1_700_002_001_000,
		Tokens: map[string]session.ModelTokens{},
		Subagents: []session.Subagent{{
			ID: "child", Name: "reviewer", Status: session.SubagentReturned,
			Task:     strings.Repeat("q", snapshotv1.MaxSubagentTextBytes),
			Result:   strings.Repeat("r", snapshotv1.MaxSubagentTextBytes),
			Commands: commands, Tokens: map[string]session.ModelTokens{},
		}},
		SessionDetails: session.SessionDetails{Todos: todos},
	}
	data, err := sessionexport.Marshal(local, sessionexport.BuildOptions{CollectorVersion: "fixturegen/1"})
	if err != nil {
		panic(fmt.Sprintf("marshal heavy aggregate fixture: %v", err))
	}
	return data
}

func mustMarshal(snapshot snapshotv1.Snapshot) []byte {
	data, err := snapshotv1.Marshal(snapshot)
	if err != nil {
		panic(fmt.Sprintf("marshal fixture: %v", err))
	}
	return data
}

func corruptHash(data []byte) []byte {
	result := append([]byte(nil), data...)
	index := bytes.Index(result, []byte("sha256:")) + len("sha256:")
	if result[index] == 'f' {
		result[index] = 'e'
	} else {
		result[index] = 'f'
	}
	return result
}

func intp(value int) *int { return &value }

func stringp(value string) *string { return &value }
