package agentexec

import (
	"slices"
	"testing"
)

func TestWithoutSessionMarkersKeepsConfigurationAndCredentials(t *testing.T) {
	env := []string{
		"CLAUDECODE=1",
		"CLAUDE_CODE_CHILD_SESSION=1",
		"CLAUDE_CODE_SESSION_ID=parent",
		"CLAUDE_CODE_MESSAGING_TOKEN=secret",
		"CODEX_SESSION_ID=parent",
		"CODEX_THREAD_ID=parent",
		"PATH=/bin",
		"HOME=/home/user",
		"CLAUDE_CODE_USE_FOUNDRY=1",
		"CODEX_HOME=/home/user/.codex",
		"CODEX_AZURE_OPENAI_API_KEY=key",
		"CLAUDECODE_SUFFIX=kept",
	}
	original := slices.Clone(env)
	want := []string{
		"PATH=/bin",
		"HOME=/home/user",
		"CLAUDE_CODE_USE_FOUNDRY=1",
		"CODEX_HOME=/home/user/.codex",
		"CODEX_AZURE_OPENAI_API_KEY=key",
		"CLAUDECODE_SUFFIX=kept",
	}
	if got := WithoutSessionMarkers(env); !slices.Equal(got, want) {
		t.Fatalf("WithoutSessionMarkers() = %q, want %q", got, want)
	}
	if !slices.Equal(env, original) {
		t.Fatalf("WithoutSessionMarkers() changed its input: %q", env)
	}
}
