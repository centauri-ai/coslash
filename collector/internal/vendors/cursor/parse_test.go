package cursor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestParseTranscriptPreservesUserQuestionsAsUserTurns(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	path := filepath.Join(t.TempDir(), "agent-transcripts", id, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := "" +
		`{"role":"user","message":{"content":[{"type":"text","text":"<user_query>Start here</user_query>"}]}}` + "\n" +
		`{"type":"turn_ended","status":"success"}` + "\n" +
		`{"role":"user","message":{"content":[{"type":"text","text":"<user_query>Can you fix this?</user_query>"}]}}` + "\n" +
		`{"type":"turn_ended","status":"success"}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Session.Digest) != 2 {
		t.Fatalf("digest length = %d, want 2", len(parsed.Session.Digest))
	}
	if got := parsed.Session.Digest[1]; got.Category != session.DigestUser || got.Description != "Can you fix this?" {
		t.Fatalf("second digest entry = %#v, want user turn", got)
	}
}

func TestParseTranscriptDoesNotCountPullRequestURLsFromAssistantProse(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	path := filepath.Join(t.TempDir(), "agent-transcripts", id, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := "" +
		`{"role":"user","message":{"content":[{"type":"text","text":"<user_query>Review this PR</user_query>"}]}}` + "\n" +
		`{"role":"assistant","message":{"content":[{"type":"text","text":"I reviewed https://github.com/centauri-ai/coslash/pull/184"}]}}` + "\n" +
		`{"type":"turn_ended","status":"success"}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Session.PullRequests != 0 {
		t.Fatalf("pull requests = %d, want 0", parsed.Session.PullRequests)
	}
}

func TestParseTranscriptDoesNotReportUnverifiedCommitAttempts(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	path := filepath.Join(t.TempDir(), "agent-transcripts", id, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":{"command":"git commit -m 'already used'"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Session.Commands) != 1 {
		t.Fatalf("commands = %v, want commit command retained", parsed.Session.Commands)
	}
	if len(parsed.Session.CommitLog) != 0 {
		t.Fatalf("commit log = %v, want no unverified commits", parsed.Session.CommitLog)
	}
}

func TestParseTranscriptPreservesApplyPatchMove(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	path := filepath.Join(t.TempDir(), "agent-transcripts", id, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"ApplyPatch","input":{"patch":"*** Begin Patch\n*** Update File: old.go\n*** Move to: new.go\n@@\n-old\n+new\n*** End Patch\n"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Session.FileEdits) != 1 {
		t.Fatalf("file edits = %v, want one", parsed.Session.FileEdits)
	}
	edit := parsed.Session.FileEdits[0]
	if edit.Path != "new.go" || edit.Additions != 1 || edit.Deletions != 1 {
		t.Fatalf("move edit = %#v, want new.go with one addition and deletion", edit)
	}
}

func TestParseTranscriptCountsSignPrefixedApplyPatchContent(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	path := filepath.Join(t.TempDir(), "agent-transcripts", id, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"ApplyPatch","input":{"patch":"*** Begin Patch\n*** Update File: signs.txt\n@@\n---literal\n+++literal\n*** End Patch\n"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	edit := parsed.Session.FileEdits[0]
	if edit.Additions != 1 || edit.Deletions != 1 {
		t.Fatalf("sign-prefixed edit = %#v, want one addition and deletion", edit)
	}
}
