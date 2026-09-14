package cursor

import (
	"os"
	"path/filepath"
	"testing"
)

const testTranscriptID = "123e4567-e89b-42d3-a456-426614174000"

func TestParseTranscriptErrorDoesNotPersistSessionStatus(t *testing.T) {
	path := writeTranscript(t, "Users-calvin-centauri-ai", `{"role":"user","message":{"content":[{"type":"text","text":"hello"}]}}
{"type":"turn_ended","status":"error"}
`)

	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.StatusHint != nil {
		t.Fatalf("status hint = %q; want nil for a terminal error", *parsed.StatusHint)
	}
	if parsed.Session.Errors != 1 || !parsed.Stopped || parsed.InTurn {
		t.Fatalf("terminal error facts = errors %d, stopped %t, in turn %t; want 1, true, false", parsed.Session.Errors, parsed.Stopped, parsed.InTurn)
	}
}

func TestParseTranscriptDoesNotInferLossyWorkspaceSlug(t *testing.T) {
	path := writeTranscript(t, "Users-calvin-centauri-ai", `{"role":"user","message":{"content":[{"type":"text","text":"hello"}]}}
`)

	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Session.WorkingDirectory != "" {
		t.Fatalf("workspace cwd = %q; want empty for an ambiguous slug", parsed.Session.WorkingDirectory)
	}
}

func TestParseTranscriptPreservesShellWorkingDirectory(t *testing.T) {
	path := writeTranscript(t, "Users-calvin-centauri-ai", `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":{"working_directory":"/tmp/cursor-transcript-cwd"}}]}}
`)

	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Session.WorkingDirectory != "/tmp/cursor-transcript-cwd" {
		t.Fatalf("working directory = %q; want transcript Shell cwd", parsed.Session.WorkingDirectory)
	}
}

func writeTranscript(t *testing.T, slug, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "projects", slug, "agent-transcripts", testTranscriptID, testTranscriptID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if !IsTranscript(path) {
		t.Fatalf("test path is not a Cursor transcript: %s", path)
	}
	return path
}
