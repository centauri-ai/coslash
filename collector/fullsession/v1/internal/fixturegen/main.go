package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
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
	valid, err := fullsessionv1.Freeze(record())
	if err != nil {
		panic(err)
	}
	canonical, err := fullsessionv1.Marshal(valid)
	if err != nil {
		panic(err)
	}
	entries := []struct {
		path   string
		data   []byte
		valid  bool
		reason string
	}{
		{"valid/codex.json", canonical, true, ""},
		{"invalid/malformed.json", []byte(`{"schemaVersion":`), false, "malformed JSON"},
		{"invalid/unknown-field.json", bytes.Replace(canonical, []byte(`"schemaVersion"`), []byte(`"unknown":true,"schemaVersion"`), 1), false, "unknown field"},
		{"invalid/incomplete-body.json", bytes.Replace(canonical, []byte(`"text":"@@\n-old\n+new\n"`), []byte(`"text":""`), 1), false, "missing declared change body"},
		{"invalid/timestamp-out-of-range.json", bytes.Replace(canonical, []byte(`"lastActivityAtMs":1800000012000`), []byte(`"lastActivityAtMs":253402300800000`), 1), false, "session timestamp exceeds year 9999"},
		{"invalid/bad-revision.json", bytes.Replace(canonical, []byte(valid.RevisionID), []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 1), false, "revision hash mismatch"},
		{"invalid/bad-body-hash.json", bytes.Replace(canonical, []byte(valid.Session.FileEdits[0].Changes[0].SHA256), []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), 1), false, "change hash mismatch"},
	}
	output := manifest{SchemaVersion: fullsessionv1.SchemaVersion, CollectorVersion: "fixturegen/1"}
	for _, entry := range entries {
		path := filepath.Join("testdata", "fixtures", entry.path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(path, entry.data, 0o644); err != nil {
			panic(err)
		}
		sum := sha256.Sum256(entry.data)
		output.Fixtures = append(output.Fixtures, manifestEntry{Path: entry.path, SHA256: hex.EncodeToString(sum[:]), Valid: entry.valid, Reason: entry.reason})
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

func record() fullsessionv1.Record {
	name, summary, status := "Complete SSH fixture", "Preserves parsed detail and ordered changes.", "inactive"
	branch, entrypoint, model := "feature/full-data", "codex", "gpt-5"
	firstPrompt, goal := "Restore the complete session without dropping diffs.", "Ship one complete thin path."
	duration, contextTokens, contextWindow := 12_000, 12_345, 272_000
	subModel, subDuration, subTurn := "gpt-5-mini", 2_000, 2
	return fullsessionv1.Record{
		SourceID: "r_0123456789abcdef", Agent: "codex", SessionID: "019f4dde-db5b-7100-bdc0-09b5aaaac56f",
		Session: fullsessionv1.Session{
			Name: &name, Summary: &summary, Status: &status, WorkingDirectory: "/workspace/coslash",
			Branch: &branch, EditedFileCount: 1, DurationMs: &duration,
			Usage:        []fullsessionv1.ModelUsage{{Model: "gpt-5", InputTokens: 2000, OutputTokens: 400, CacheReadInputTokens: 800, CostMicroUSD: 125000}},
			CostMicroUSD: 125000, UnpricedModels: []string{}, StartedAtMs: 1_800_000_000_000,
			LastActivityAtMs: 1_800_000_012_000, Entrypoint: &entrypoint, Model: &model,
			ContextTokens: &contextTokens, ContextWindow: &contextWindow, Turns: 3, ToolUses: 4,
			FirstPrompt: &firstPrompt, Commands: []string{"go test ./..."}, Commits: []string{"feat: preserve complete SSH records"},
			CommitSHAs: []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, Todos: []fullsessionv1.Todo{{Text: "verify restart", Done: true}},
			Digest: []fullsessionv1.DigestEntry{{Turn: 1, Category: "first_prompt", Description: firstPrompt, TimeMs: 1_800_000_000_000}},
			FileEdits: []fullsessionv1.FileEdit{{Path: "collector/example.go", Additions: 2, Deletions: 1, Edits: 2, Changes: []fullsessionv1.FileChange{
				{Kind: "diff", Operation: "Patch", Additions: 1, Deletions: 1, Text: "@@\n-old\n+new\n"},
				{Kind: "content", Operation: "Write", Additions: 1, Text: "package example\n"},
			}}},
			Subagents:    []fullsessionv1.Subagent{{ID: "agent-1", Name: "verify", Model: &subModel, Status: "returned", Task: "run focused tests", Result: "all passed", DurationMs: &subDuration, SpawnedAtTurn: &subTurn, ToolUses: 1, Commands: []fullsessionv1.SubagentCommand{{Label: "tests", Command: "go test ./..."}}, Usage: []fullsessionv1.ModelUsage{}, CostMicroUSD: 1000}},
			Synthesis:    &fullsessionv1.SessionSynthesis{Goals: []string{goal}, Outcome: "complete", KeyDecisions: []string{"use exact revisions"}, NextStep: "handoff"},
			DeclaredGoal: &goal,
		},
	}
}
