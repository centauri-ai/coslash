package claude

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestFilterWorkflowTranscripts(t *testing.T) {
	root := t.TempDir()
	files := []string{
		filepath.Join(root, "session.jsonl"),
		filepath.Join(root, "session", "subagents", "agent-one.jsonl"),
		filepath.Join(root, "session", "subagents", "workflows", "run", "agent-two.jsonl"),
		filepath.Join(root, "session", "subagents", "workflows", "run", "workflow.jsonl"),
	}
	want := files[:3]
	if got := filterWorkflowTranscripts(files); !slices.Equal(got, want) {
		t.Fatalf("filterWorkflowTranscripts() = %v, want %v", got, want)
	}
}
